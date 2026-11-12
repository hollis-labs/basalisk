package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/basalisk/internal/appdirs"
	"github.com/hollis-labs/basalisk/internal/persistence"
)

// prepare resolves and secures the data directory, then returns the manifest config (the
// generated substrateConfigJSON) with the
// files that live in it as absolute paths. The manifest holds working-directory defaults; the
// operator's data directory (XDG data home, or BASALISK_DATA_DIR) is only known at run time.
//
// It also refuses a database that still has the pre-Substrate layout, before the database
// module opens (and so migrates) the file.
func prepare(getenv func(string) string) (string, error) {
	dirs, err := appdirs.Resolve(getenv)
	if err != nil {
		return "", err
	}
	if err = dirs.Prepare(); err != nil {
		return "", fmt.Errorf("prepare the data directory %s: %w", dirs.Data, err)
	}
	cfg := map[string]any{}
	if err = json.Unmarshal([]byte(substrateConfigJSON), &cfg); err != nil {
		return "", err
	}
	set := func(module, key, value string, nested ...string) {
		m, _ := cfg[module].(map[string]any)
		if m == nil {
			m = map[string]any{}
			cfg[module] = m
		}
		for _, n := range nested {
			child, _ := m[n].(map[string]any)
			if child == nil {
				child = map[string]any{}
				m[n] = child
			}
			m = child
		}
		m[key] = value
	}
	set("database", "path", dirs.Database(), "sqlite")
	set("identity", "key_file", dirs.SigningKey())
	set("secrets", "state_dir", dirs.Secrets())

	dbPath, err := effectiveDatabasePath(cfg, getenv("SUBSTRATE_CONFIG"))
	if err != nil {
		return "", err
	}
	if err = persistence.RefuseLegacy(dbPath); err != nil {
		return "", err
	}
	out, err := json.Marshal(cfg)
	return string(out), err
}

// effectiveDatabasePath is the SQLite file the database module will open: the config's path,
// unless the SUBSTRATE_CONFIG file replaces database.sqlite (the config module merges one
// level deep, so a replaced sqlite block without a path falls back to the module default).
func effectiveDatabasePath(cfg map[string]any, overrideFile string) (string, error) {
	path := func(c map[string]any) (string, bool) {
		db, _ := c["database"].(map[string]any)
		sq, ok := db["sqlite"].(map[string]any)
		if !ok {
			return "", false
		}
		p, _ := sq["path"].(string)
		return p, true
	}
	p, _ := path(cfg)
	if overrideFile != "" {
		b, err := os.ReadFile(overrideFile) //nolint:gosec // the operator names this file
		if err != nil {
			return "", fmt.Errorf("read %s: %w", overrideFile, err)
		}
		var override map[string]any
		if err := yaml.Unmarshal(b, &override); err != nil {
			return "", fmt.Errorf("parse %s: %w", overrideFile, err)
		}
		if op, replaced := path(override); replaced {
			p = op
			if p == "" {
				p = substrateApp + ".db" // the database module's default, relative to the working directory
			}
		}
	}
	if p == "" {
		return "", fmt.Errorf("no database path configured")
	}
	return filepath.Clean(p), nil
}
