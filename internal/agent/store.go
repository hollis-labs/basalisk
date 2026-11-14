package agent

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmtypes"

	_ "modernc.org/sqlite" // the driver, as the rest of the app uses it
)

// Store keeps the agent's conversations in a SQLite file of its own, beside the app's
// database. Its own file because the app's schema is one migration edited in place, which
// an existing database would never see change; here the schema moves forward by number.
type Store struct{ db *sql.DB }

// Each entry runs once, in order; the database remembers how many it has run
// (PRAGMA user_version). Add to the end, never edit one that has shipped.
var migrations = []string{
	`CREATE TABLE threads (
		id TEXT PRIMARY KEY,
		owner TEXT NOT NULL,
		kind TEXT NOT NULL DEFAULT 'main',      -- 'main': the running thread. 'task': one agent's work on a task.
		parent_id TEXT NOT NULL DEFAULT '',     -- the thread a task thread was started from
		title TEXT NOT NULL DEFAULT '',
		archived_at TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	CREATE INDEX threads_owner ON threads(owner, kind, archived_at);
	CREATE TABLE messages (
		id TEXT PRIMARY KEY,
		thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
		seq INTEGER NOT NULL,
		role TEXT NOT NULL,                     -- 'user' or 'assistant', as the model sees it
		content TEXT NOT NULL,                  -- JSON: the message's content blocks
		meta TEXT NOT NULL DEFAULT '{}',        -- JSON: what the interface shows and the model does not (the window context)
		run_id TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		UNIQUE(thread_id, seq)
	);
	CREATE TABLE runs (
		id TEXT PRIMARY KEY,
		thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
		owner TEXT NOT NULL,
		status TEXT NOT NULL,                   -- running, finished, cancelled, error
		model TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT '',        -- how it ended, or the error
		input_tokens INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		started_at TEXT NOT NULL,
		finished_at TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX runs_thread ON runs(thread_id, started_at);
	-- A standing answer to "may the agent use this tool": the "always" of once/session/always.
	CREATE TABLE grants (
		owner TEXT NOT NULL,
		tool TEXT NOT NULL,
		created_at TEXT NOT NULL,
		PRIMARY KEY(owner, tool)
	);`,
	// The router's rule that took a run's message ('' when the model answered it).
	`ALTER TABLE runs ADD COLUMN route TEXT NOT NULL DEFAULT ''`,
	// How large the last model request of a run was (what the next one starts from), and
	// when each reflex last fired.
	`ALTER TABLE runs ADD COLUMN prefix_tokens INTEGER NOT NULL DEFAULT 0;
	CREATE TABLE reflexes (
		name TEXT PRIMARY KEY,
		fired_count INTEGER NOT NULL DEFAULT 0,
		last_fired_at TEXT NOT NULL DEFAULT ''
	);`,
	// A task thread: how it stands, its last report, when the main thread was told of it,
	// and who started it (agent, owner, or trigger:<name>).
	`ALTER TABLE threads ADD COLUMN state TEXT NOT NULL DEFAULT '';
	ALTER TABLE threads ADD COLUMN result TEXT NOT NULL DEFAULT '';
	ALTER TABLE threads ADD COLUMN reported_at TEXT NOT NULL DEFAULT '';
	ALTER TABLE threads ADD COLUMN origin TEXT NOT NULL DEFAULT '';`,
	// Compaction: the summary the model is sent in place of a thread's older messages,
	// and the last message it covers.
	`ALTER TABLE threads ADD COLUMN summary TEXT NOT NULL DEFAULT '';
	ALTER TABLE threads ADD COLUMN summary_upto INTEGER NOT NULL DEFAULT 0;`,
}

// OpenStore opens (and creates or upgrades) the file at path.
func OpenStore(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// One writer at a time: the agent's writes are small and rare.
	db.SetMaxOpenConns(1)
	var have int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&have); err != nil {
		_ = db.Close()
		return nil, err
	}
	if have > len(migrations) {
		_ = db.Close()
		return nil, fmt.Errorf("agent: %s was written by a newer Basalisk (schema %d, this build knows %d)", path, have, len(migrations))
	}
	for i := have; i < len(migrations); i++ {
		tx, err := db.BeginTx(ctx, nil)
		if err == nil {
			if _, err = tx.ExecContext(ctx, migrations[i]); err == nil {
				_, err = tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1))
			}
			if err == nil {
				err = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
		}
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("agent: schema step %d: %w", i+1, err)
		}
	}
	// A run that was in flight when the app last stopped did not finish.
	_, _ = db.ExecContext(ctx, `UPDATE runs SET status='error', reason='Basalisk stopped while this was running.', finished_at=? WHERE status='running'`, now())
	_, _ = db.ExecContext(ctx, `UPDATE threads SET state='stopped', result='Basalisk stopped while this was running.', reported_at='' WHERE kind='task' AND state='running'`)
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func newID(prefix string) string { return prefix + "_" + rand.Text()[:20] }

// Message is one stored message. Blocks is what the model is sent; Meta is the interface's.
type Message struct {
	ID        string                  `json:"id"`
	Seq       int                     `json:"seq"`
	Role      string                  `json:"role"`
	Blocks    []llmtypes.ContentBlock `json:"blocks"`
	Meta      json.RawMessage         `json:"meta"`
	RunID     string                  `json:"run_id,omitempty"`
	CreatedAt string                  `json:"created_at"`
}

// MainThread is the owner's running thread, created on first use.
func (s *Store) MainThread(ctx context.Context, owner string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM threads WHERE owner=? AND kind='main' AND archived_at='' ORDER BY created_at DESC LIMIT 1`, owner).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id = newID("thr")
	t := now()
	_, err = s.db.ExecContext(ctx, `INSERT INTO threads(id, owner, kind, created_at, updated_at) VALUES(?,?,'main',?,?)`, id, owner, t, t)
	return id, err
}

// ArchiveMain puts the running thread away, so the next message starts a new one. Nothing
// is deleted.
func (s *Store) ArchiveMain(ctx context.Context, owner string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE threads SET archived_at=? WHERE owner=? AND kind='main' AND archived_at=''`, now(), owner)
	return err
}

// Append adds a message to the end of a thread.
func (s *Store) Append(ctx context.Context, thread, runID, role string, blocks []llmtypes.ContentBlock, meta json.RawMessage) (Message, error) {
	content, err := json.Marshal(blocks)
	if err != nil {
		return Message{}, err
	}
	if len(meta) == 0 {
		meta = json.RawMessage("{}")
	}
	m := Message{ID: newID("msg"), Role: role, Blocks: blocks, Meta: meta, RunID: runID, CreatedAt: now()}
	err = s.db.QueryRowContext(ctx, `INSERT INTO messages(id, thread_id, seq, role, content, meta, run_id, created_at)
		VALUES(?,?,(SELECT COALESCE(MAX(seq),0)+1 FROM messages WHERE thread_id=?),?,?,?,?,?) RETURNING seq`,
		m.ID, thread, thread, role, string(content), string(meta), runID, m.CreatedAt).Scan(&m.Seq)
	if err != nil {
		return Message{}, err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE threads SET updated_at=? WHERE id=?`, m.CreatedAt, thread)
	return m, nil
}

// Messages are a thread's messages, oldest first.
func (s *Store) Messages(ctx context.Context, thread string) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, seq, role, content, meta, run_id, created_at FROM messages WHERE thread_id=? ORDER BY seq`, thread)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var content, meta string
		if err := rows.Scan(&m.ID, &m.Seq, &m.Role, &content, &meta, &m.RunID, &m.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(content), &m.Blocks); err != nil {
			return nil, fmt.Errorf("agent: message %s is not readable: %w", m.ID, err)
		}
		m.Meta = json.RawMessage(meta)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) startRun(ctx context.Context, id, thread, owner, model string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO runs(id, thread_id, owner, status, model, started_at) VALUES(?,?,?,'running',?,?)`, id, thread, owner, model, now())
	return err
}

func (s *Store) setRoute(ctx context.Context, id, rule string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE runs SET route=? WHERE id=?`, rule, id)
}

func (s *Store) setPrefix(ctx context.Context, id string, tokens int) {
	_, _ = s.db.ExecContext(ctx, `UPDATE runs SET prefix_tokens=? WHERE id=?`, tokens, id)
}

// lastPrefix is how large the thread's last model request was (0 when unknown).
func (s *Store) lastPrefix(ctx context.Context, thread string) int {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT prefix_tokens FROM runs WHERE thread_id=? AND prefix_tokens>0 ORDER BY started_at DESC LIMIT 1`, thread).Scan(&n)
	return n
}

func (s *Store) reflexFired(ctx context.Context, name string) (count int64, last string) {
	_ = s.db.QueryRowContext(ctx, `SELECT fired_count, last_fired_at FROM reflexes WHERE name=?`, name).Scan(&count, &last)
	return count, last
}

func (s *Store) bumpReflex(ctx context.Context, name string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO reflexes(name, fired_count, last_fired_at) VALUES(?,1,?)
		ON CONFLICT(name) DO UPDATE SET fired_count=fired_count+1, last_fired_at=excluded.last_fired_at`, name, at.UTC().Format(time.RFC3339))
	return err
}

func (s *Store) finishRun(ctx context.Context, id, status, reason string, in, out int) {
	_, _ = s.db.ExecContext(ctx, `UPDATE runs SET status=?, reason=?, input_tokens=?, output_tokens=?, finished_at=? WHERE id=?`, status, reason, in, out, now(), id)
}

// RunOwner says whose run this is ("" when there is no such run).
func (s *Store) RunOwner(ctx context.Context, id string) string {
	var owner string
	_ = s.db.QueryRowContext(ctx, `SELECT owner FROM runs WHERE id=?`, id).Scan(&owner)
	return owner
}

func (s *Store) granted(ctx context.Context, owner, tool string) bool {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM grants WHERE owner=? AND tool=?`, owner, tool).Scan(&n)
	return n > 0
}

func (s *Store) grant(ctx context.Context, owner, tool string) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO grants(owner, tool, created_at) VALUES(?,?,?)`, owner, tool, now())
	return err
}

// Grants are the tools the owner has allowed for good.
func (s *Store) Grants(ctx context.Context, owner string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tool FROM grants WHERE owner=? ORDER BY tool`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Revoke takes a standing permission back.
func (s *Store) Revoke(ctx context.Context, owner, tool string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM grants WHERE owner=? AND tool=?`, owner, tool)
	return err
}

// Task states.
const (
	TaskRunning = "running"
	TaskDone    = "done"
	TaskFailed  = "failed"
	TaskStopped = "stopped"
)

// Task is a task thread: one agent's work on one thing, beside the running thread.
type Task struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	State     string `json:"state"`
	Result    string `json:"result"`
	Origin    string `json:"origin"`
	ParentID  string `json:"parent_id,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	// Reported says the running thread has been told how it ended.
	Reported bool `json:"reported"`
}

func (s *Store) newTask(ctx context.Context, owner, parent, title, origin string) (string, error) {
	id := newID("thr")
	t := now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO threads(id, owner, kind, parent_id, title, state, origin, created_at, updated_at) VALUES(?,?,'task',?,?,?,?,?,?)`,
		id, owner, parent, title, TaskRunning, origin, t, t)
	return id, err
}

const taskColumns = `id, title, state, result, origin, parent_id, created_at, updated_at, reported_at`

func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var t Task
	var reported string
	err := row.Scan(&t.ID, &t.Title, &t.State, &t.Result, &t.Origin, &t.ParentID, &t.CreatedAt, &t.UpdatedAt, &reported)
	t.Reported = reported != ""
	return t, err
}

// Task is one of the owner's tasks (ok false when there is none by that id).
func (s *Store) Task(ctx context.Context, owner, id string) (Task, bool) {
	t, err := scanTask(s.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM threads WHERE id=? AND owner=? AND kind='task' AND archived_at=''`, id, owner))
	return t, err == nil
}

// Tasks are the owner's tasks that have not been put away, newest first.
func (s *Store) Tasks(ctx context.Context, owner string, limit int) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+taskColumns+` FROM threads WHERE owner=? AND kind='task' AND archived_at='' ORDER BY created_at DESC LIMIT ?`, owner, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) taskRunning(ctx context.Context, id string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE threads SET state=?, updated_at=? WHERE id=? AND kind='task'`, TaskRunning, now(), id)
}

func (s *Store) setTask(ctx context.Context, id, state, result string) {
	// A new state is news: the running thread is told again.
	_, _ = s.db.ExecContext(ctx, `UPDATE threads SET state=?, result=?, reported_at='', updated_at=? WHERE id=?`, state, result, now(), id)
}

// unreported are the owner's tasks that have ended and that the running thread has not
// been told about, oldest first; they are marked as told.
func (s *Store) unreported(ctx context.Context, owner string) []Task {
	rows, err := s.db.QueryContext(ctx, `SELECT `+taskColumns+` FROM threads WHERE owner=? AND kind='task' AND state NOT IN ('', 'running') AND reported_at='' ORDER BY updated_at LIMIT 8`, owner)
	if err != nil {
		return nil
	}
	var out []Task
	for rows.Next() {
		if t, err := scanTask(rows); err == nil {
			out = append(out, t)
		}
	}
	_ = rows.Close()
	for _, t := range out {
		s.markReported(ctx, t.ID)
	}
	return out
}

func (s *Store) markReported(ctx context.Context, id string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE threads SET reported_at=? WHERE id=? AND reported_at=''`, now(), id)
}

// ArchiveTask puts a task away. Nothing is deleted.
func (s *Store) ArchiveTask(ctx context.Context, owner, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE threads SET archived_at=? WHERE id=? AND owner=? AND kind='task' AND archived_at=''`, now(), id, owner)
	return err
}

// summary is the thread's summary and the sequence number of the last message it covers
// ("" and 0 when the thread has not been compacted).
func (s *Store) summary(ctx context.Context, thread string) (string, int) {
	var text string
	var upto int
	_ = s.db.QueryRowContext(ctx, `SELECT summary, summary_upto FROM threads WHERE id=?`, thread).Scan(&text, &upto)
	return text, upto
}

func (s *Store) setSummary(ctx context.Context, thread, text string, upto int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE threads SET summary=?, summary_upto=? WHERE id=?`, text, upto, thread)
	return err
}
