// Package account owns Basalisk's single-owner identity and integration metadata.
package account

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/hollis-labs/basalisk/internal/activity"
	"github.com/hollis-labs/basalisk/internal/assurance"
	"github.com/hollis-labs/libs/security/pairing"
	"github.com/hollis-labs/libs/security/secrets"
	_ "modernc.org/sqlite"
)

type Store struct {
	assurance        *assurance.Service
	assuranceTachyon *assurance.TachyonClient
	db               *sql.DB
	secrets          *secrets.Resolver
	activity         *activity.Store
	pairing          *pairing.Store
	deliveryMu       sync.RWMutex
	delivery         deliveryActivity
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "basalisk.db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version > 8 {
		db.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("database schema %d is newer than this Basalisk supports", version)
	}
	_, err = db.Exec(`PRAGMA busy_timeout=5000;
 PRAGMA journal_mode=WAL;
 PRAGMA synchronous=FULL;
 PRAGMA foreign_keys=ON;
 CREATE TABLE IF NOT EXISTS owner (
 id INTEGER PRIMARY KEY CHECK(id=1), username TEXT NOT NULL, display_name TEXT NOT NULL,
 email TEXT NOT NULL DEFAULT '', password_hash BLOB NOT NULL,
 theme TEXT NOT NULL DEFAULT 'dark', timezone TEXT NOT NULL DEFAULT 'UTC', revision INTEGER NOT NULL DEFAULT 1);
 CREATE TABLE IF NOT EXISTS sessions (hash TEXT PRIMARY KEY, expires INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS integrations (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, provider TEXT NOT NULL, base_url TEXT NOT NULL,
 secret_ref TEXT NOT NULL, updated_at TEXT NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	activities, err := activity.Init(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	devices, err := pairing.Open(db, pairing.Config{Audience: "basalisk", Scopes: []string{scopeContext, scopeObserve, scopeCapture}})
	if err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS companion_observations (
 device_id TEXT NOT NULL,generation TEXT NOT NULL,sequence INTEGER NOT NULL,payload_hash TEXT NOT NULL,
 occurred_at TEXT NOT NULL,received_at TEXT NOT NULL,kind TEXT NOT NULL,bundle_id TEXT NOT NULL,app_name TEXT NOT NULL,
 session_id TEXT NOT NULL,context_id TEXT NOT NULL,context_as_of TEXT NOT NULL,
 PRIMARY KEY(device_id,generation,sequence));
 CREATE INDEX IF NOT EXISTS companion_history ON companion_observations(occurred_at);
 CREATE TABLE IF NOT EXISTS companion_artifacts (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT, device_id TEXT NOT NULL,capture_id TEXT NOT NULL,
 session_id TEXT NOT NULL,payload_hash TEXT NOT NULL,payload TEXT NOT NULL,received_at TEXT NOT NULL,
 UNIQUE(device_id,capture_id));
 CREATE INDEX IF NOT EXISTS companion_artifacts_session ON companion_artifacts(session_id,sequence);
	 CREATE TABLE IF NOT EXISTS fragment_connections (
	 device_id TEXT PRIMARY KEY, integration_id TEXT NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
	 updated_at TEXT NOT NULL);
	 CREATE TABLE IF NOT EXISTS fragment_submissions (
	 sequence INTEGER PRIMARY KEY AUTOINCREMENT, device_id TEXT NOT NULL, capture_id TEXT NOT NULL,
	 integration_id TEXT NOT NULL, payload_hash TEXT NOT NULL, payload TEXT NOT NULL,
	 state TEXT NOT NULL, code TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
	 UNIQUE(device_id,capture_id));
	 `); err != nil {
		db.Close()
		return nil, err
	}
	if version < 7 {
		_, err = db.Exec(`BEGIN;
		ALTER TABLE fragment_submissions ADD COLUMN manifest TEXT NOT NULL DEFAULT '';
		ALTER TABLE fragment_submissions ADD COLUMN delivery_origin TEXT NOT NULL DEFAULT '';
		ALTER TABLE fragment_submissions ADD COLUMN delivery_issuer TEXT NOT NULL DEFAULT '';
		ALTER TABLE fragment_submissions ADD COLUMN delivery_subject TEXT NOT NULL DEFAULT '';
		ALTER TABLE fragment_submissions ADD COLUMN upstream_capture_id TEXT NOT NULL DEFAULT '';
		ALTER TABLE fragment_submissions ADD COLUMN receipt TEXT NOT NULL DEFAULT '';
		ALTER TABLE fragment_submissions ADD COLUMN fragment_id TEXT NOT NULL DEFAULT '';
		ALTER TABLE fragment_submissions ADD COLUMN fragment_revision_id TEXT NOT NULL DEFAULT '';
		ALTER TABLE fragment_submissions ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE fragment_submissions ADD COLUMN next_attempt INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE fragment_submissions ADD COLUMN lease_token TEXT NOT NULL DEFAULT '';
		ALTER TABLE fragment_submissions ADD COLUMN lease_until INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE fragment_submissions ADD COLUMN may_have_sent INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE companion_artifacts ADD COLUMN source TEXT NOT NULL DEFAULT 'frag.fe-response (device-reported)';
		CREATE INDEX fragment_submissions_due ON fragment_submissions(state,next_attempt,lease_until);
		PRAGMA user_version=7;
		COMMIT;`)
		if err != nil {
			db.Close()
			return nil, err
		}
	}
	if version < 8 {
		_, err = db.Exec(`BEGIN;
		CREATE TABLE capture_context_bindings (
		 id TEXT PRIMARY KEY, device_id TEXT NOT NULL, capture_id TEXT NOT NULL,
		 origin TEXT NOT NULL, draft_at TEXT NOT NULL, payload TEXT NOT NULL,
		 UNIQUE(device_id,capture_id));
		PRAGMA user_version=8;
		COMMIT;`)
		if err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db, activity: activities, pairing: devices, secrets: secrets.New(secrets.WithStateDir(filepath.Join(dir, "secrets")))}, nil
}

func (s *Store) Close() error { return s.db.Close() }

type Profile struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	Theme       string `json:"theme"`
	Timezone    string `json:"timezone"`
	Revision    int    `json:"revision"`
}

func (s *Store) profile() (Profile, error) {
	var p Profile
	err := s.db.QueryRow("SELECT username,display_name,email,theme,timezone,revision FROM owner WHERE id=1").Scan(&p.Username, &p.DisplayName, &p.Email, &p.Theme, &p.Timezone, &p.Revision)
	return p, err
}
