package account

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestUpgradeV6RetainsQueuedNotesAndLegacyAttribution(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "basalisk.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE fragment_submissions (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,device_id TEXT NOT NULL,capture_id TEXT NOT NULL,
 integration_id TEXT NOT NULL,payload_hash TEXT NOT NULL,payload TEXT NOT NULL,
 state TEXT NOT NULL,code TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,
 UNIQUE(device_id,capture_id));
 CREATE TABLE companion_artifacts (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,device_id TEXT NOT NULL,capture_id TEXT NOT NULL,
 session_id TEXT NOT NULL,payload_hash TEXT NOT NULL,payload TEXT NOT NULL,received_at TEXT NOT NULL,
 UNIQUE(device_id,capture_id));
 INSERT INTO fragment_submissions VALUES(1,'mac','old-capture','fe','original-hash','{"content":"keep this note"}','queued','delivery_unavailable','2026-10-18T00:00:00Z','2026-10-18T00:00:00Z');
 INSERT INTO companion_artifacts VALUES(1,'mac','old-artifact','session','hash','{}','2026-10-18T00:00:00Z');
 PRAGMA user_version=6;`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		s, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		status, err := readSubmission(s.db.QueryRow("SELECT " + submissionColumns + " FROM fragment_submissions WHERE sequence=1"))
		if err != nil {
			t.Fatal(err)
		}
		if status.State != "queued" || status.MayHaveSent || status.Delivering || status.FragmentID != "" {
			t.Fatal("migration changed pending note", status)
		}
		var payload, hash, source string
		if err = s.db.QueryRow("SELECT payload,payload_hash FROM fragment_submissions WHERE sequence=1").Scan(&payload, &hash); err != nil {
			t.Fatal(err)
		}
		if payload != `{"content":"keep this note"}` || hash != "original-hash" {
			t.Fatal("migration changed original operation")
		}
		if err = s.db.QueryRow("SELECT source FROM companion_artifacts WHERE sequence=1").Scan(&source); err != nil {
			t.Fatal(err)
		}
		if source != "frag.fe-response (device-reported)" {
			t.Fatal("migration falsely attested legacy provenance")
		}
		s.Close()
	}
}
