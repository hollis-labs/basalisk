package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/hollis-labs/substrate/module"

	"github.com/hollis-labs/basalisk/internal/api"
	"github.com/hollis-labs/basalisk/internal/files"
)

// enableFiles turns the file browser on in the desktop app, and for a plain `basalisk serve`
// when BASALISK_FILES=1: like the terminal, it reads the disk as the user who runs the
// server, so it is off unless asked for.
//
// The roots come from files.json beside the database (or BASALISK_FILES_CONFIG), plus
// BASALISK_FILES_REMOTES ("host:path,..."); see docs/files.example.json. A bad config is
// logged and the browser stays off; the rest of the app still starts.
func enableFiles(ctx context.Context, b *basalisk, a *api.API) {
	if b.shell == nil && os.Getenv("BASALISK_FILES") != "1" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		slog.Warn("files: no home directory; the file browser is off", "err", err)
		return
	}
	file := os.Getenv("BASALISK_FILES_CONFIG")
	if file == "" {
		if dir := dataDir(b.app); dir != "" {
			file = filepath.Join(dir, "files.json")
		}
	}
	cfg, err := files.LoadConfig(file, os.Getenv("BASALISK_FILES_REMOTES"))
	if err == nil {
		var m *files.Manager
		if m, err = files.New(cfg, home); err == nil {
			m.SetConfigFile(file) // places added from Files are kept here
			a.EnableFiles(m)
			go func() {
				<-ctx.Done()
				m.Close()
			}()
			return
		}
	}
	slog.Warn("files: bad config; the file browser is off", "err", err)
}

// dataDir is the folder that holds the database, or "".
func dataDir(app *module.App) string {
	db, _ := app.Config["database"].(map[string]any)
	sq, _ := db["sqlite"].(map[string]any)
	if p, _ := sq["path"].(string); filepath.IsAbs(p) {
		return filepath.Dir(p)
	}
	return ""
}
