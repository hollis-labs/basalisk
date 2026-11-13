//go:build darwin && desktop

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/hollis-labs/basalisk/internal/macshell"
	"github.com/hollis-labs/basalisk/internal/terminal"
)

// The desktop shell: the same server `basalisk serve` runs, in this process, with a native
// window pointed at it. The window is a Wails webview loading the loopback URL; there is no
// Wails asset server and there are no generated bindings. The page reaches the native layer
// through the ordinary API (/api/shell, /api/terminal), so the same UI works in a browser,
// where those report themselves unavailable.

func (b *basalisk) desktopCmd(ctx context.Context) *cobra.Command {
	return &cobra.Command{
		Use:   "desktop",
		Short: "Open the desktop window (serves the dashboard in this process)",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return b.desktop(ctx) },
	}
}

func (b *basalisk) desktop(ctx context.Context) error {
	b.shell = macshell.Default()
	b.term = terminal.NewManager()

	ready := make(chan net.Addr, 1)
	b.ready = func(a net.Addr) { ready <- a }
	serveCtx, stopServe := context.WithCancel(ctx)
	defer stopServe()
	served := make(chan error, 1)
	go func() { served <- b.serve(serveCtx) }()

	var addr net.Addr
	select {
	case addr = <-ready:
	case err := <-served:
		if err == nil {
			err = errors.New("the server stopped before it was ready")
		}
		return err
	}

	app := application.New(application.Options{
		Name:        "Basalisk",
		Description: "A personal operating system for your data, tools and connections",
		Mac:         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		Logger:      slog.Default(),
		LogLevel:    slog.LevelWarn,
		// main owns SIGINT and SIGTERM (the context passed in).
		DisableDefaultSignalHandler: true,
	})
	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "Basalisk",
		URL:              "http://" + addr.String() + "/",
		Width:            1440,
		Height:           900,
		MinWidth:         720,
		MinHeight:        480,
		BackgroundColour: application.NewRGB(14, 16, 20),
		DevToolsEnabled:  true,
	})

	// Shell events reach the page as a DOM event, so the UI updates without polling. The
	// script goes through the shell's own evaluate call: Wails' ExecJS waits for a Wails
	// runtime that a page loaded from the loopback server never has. Each one is sent from
	// its own goroutine because the log is written from inside shell calls.
	macshell.OnEvent(func(e macshell.Event) {
		detail, err := json.Marshal(e)
		if err != nil {
			return
		}
		go func() {
			_, _ = b.shell.Eval(fmt.Sprintf("window.dispatchEvent(new CustomEvent('basalisk:shell',{detail:%s}));0", detail))
		}()
	})

	// The NSWindow exists once the app is running; attach the native layer then.
	go func() {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if ptr := win.NativeWindow(); ptr != nil {
				macshell.Attach(ptr)
				return
			}
			select {
			case <-serveCtx.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
		slog.Error("desktop: the window never appeared; the native shell is unavailable")
	}()

	// Quit the app when the process is asked to stop or the server ends on its own.
	var serveErr error
	serveEnded := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
		case serveErr = <-served:
			close(serveEnded)
		}
		app.Quit()
	}()

	runErr := app.Run()
	stopServe()
	select {
	case <-serveEnded:
	case serveErr = <-served:
	case <-time.After(15 * time.Second):
		slog.Warn("desktop: the server did not stop in time")
	}
	return errors.Join(runErr, serveErr)
}

// desktopConfig adjusts the configuration for the desktop app, after the manifest and the
// override file were merged. A sign-in lasts 30 days instead of the server's 12 hours: the
// app is one person's, on their own machine, and it should open ready to use. An override
// file that sets identity.session_ttl to anything but the manifest default is left alone.
func desktopConfig(cfg map[string]any) {
	if !inAppBundle() {
		return
	}
	id, _ := cfg["identity"].(map[string]any)
	if id == nil {
		return
	}
	if ttl, _ := id["session_ttl"].(string); ttl == "" || ttl == "12h" {
		id["session_ttl"] = "720h"
	}
}

// inAppBundle reports whether this binary runs from a macOS app bundle
// (Basalisk.app/Contents/MacOS/basalisk). Started that way with no command, it opens the
// desktop window instead of serving headless.
func inAppBundle() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return strings.Contains(exe, ".app/Contents/MacOS/")
}

// desktopEnv gives the app its configuration file when nothing named one. An app started
// from the Finder has no environment to carry SUBSTRATE_CONFIG, so the file at
// $XDG_CONFIG_HOME/basalisk/basalisk.yaml (else ~/.config/basalisk/basalisk.yaml), the same
// place a server deployment keeps its own, is used when it exists.
func desktopEnv() {
	if os.Getenv("SUBSTRATE_CONFIG") != "" {
		return
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		dir = filepath.Join(home, ".config")
	}
	path := filepath.Join(dir, "basalisk", "basalisk.yaml")
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		_ = os.Setenv("SUBSTRATE_CONFIG", path)
	}
}

// desktopLogging sends stderr and stdout to ~/Library/Logs/Basalisk/basalisk.log when the
// app was started from its bundle with no terminal attached (the Finder, `open`), where
// both would otherwise go nowhere.
func desktopLogging() {
	if !inAppBundle() {
		return
	}
	if st, err := os.Stderr.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
		if _, isNull := devNull(st); !isNull {
			return // a real terminal: leave it
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, "Library", "Logs", "Basalisk")
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "basalisk.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_ = syscall.Dup2(int(f.Fd()), 2)
	_ = syscall.Dup2(int(f.Fd()), 1)
}

// devNull reports whether the file is /dev/null, which is also a character device.
func devNull(st os.FileInfo) (os.FileInfo, bool) {
	null, err := os.Stat(os.DevNull)
	if err != nil {
		return st, false
	}
	return st, os.SameFile(st, null)
}
