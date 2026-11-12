// Package persistence owns Basalisk's durable schema: one goose migration, embedded
// here and applied by Substrate's database module. Basalisk is SQLite only.
package persistence

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"

	_ "modernc.org/sqlite" // registers the "sqlite" driver the guard uses
)

//go:embed migrations
var migrations embed.FS

// Migrations is the embedded migration tree: Root "migrations" holds one directory per
// dialect (here only sqlite).
func Migrations() (fsys embed.FS, root string) { return migrations, "migrations" }

// ErrLegacyLayout means the database file holds the layout used before Basalisk moved onto
// Substrate. There is no migration: the operator moves the old data directory aside.
var ErrLegacyLayout = errors.New("the database uses the pre-Substrate Basalisk layout")

// RefuseLegacy returns ErrLegacyLayout (wrapped with what to do) when the SQLite file at
// path already exists with the old layout: an owner table, or a non-zero PRAGMA
// user_version (the old code versioned its schema that way; the new one never does). A
// missing or empty file is fine. Run it before the database module opens the file, because
// opening migrates.
func RefuseLegacy(path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	// Only reads: the guard never changes the file it is judging. (A read-only open would
	// fail on a WAL database whose -shm file is gone.)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var version, owner int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='owner'").Scan(&owner); err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if owner > 0 || version > 0 {
		return fmt.Errorf("%w (%s): move the old data directory aside and start again; there is no migration. See docs/conversion-notes.md",
			ErrLegacyLayout, path)
	}
	return nil
}
