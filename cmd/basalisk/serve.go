package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/httphost"
	"github.com/hollis-labs/substrate/modules/identity"
	"github.com/hollis-labs/substrate/modules/secrets"

	"github.com/hollis-labs/basalisk/internal/agent"
	"github.com/hollis-labs/basalisk/internal/api"
	"github.com/hollis-labs/basalisk/internal/feeds"
	"github.com/hollis-labs/basalisk/internal/hostguard"
	"github.com/hollis-labs/basalisk/internal/inbox"
	"github.com/hollis-labs/basalisk/internal/macshell"
	"github.com/hollis-labs/basalisk/internal/scratch"
	"github.com/hollis-labs/basalisk/internal/settings"
	"github.com/hollis-labs/basalisk/internal/story"
	"github.com/hollis-labs/basalisk/internal/terminal"
	"github.com/hollis-labs/basalisk/internal/tetherlink"
	"github.com/hollis-labs/basalisk/internal/webui"
	"github.com/hollis-labs/basalisk/internal/work"
)

// basalisk is what the commands share: the started framework app. ready, when set, learns the
// bound address (tests listen on port 0).
type basalisk struct {
	app   *module.App
	ready func(net.Addr)
	// Set by the desktop command only: the native shell and the terminal. A plain
	// `basalisk serve` leaves both nil and serves neither.
	shell macshell.Controller
	term  *terminal.Manager
}

func (b *basalisk) serveCmd(ctx context.Context) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Serve the dashboard, its API and the companion API",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return b.serve(ctx) },
	}
}

// serve runs the HTTP host, the observation pruner and, when configured, the fragment
// delivery worker, until ctx ends. It then waits for the pruner and the worker, so an
// interrupted delivery attempt saves its retry state before the database closes.
func (b *basalisk) serve(ctx context.Context) error {
	app := b.app
	cfg, err := settings.Parse(app.Config[settings.Key])
	if err != nil {
		return err
	}
	if err = requireLoopback(app); err != nil {
		return err
	}
	st := database.From(app)
	resolver := secrets.Resolver(app)
	id := identity.From(app)
	if st == nil || resolver == nil || id == nil {
		return errors.New("the database, secrets and identity modules must be started")
	}
	svc, err := api.New(st.DB(), resolver)
	if err != nil {
		return err
	}
	// A note's source link needs an absolute URL even with no public origin configured.
	if hc, _ := app.Config["httphost"].(map[string]any); hc != nil {
		if addr, _ := hc["addr"].(string); addr != "" {
			cfg.Delivery.LocalOrigin = "http://" + addr
		}
	}
	worker, err := svc.NewFragmentWorker(cfg.Delivery, nil)
	if err != nil {
		return err
	}
	a := svc.API(api.IdentityUsers{Svc: id})
	var agentRT *agent.Runtime
	scratchPath := ""
	// The desktop's settings documents live beside the database, in the private data directory.
	if db, _ := app.Config["database"].(map[string]any); db != nil {
		if sq, _ := db["sqlite"].(map[string]any); sq != nil {
			if p, _ := sq["path"].(string); filepath.IsAbs(p) {
				a.SetDeskDir(filepath.Join(filepath.Dir(p), "desk"))
				agentRT = enableAgent(ctx, a, cfg.Agent, filepath.Join(filepath.Dir(p), "agent.db")) // agent.go
				scratchPath = filepath.Join(filepath.Dir(p), "scratch.db")
			}
		}
	}
	// References (Torque tasks, Tesseract items) resolve through the Tether gateway, when
	// one is configured. It starts on first use and stops with the server.
	link := tetherlink.New(cfg.TetherMCP)
	a.EnableCite(link)
	// Scratch: the owner's quick items, kept beside the database and written on to Caster
	// through the same gateway (internal/scratch).
	if scratchPath != "" {
		if store, err := scratch.Open(ctx, scratchPath); err != nil {
			slog.Warn("scratch is off: its store could not be opened", "path", scratchPath, "err", err)
		} else {
			syncer := scratch.NewSyncer(store, link)
			a.EnableScratch(store, syncer, link)
			go syncer.Run(ctx)
			go func() {
				<-ctx.Done()
				_ = store.Close()
			}()
			if agentRT != nil {
				agentRT.AddTools(agent.ScratchTools(store, syncer.Kick, a.ScratchAuto))
			}
		}
	}
	// The Reader's feeds (RSS and Atom), kept beside the database (internal/feeds).
	if scratchPath != "" {
		feedsPath := filepath.Join(filepath.Dir(scratchPath), "feeds.db")
		if store, err := feeds.Open(ctx, feedsPath); err != nil {
			slog.Warn("reader feeds are off: their store could not be opened", "path", feedsPath, "err", err)
		} else {
			a.EnableReader(ctx, store)
			go func() {
				<-ctx.Done()
				_ = store.Close()
			}()
		}
	}
	// The inbox: the owner's Tether mail, through the same gateway (internal/inbox).
	if link.Configured() {
		addresses := cfg.InboxAddresses
		if len(addresses) == 0 {
			addresses = []string{story.RoundFrom}
		}
		a.EnableInbox(inbox.New(&inbox.Tether{Link: link, Addresses: addresses}))
	}
	// Story: scripts and storyboards, kept in Caster's "story" namespace through the same
	// gateway, with a working copy beside the database (internal/story).
	if scratchPath != "" {
		var caster story.Caster
		var tell story.Notifier
		var keep story.Archiver
		if link.Configured() {
			caster = story.GatewayCaster{Link: link}
			tell = story.GatewayNotifier{Link: link}
			keep = story.GatewayArchiver{Link: link}
		}
		storyPath := filepath.Join(filepath.Dir(scratchPath), "story.db")
		if store, err := story.Open(ctx, storyPath, caster); err != nil {
			slog.Warn("story is off: its store could not be opened", "path", storyPath, "err", err)
		} else {
			a.EnableStory(store, tell, keep)
			go func() {
				<-ctx.Done()
				_ = store.Close()
			}()
		}
	}
	// Work: the engine under Content, Planner and Writer, kept in Caster ("work" and a
	// namespace per workspace) through the same gateway, with a working copy beside the
	// database (internal/work).
	if scratchPath != "" {
		var caster work.Caster
		if link.Configured() {
			caster = work.GatewayCaster{Link: link}
		}
		workPath := filepath.Join(filepath.Dir(scratchPath), "work.db")
		if store, err := work.Open(ctx, workPath, caster); err != nil {
			slog.Warn("work is off: its store could not be opened", "path", workPath, "err", err)
		} else {
			a.EnableWork(store)
			go func() {
				<-ctx.Done()
				_ = store.Close()
			}()
		}
	}
	if agentRT != nil {
		// The agent reaches Torque and Tesseract through the same gateway (internal/agent/tether.go).
		tools := agent.TetherTools(link, agent.TetherOptions{URN: agentRT.Config().URN})
		agentRT.AddTools(tools)
		// Ask the gateway for them now, not at the first message: a message sent before they
		// arrive goes out without them, and their arrival then changes the prompt's prefix,
		// which the provider has to cache again in full.
		tools(ctx)
	}
	go func() {
		<-ctx.Done()
		link.Close()
	}()
	if b.shell != nil {
		a.EnableShell(b.shell)
	}
	// BASALISK_TERMINAL=1 turns the terminal on for a plain `basalisk serve` as well: a shell
	// on the machine that serves, for whoever can sign in. Off unless asked for.
	if b.term == nil && os.Getenv("BASALISK_TERMINAL") == "1" {
		b.term = terminal.NewManager()
	}
	if b.term != nil {
		a.EnableTerminal(b.term)
		// End the shells as soon as the server is asked to stop: that closes their sockets,
		// so the HTTP host does not wait on an open terminal.
		go func() {
			<-ctx.Done()
			b.term.CloseAll()
		}()
	}
	enableFiles(ctx, b, a) // files.go
	if b.shell != nil {
		a.EnableBrowser(macshell.DefaultBrowser())
	}
	a.RegisterScopes(auth.ScopeTable(app))

	assets, err := webui.Assets()
	if err != nil {
		return err
	}
	handler, err := httphost.Handler(app, httphost.Options{Assets: assets, Routes: a.Handler(), Mounts: a.Mounts()})
	if err != nil {
		return err
	}
	// Outermost first: the Host check, then the session cookie becomes the bearer the guard
	// verifies, then the host (guard, routes, SPA).
	handler = hostguard.Handler(cfg.AllowedHosts, id.Middleware(handler))

	background, stopBackground := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { svc.PruneObservations(background) })
	if worker != nil {
		wg.Go(func() { worker.Run(background) })
	}
	serveErr := httphost.Serve(ctx, app, handler, httphost.ServeOptions{
		Ready: func(addr net.Addr) {
			slog.Info("basalisk listening", "addr", addr.String())
			if b.ready != nil {
				b.ready(addr)
			}
		},
	})
	stopBackground()
	wg.Wait()
	return serveErr
}

// requireLoopback keeps the backend on the loopback interface: the Host check assumes it, and
// a remote client reaches it through an authenticated tunnel or a local TLS proxy.
func requireLoopback(app *module.App) error {
	cfg, _ := app.Config["httphost"].(map[string]any)
	addr, _ := cfg["addr"].(string)
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		if host == "localhost" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("basalisk currently requires a loopback httphost.addr (for example 127.0.0.1:8085), got %q", addr)
}
