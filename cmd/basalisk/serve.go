package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/httphost"
	"github.com/hollis-labs/substrate/modules/identity"
	"github.com/hollis-labs/substrate/modules/secrets"

	"github.com/hollis-labs/basalisk/internal/api"
	"github.com/hollis-labs/basalisk/internal/hostguard"
	"github.com/hollis-labs/basalisk/internal/settings"
	"github.com/hollis-labs/basalisk/internal/webui"
)

// basalisk is what the commands share: the started framework app. ready, when set, learns the
// bound address (tests listen on port 0).
type basalisk struct {
	app   *module.App
	ready func(net.Addr)
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
	worker, err := svc.NewFragmentWorker(cfg.Delivery, nil)
	if err != nil {
		return err
	}
	a := svc.API(api.IdentityUsers{DB: st.DB()})
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
