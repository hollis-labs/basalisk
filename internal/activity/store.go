// Package activity owns human work sessions, independently of login sessions.
// Its journal is local domain state, not a portfolio transport envelope.
package activity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
)

var (
	ErrInvalid  = errors.New("invalid activity command")
	ErrConflict = errors.New("activity changed or command conflicts")
	ErrNotFound = errors.New("activity session not found")
)

type Store struct{ db *sql.DB }

type Identity struct {
	InstallationID string `json:"installationId"`
	UserID         string `json:"userId"`
}

// Context contains user-declared labels, not verified external registry IDs.
type Context struct {
	ID         string `json:"id"`
	Project    string `json:"project"`
	Workstream string `json:"workstream"`
	Source     string `json:"source"`
	RecordedAt string `json:"recordedAt"`
}

type Session struct {
	Sequence  int64   `json:"sequence"`
	ID        string  `json:"id"`
	UserID    string  `json:"userId"`
	Title     string  `json:"title"`
	Status    string  `json:"status"`
	Revision  int64   `json:"revision"`
	Context   Context `json:"context"`
	StartedAt string  `json:"startedAt"`
	UpdatedAt string  `json:"updatedAt"`
	EndedAt   string  `json:"endedAt,omitempty"`
}

type Event struct {
	Sequence   int64   `json:"sequence"`
	ID         string  `json:"id"`
	SessionID  string  `json:"activitySessionId"`
	ActorID    string  `json:"actorId"`
	Kind       string  `json:"kind"`
	Revision   int64   `json:"revision"`
	Context    Context `json:"context"`
	Source     string  `json:"source"`
	OccurredAt string  `json:"occurredAt"`
	ReceivedAt string  `json:"receivedAt"`
}

type State struct {
	Identity Identity `json:"identity"`
	Current  *Session `json:"current"`
}

type Page[T any] struct {
	Items      []T   `json:"items"`
	NextBefore int64 `json:"nextBefore,omitempty"`
}

type Command struct {
	RequestID  string `json:"requestId"`
	Action     string `json:"action"`
	SessionID  string `json:"sessionId,omitempty"`
	Revision   int64  `json:"revision,omitempty"`
	Title      string `json:"title,omitempty"`
	Project    string `json:"project,omitempty"`
	Workstream string `json:"workstream,omitempty"`
}

func id(prefix string) string { return prefix + rand.Text() }

// Init upgrades the existing account database atomically. The reserved owner ID
// is stable even before first-run setup and never depends on a username or host.
func Init(db *sql.DB) (*Store, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS activity_identity (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1), installation_id TEXT NOT NULL, user_id TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS activity_contexts (
 id TEXT PRIMARY KEY, project TEXT NOT NULL, workstream TEXT NOT NULL, source TEXT NOT NULL, recorded_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS activity_sessions (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, user_id TEXT NOT NULL,
 title TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('active','paused','ended')), revision INTEGER NOT NULL,
 context_id TEXT NOT NULL REFERENCES activity_contexts(id), started_at TEXT NOT NULL, updated_at TEXT NOT NULL, ended_at TEXT NOT NULL);
CREATE UNIQUE INDEX IF NOT EXISTS activity_one_open ON activity_sessions(user_id) WHERE status != 'ended';
CREATE TABLE IF NOT EXISTS activity_events (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, session_id TEXT NOT NULL REFERENCES activity_sessions(id),
 actor_id TEXT NOT NULL, kind TEXT NOT NULL, revision INTEGER NOT NULL, context_id TEXT NOT NULL REFERENCES activity_contexts(id),
 source TEXT NOT NULL, occurred_at TEXT NOT NULL, received_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS activity_events_session ON activity_events(session_id,sequence);
CREATE TABLE IF NOT EXISTS activity_commands (
 request_id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, result TEXT NOT NULL);`)
	if err == nil {
		_, err = tx.Exec("INSERT OR IGNORE INTO activity_identity VALUES(1,?,?)", id("bas_"), id("usr_"))
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

type scanner interface{ Scan(...any) error }

const sessionSelect = `SELECT s.sequence,s.id,s.user_id,s.title,s.status,s.revision,
 c.id,c.project,c.workstream,c.source,c.recorded_at,s.started_at,s.updated_at,s.ended_at
 FROM activity_sessions s JOIN activity_contexts c ON c.id=s.context_id `

func readSession(row scanner) (Session, error) {
	var s Session
	err := row.Scan(&s.Sequence, &s.ID, &s.UserID, &s.Title, &s.Status, &s.Revision,
		&s.Context.ID, &s.Context.Project, &s.Context.Workstream, &s.Context.Source, &s.Context.RecordedAt,
		&s.StartedAt, &s.UpdatedAt, &s.EndedAt)
	return s, err
}

func (s *Store) State(ctx context.Context) (State, error) {
	var state State
	err := s.db.QueryRowContext(ctx, "SELECT installation_id,user_id FROM activity_identity WHERE singleton=1").Scan(&state.Identity.InstallationID, &state.Identity.UserID)
	if err != nil {
		return state, err
	}
	current, err := readSession(s.db.QueryRowContext(ctx, sessionSelect+"WHERE s.status != 'ended'"))
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err == nil {
		state.Current = &current
	}
	return state, err
}

func (s *Store) Sessions(ctx context.Context, before int64) (Page[Session], error) {
	p := Page[Session]{Items: []Session{}}
	rows, err := s.db.QueryContext(ctx, sessionSelect+"WHERE (?=0 OR s.sequence<?) ORDER BY s.sequence DESC LIMIT 21", before, before)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := readSession(rows)
		if err != nil {
			return p, err
		}
		if len(p.Items) == 20 {
			p.NextBefore = p.Items[19].Sequence
			break
		}
		p.Items = append(p.Items, item)
	}
	return p, rows.Err()
}

func (s *Store) Events(ctx context.Context, sessionID string, before int64) (Page[Event], error) {
	p := Page[Event]{Items: []Event{}}
	var exists int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM activity_sessions WHERE id=?", sessionID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.sequence,e.id,e.session_id,e.actor_id,e.kind,e.revision,
 c.id,c.project,c.workstream,c.source,c.recorded_at,e.source,e.occurred_at,e.received_at
 FROM activity_events e JOIN activity_contexts c ON c.id=e.context_id
 WHERE e.session_id=? AND (?=0 OR e.sequence<?) ORDER BY e.sequence DESC LIMIT 51`, sessionID, before, before)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Event
		if err = rows.Scan(&e.Sequence, &e.ID, &e.SessionID, &e.ActorID, &e.Kind, &e.Revision,
			&e.Context.ID, &e.Context.Project, &e.Context.Workstream, &e.Context.Source, &e.Context.RecordedAt,
			&e.Source, &e.OccurredAt, &e.ReceivedAt); err != nil {
			return p, err
		}
		if len(p.Items) == 50 {
			p.NextBefore = p.Items[49].Sequence
			break
		}
		p.Items = append(p.Items, e)
	}
	return p, rows.Err()
}

func validText(value string, max int) bool {
	return len(value) <= max && !strings.ContainsFunc(value, unicode.IsControl)
}

func (c *Command) validate() bool {
	c.Title = strings.TrimSpace(c.Title)
	c.Project = strings.TrimSpace(c.Project)
	c.Workstream = strings.TrimSpace(c.Workstream)
	if len(c.RequestID) < 16 || !validText(c.RequestID, 128) || !validText(c.Title, 120) || !validText(c.Project, 200) || !validText(c.Workstream, 200) {
		return false
	}
	switch c.Action {
	case "start":
		return c.SessionID == "" && c.Revision == 0 && c.Title != ""
	case "context":
		return c.SessionID != "" && len(c.SessionID) <= 100 && c.Revision > 0 && c.Title == ""
	case "pause", "resume", "end":
		return c.SessionID != "" && len(c.SessionID) <= 100 && c.Revision > 0 && c.Title == "" && c.Project == "" && c.Workstream == ""
	default:
		return false
	}
}

// Apply commits a session change, its context, journal entry, and retry receipt
// together. Retrying a request returns the original result, even after later
// edits; reusing its ID for another command is a conflict.
func (s *Store) Apply(ctx context.Context, c Command) (Session, error) {
	var result Session
	if !c.validate() {
		return result, ErrInvalid
	}
	raw, _ := json.Marshal(c)
	hash := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(hash[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var previousHash, previousResult string
	err = tx.QueryRowContext(ctx, "SELECT fingerprint,result FROM activity_commands WHERE request_id=?", c.RequestID).Scan(&previousHash, &previousResult)
	if err == nil {
		if previousHash != fingerprint {
			return result, ErrConflict
		}
		err = json.Unmarshal([]byte(previousResult), &result)
		return result, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	kind := ""
	if c.Action == "start" {
		var open int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM activity_sessions WHERE status!='ended'").Scan(&open); err != nil {
			return result, err
		}
		if open != 0 {
			return result, ErrConflict
		}
		if err = tx.QueryRowContext(ctx, "SELECT user_id FROM activity_identity WHERE singleton=1").Scan(&result.UserID); err != nil {
			return result, err
		}
		result.ID = id("act_")
		result.Title = c.Title
		result.Status = "active"
		result.Revision = 1
		result.StartedAt = now
		kind = "started"
	} else {
		result, err = readSession(tx.QueryRowContext(ctx, sessionSelect+"WHERE s.id=?", c.SessionID))
		if errors.Is(err, sql.ErrNoRows) {
			return result, ErrNotFound
		}
		if err != nil {
			return result, err
		}
		if result.Revision != c.Revision || result.Status == "ended" {
			return result, ErrConflict
		}
		switch c.Action {
		case "pause":
			if result.Status != "active" {
				return result, ErrConflict
			}
			result.Status = "paused"
			kind = "paused"
		case "resume":
			if result.Status != "paused" {
				return result, ErrConflict
			}
			result.Status = "active"
			kind = "resumed"
		case "end":
			result.Status = "ended"
			result.EndedAt = now
			kind = "ended"
		case "context":
			kind = "context_changed"
		}
		result.Revision++
	}
	result.UpdatedAt = now
	if c.Action == "start" || c.Action == "context" {
		result.Context = Context{ID: id("ctx_"), Project: c.Project, Workstream: c.Workstream, Source: "user", RecordedAt: now}
		_, err = tx.ExecContext(ctx, "INSERT INTO activity_contexts VALUES(?,?,?,?,?)", result.Context.ID, result.Context.Project, result.Context.Workstream, result.Context.Source, now)
		if err != nil {
			return result, err
		}
	}
	if c.Action == "start" {
		var inserted sql.Result
		inserted, err = tx.ExecContext(ctx, `INSERT INTO activity_sessions(id,user_id,title,status,revision,context_id,started_at,updated_at,ended_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			result.ID, result.UserID, result.Title, result.Status, result.Revision, result.Context.ID, result.StartedAt, now, "")
		if err == nil {
			result.Sequence, err = inserted.LastInsertId()
		}
	} else {
		_, err = tx.ExecContext(ctx, "UPDATE activity_sessions SET status=?,revision=?,context_id=?,updated_at=?,ended_at=? WHERE id=?", result.Status, result.Revision, result.Context.ID, now, result.EndedAt, result.ID)
	}
	if err != nil {
		return result, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO activity_events(id,session_id,actor_id,kind,revision,context_id,source,occurred_at,received_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		id("evt_"), result.ID, result.UserID, kind, result.Revision, result.Context.ID, "basalisk.web", now, now)
	if err != nil {
		return result, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO activity_commands VALUES(?,?,?)", c.RequestID, fingerprint, string(encoded))
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
