package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/substrate/boot"
	"github.com/hollis-labs/substrate/module"

	"github.com/hollis-labs/basalisk/internal/appdirs"
	"github.com/hollis-labs/basalisk/internal/persistence"
)

// bootOptions are the app's two boot hooks.
//
// After the config module has applied the SUBSTRATE_CONFIG override, the data directory
// (XDG data home, or BASALISK_DATA_DIR) is resolved and secured, and every file path that
// is relative is joined onto it (see anchorPath). The manifest holds relative defaults; an
// absolute path from the override is the operator's choice and stays.
//
// Immediately before the database module starts (it migrates as soon as it opens the file),
// a pre-Substrate database is refused. The path is read from the config the module will
// read (already joined onto the data directory), so the guard needs no copy of the merge rule.
func bootOptions(getenv func(string) string) boot.Options {
	return boot.Options{
		AfterConfig: func(_ context.Context, app *module.App) error {
			dirs, err := appdirs.Resolve(getenv)
			if err != nil {
				return err
			}
			if err = dirs.Prepare(); err != nil {
				return fmt.Errorf("prepare the data directory %s: %w", dirs.Data, err)
			}
			anchor := func(def, modName, key string, nested ...string) error {
				m, _ := app.Config[modName].(map[string]any)
				if m == nil {
					m = map[string]any{}
					app.Config[modName] = m
				}
				for _, n := range nested {
					child, _ := m[n].(map[string]any)
					if child == nil {
						child = map[string]any{}
						m[n] = child
					}
					m = child
				}
				p, _ := m[key].(string)
				got, err := anchorPath(dirs.Data, def, p)
				if err != nil {
					return fmt.Errorf("%s.%s: %w", modName, key, err)
				}
				m[key] = got
				return nil
			}
			for _, a := range []struct {
				def, mod, key string
				nested        []string
			}{
				{dirs.Database(), "database", "path", []string{"sqlite"}},
				{dirs.SigningKey(), "identity", "key_file", nil},
				{dirs.Secrets(), "secrets", "state_dir", nil},
			} {
				if err := anchor(a.def, a.mod, a.key, a.nested...); err != nil {
					return err
				}
			}
			return prepareFiles(dirs, app)
		},
		Before: map[string]boot.Hook{
			"database": func(_ context.Context, app *module.App) error {
				db, _ := app.Config["database"].(map[string]any)
				sq, _ := db["sqlite"].(map[string]any)
				p, _ := sq["path"].(string)
				if p == "" {
					p = app.Name + ".db" // the database module's default, relative to the working directory
				}
				return persistence.RefuseLegacy(filepath.Clean(p))
			},
		},
	}
}

// anchorPath places a configured path under the data directory. An empty value is def (the
// default name under data); an absolute path is the operator's explicit choice and is kept
// as is; a relative path, whether the manifest default or from the override file, is joined
// onto data. A relative path that would climb out of data through ".." is refused: it is
// not an explicit absolute choice, and silently writing the database or key outside the
// private data directory would defeat the directory's 0700 protection.
func anchorPath(data, def, p string) (string, error) {
	switch {
	case p == "":
		return def, nil
	case filepath.IsAbs(p):
		return p, nil
	}
	joined := filepath.Join(data, p)
	if rel, err := filepath.Rel(data, joined); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("relative path %q leaves the data directory %s; use an absolute path to put it elsewhere", p, data)
	}
	return joined, nil
}

// prepareFiles creates what the final configuration will use, and nothing else. The database
// file is created (or tightened) 0600 only for the sqlite driver, and only at the configured
// path, so an override that points elsewhere leaves no empty default database behind. A file
// or directory path inside the data directory gets its missing parent directories (0700); a
// path outside it gets none.
func prepareFiles(dirs appdirs.Dirs, app *module.App) error {
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	db, _ := app.Config["database"].(map[string]any)
	if d := str(db, "driver"); d == "" || d == "sqlite" {
		sq, _ := db["sqlite"].(map[string]any)
		p := str(sq, "path")
		if err := dirs.EnsureParent(p); err != nil {
			return fmt.Errorf("database.path: %w", err)
		}
		if err := appdirs.PrivateFile(p); err != nil {
			return fmt.Errorf("database.path: %w", err)
		}
	}
	id, _ := app.Config["identity"].(map[string]any)
	if err := dirs.EnsureParent(str(id, "key_file")); err != nil {
		return fmt.Errorf("identity.key_file: %w", err)
	}
	sec, _ := app.Config["secrets"].(map[string]any)
	if err := dirs.EnsureParent(str(sec, "state_dir")); err != nil {
		return fmt.Errorf("secrets.state_dir: %w", err)
	}
	return nil
}
