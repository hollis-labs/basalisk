package persistence_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/basalisk/internal/persistence"
	"github.com/hollis-labs/basalisk/internal/persistence/persistencetest"
)

func exec(t *testing.T, path, statements string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(statements); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationCreatesTheAppTablesAndNoAccountTables(t *testing.T) {
	st, err := persistencetest.Open(filepath.Join(t.TempDir(), "basalisk.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	has := func(table string) bool {
		var n int
		if err := st.DB().QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	for _, table := range []string{"app_preferences", "integrations", "companion_observations", "companion_artifacts", "fragment_connections", "fragment_submissions", "capture_context_bindings", "activity_identity", "activity_contexts", "activity_sessions", "activity_events", "activity_commands"} {
		if !has(table) {
			t.Errorf("missing table %s", table)
		}
	}
	// Accounts and login sessions are the identity module's now.
	for _, table := range []string{"owner", "sessions"} {
		if has(table) {
			t.Errorf("the account table %s came back", table)
		}
	}
	// The installation id is seeded by the migration and is stable across opens.
	var id string
	if err := st.DB().QueryRow("SELECT installation_id FROM activity_identity WHERE singleton=1").Scan(&id); err != nil || id == "" {
		t.Fatalf("installation id: %q %v", id, err)
	}
}

func TestRefuseLegacy(t *testing.T) {
	dir := t.TempDir()

	if err := persistence.RefuseLegacy(filepath.Join(dir, "missing.db")); err != nil {
		t.Errorf("a missing file is a fresh install: %v", err)
	}
	empty := filepath.Join(dir, "empty.db")
	exec(t, empty, `CREATE TABLE unrelated(x)`)
	if err := persistence.RefuseLegacy(empty); err != nil {
		t.Errorf("a database with neither marker: %v", err)
	}

	owner := filepath.Join(dir, "owner.db")
	exec(t, owner, `CREATE TABLE owner(id INTEGER PRIMARY KEY, username TEXT)`)
	versioned := filepath.Join(dir, "versioned.db")
	exec(t, versioned, `CREATE TABLE integrations(id TEXT); PRAGMA user_version=8`)
	for name, path := range map[string]string{"owner table": owner, "user_version": versioned} {
		err := persistence.RefuseLegacy(path)
		if !errors.Is(err, persistence.ErrLegacyLayout) {
			t.Errorf("%s: err = %v, want ErrLegacyLayout", name, err)
		}
	}

	// The database this version creates passes its own guard.
	fresh := filepath.Join(dir, "fresh.db")
	st, err := persistencetest.Open(fresh)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if err := persistence.RefuseLegacy(fresh); err != nil {
		t.Errorf("the new layout refused: %v", err)
	}
}
