// Package assist keeps the record of what the assistant did on the owner's behalf: every
// message it sent as them, every one it settled for them. The chat stays quiet; this is
// where to look when the owner wants to know what was done in their name.
package assist

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	_ "modernc.org/sqlite" // the database driver
)

// Entry is one thing done for the owner.
type Entry struct {
	ID int64     `json:"id"`
	At time.Time `json:"at"`
	// Kind: ack, route, status_ask, reply, send, settle.
	Kind string `json:"kind"`
	// Summary is one plain line: "Acknowledged Task Ops: “Deploy finished”".
	Summary string `json:"summary"`
	// With is who it involved (an address), Ref the message it concerned or produced.
	With string `json:"with,omitempty"`
	Ref  string `json:"ref,omitempty"`
	// Asked says whether the owner was asked before it was done.
	Asked bool `json:"asked"`
	// Detail is what was sent, for the record.
	Detail map[string]any `json:"detail,omitempty"`
}

// Log is the record.
type Log struct{ db *sql.DB }

// Open opens (and creates) the log at path.
func Open(ctx context.Context, path string) (*Log, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS activity (
		id INTEGER PRIMARY KEY AUTOINCREMENT, at TEXT NOT NULL, kind TEXT NOT NULL, summary TEXT NOT NULL,
		with_whom TEXT NOT NULL DEFAULT '', ref TEXT NOT NULL DEFAULT '', asked INTEGER NOT NULL DEFAULT 0, detail TEXT NOT NULL DEFAULT '{}')`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Log{db: db}, nil
}

// Close closes the log.
func (l *Log) Close() error { return l.db.Close() }

// Add records an entry. A failure to record is returned: the caller decides whether what it
// was about to do should still be done.
func (l *Log) Add(ctx context.Context, e Entry) error {
	detail, _ := json.Marshal(e.Detail)
	if e.Detail == nil {
		detail = []byte("{}")
	}
	_, err := l.db.ExecContext(ctx, `INSERT INTO activity(at,kind,summary,with_whom,ref,asked,detail) VALUES(?,?,?,?,?,?,?)`,
		time.Now().UTC().Format(time.RFC3339), e.Kind, e.Summary, e.With, e.Ref, e.Asked, string(detail))
	return err
}

// Recent is the latest entries, newest first, before the given id (0 for the newest).
func (l *Log) Recent(ctx context.Context, before int64, limit int) ([]Entry, error) {
	if limit <= 0 || limit > 200 {
		limit = 60
	}
	q, args := `SELECT id,at,kind,summary,with_whom,ref,asked,detail FROM activity`, []any{}
	if before > 0 {
		q, args = q+` WHERE id < ?`, append(args, before)
	}
	rows, err := l.db.QueryContext(ctx, q+` ORDER BY id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var at, detail string
		if err := rows.Scan(&e.ID, &at, &e.Kind, &e.Summary, &e.With, &e.Ref, &e.Asked, &detail); err != nil {
			return nil, err
		}
		e.At, _ = time.Parse(time.RFC3339, at)
		_ = json.Unmarshal([]byte(detail), &e.Detail)
		out = append(out, e)
	}
	return out, rows.Err()
}
