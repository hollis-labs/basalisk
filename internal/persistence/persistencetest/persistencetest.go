// Package persistencetest opens a migrated Basalisk database the way Substrate's database
// module does (libs core/store, then the embedded goose migrations), for tests that need
// their own database, a reopen, or several handles on one file. It is imported by tests only.
package persistencetest

import (
	"context"
	"fmt"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/store/migrate"

	"github.com/hollis-labs/basalisk/internal/persistence"
)

// Open opens and migrates the SQLite database at path, creating it when missing.
func Open(path string) (*store.Store, error) {
	if path == "" {
		return nil, fmt.Errorf("db path is required")
	}
	ctx := context.Background()
	cfg := store.Config{Driver: store.DriverSQLite, SQLite: store.SQLiteConfig{Path: path, CreateParentDir: true}}
	st, err := store.Open(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	fsys, root := persistence.Migrations()
	if err := migrate.Ensure(ctx, cfg, st, migrate.Source{FS: fsys, Root: root}); err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return st, nil
}
