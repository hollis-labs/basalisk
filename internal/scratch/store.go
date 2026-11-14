package scratch

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // the driver, as the rest of the app uses it
)

// Store keeps the scratch items in a SQLite file of its own, beside the app's database.
// It is the working copy: every view is a query over it, and it holds what Caster does not
// (done, put away). Each item is also written through to Caster (sync.go), which is where
// the text lives for everything else.
type Store struct{ db *sql.DB }

var migrations = []string{
	`CREATE TABLE items (
		id TEXT PRIMARY KEY,
		owner TEXT NOT NULL,
		kind TEXT NOT NULL,
		text TEXT NOT NULL,
		done_at TEXT NOT NULL DEFAULT '',
		archived_at TEXT NOT NULL DEFAULT '',
		day TEXT NOT NULL,                       -- the local day it was written, YYYY-MM-DD
		context TEXT NOT NULL DEFAULT '{}',      -- JSON: where the owner was when it was written
		origin TEXT NOT NULL DEFAULT 'owner',    -- owner, agent
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		-- Caster: the ref the item is kept under, and the updated_at of the version it has.
		caster_ref TEXT NOT NULL DEFAULT '',
		synced_at TEXT NOT NULL DEFAULT '',
		sync_error TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX items_day ON items(owner, day, created_at);
	CREATE INDEX items_open ON items(owner, kind, done_at, archived_at);
	CREATE TABLE links (
		item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
		rel TEXT NOT NULL,
		value TEXT NOT NULL,
		PRIMARY KEY(item_id, rel, value)
	);
	CREATE INDEX links_value ON links(rel, value);`,
}

// Open opens (and creates or upgrades) the file at path.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var have int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&have); err != nil {
		_ = db.Close()
		return nil, err
	}
	if have > len(migrations) {
		_ = db.Close()
		return nil, fmt.Errorf("scratch: %s was written by a newer Basalisk", path)
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
			return nil, fmt.Errorf("scratch: schema step %d: %w", i+1, err)
		}
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Item is one scratch item.
type Item struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Text       string          `json:"text"`
	Done       bool            `json:"done"`
	DoneAt     string          `json:"done_at,omitempty"`
	Archived   bool            `json:"archived"`
	Day        string          `json:"day"`
	Links      []Link          `json:"links"`
	Context    json.RawMessage `json:"context"`
	Origin     string          `json:"origin"`
	CreatedAt  string          `json:"created_at"`
	UpdatedAt  string          `json:"updated_at"`
	CasterRef  string          `json:"caster_ref,omitempty"`
	Synced     bool            `json:"synced"`
	SyncError  string          `json:"sync_error,omitempty"`
	archivedAt string
}

// stamp is a time as it is stored: fixed width, so that stamps sort as text.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }

func newID() string { return "scr_" + strings.ToLower(rand.Text()[:16]) }

// Add writes what was typed as items (Parse) and returns them, oldest first.
func (s *Store) Add(ctx context.Context, owner, text, origin string, moment json.RawMessage, at time.Time) ([]Item, error) {
	parsed := Parse(text)
	if len(parsed) == 0 {
		return nil, errors.New("there is nothing to keep")
	}
	if len(moment) == 0 || !json.Valid(moment) {
		moment = json.RawMessage("{}")
	}
	if origin == "" {
		origin = "owner"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var ids []string
	for i, p := range parsed {
		id := newID()
		// One capture's items keep their order: each a moment after the last.
		created := stamp(at.Add(time.Duration(i) * time.Microsecond))
		done := ""
		if p.Done {
			done = created
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO items(id, owner, kind, text, done_at, day, context, origin, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			id, owner, p.Kind, p.Text, done, at.Format("2006-01-02"), string(moment), origin, created, created); err != nil {
			return nil, err
		}
		for _, l := range p.Links {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO links(item_id, rel, value) VALUES(?,?,?)`, id, l.Rel, l.Value); err != nil {
				return nil, err
			}
		}
		ids = append(ids, id)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(ids))
	for _, id := range ids {
		if it, ok := s.Get(ctx, owner, id); ok {
			out = append(out, it)
		}
	}
	return out, nil
}

const itemColumns = `id, kind, text, done_at, archived_at, day, context, origin, created_at, updated_at, caster_ref, synced_at, sync_error`

func (s *Store) scan(ctx context.Context, row interface{ Scan(...any) error }) (Item, error) {
	var it Item
	var moment, synced string
	if err := row.Scan(&it.ID, &it.Kind, &it.Text, &it.DoneAt, &it.archivedAt, &it.Day, &moment, &it.Origin, &it.CreatedAt, &it.UpdatedAt, &it.CasterRef, &synced, &it.SyncError); err != nil {
		return it, err
	}
	it.Done, it.Archived, it.Context = it.DoneAt != "", it.archivedAt != "", json.RawMessage(moment)
	// synced_at holds the updated_at of the version Caster has.
	it.Synced = synced == it.UpdatedAt
	it.Links = []Link{}
	return it, nil
}

func (s *Store) links(ctx context.Context, items []Item) error {
	if len(items) == 0 {
		return nil
	}
	at := map[string]int{}
	marks := make([]string, len(items))
	args := make([]any, len(items))
	for i, it := range items {
		at[it.ID], marks[i], args[i] = i, "?", it.ID
	}
	rows, err := s.db.QueryContext(ctx, `SELECT item_id, rel, value FROM links WHERE item_id IN (`+strings.Join(marks, ",")+`) ORDER BY rel, value`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var l Link
		if err := rows.Scan(&id, &l.Rel, &l.Value); err != nil {
			return err
		}
		items[at[id]].Links = append(items[at[id]].Links, l)
	}
	return rows.Err()
}

// Get is one of the owner's items.
func (s *Store) Get(ctx context.Context, owner, id string) (Item, bool) {
	it, err := s.scan(ctx, s.db.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM items WHERE id=? AND owner=?`, id, owner))
	if err != nil {
		return Item{}, false
	}
	one := []Item{it}
	_ = s.links(ctx, one)
	return one[0], true
}

// Query says which items a view shows. Empty fields do not narrow it.
type Query struct {
	Day      string // YYYY-MM-DD
	Kind     string
	Rel      string // with Value: items tied to it (project, agent, task, tag, session)
	Value    string
	Open     bool   // todos and follow-ups that are not done
	Text     string // words that must all appear
	Archived bool   // the ones put away, which are otherwise left out
	Limit    int
}

// List is a view: the owner's items that match, oldest first within the newest Limit.
func (s *Store) List(ctx context.Context, owner string, q Query) ([]Item, error) {
	where := []string{"owner=?"}
	args := []any{owner}
	if q.Archived {
		where = append(where, "archived_at<>''")
	} else {
		where = append(where, "archived_at=''")
	}
	if q.Day != "" {
		where, args = append(where, "day=?"), append(args, q.Day)
	}
	if q.Kind != "" {
		where, args = append(where, "kind=?"), append(args, q.Kind)
	}
	if q.Open {
		where = append(where, "kind IN ('todo','follow-up','question') AND done_at=''")
	}
	if q.Rel != "" && q.Value != "" {
		value := q.Value
		if q.Rel != RelTask {
			value = strings.ToLower(value)
		}
		where, args = append(where, "id IN (SELECT item_id FROM links WHERE rel=? AND value=?)"), append(args, q.Rel, value)
	}
	for _, word := range strings.Fields(q.Text) {
		where, args = append(where, "text LIKE ? ESCAPE '\\'"), append(args, "%"+likeEscape(word)+"%")
	}
	limit := q.Limit
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+itemColumns+` FROM items WHERE `+strings.Join(where, " AND ")+` ORDER BY created_at DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		it, err := s.scan(ctx, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, s.links(ctx, out)
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Change is an edit to one item. Nil fields are left as they are.
type Change struct {
	Text     *string
	Done     *bool
	Archived *bool
}

// Update edits one item. New text is read again for its kind and links (the first item of
// what is typed; an edit does not split one item into several).
func (s *Store) Update(ctx context.Context, owner, id string, c Change, at time.Time) (Item, error) {
	cur, ok := s.Get(ctx, owner, id)
	if !ok {
		return Item{}, errors.New("there is no such item")
	}
	now := stamp(at)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Item{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if c.Text != nil {
		parsed := Parse(*c.Text)
		if len(parsed) == 0 {
			return Item{}, errors.New("an item cannot be emptied; put it away instead")
		}
		p := parsed[0]
		// Text typed without a marker keeps the kind the item had.
		if p.Kind == KindNote && cur.Kind != KindNote && !strings.HasPrefix(strings.TrimSpace(*c.Text), ":") {
			p.Kind = cur.Kind
		}
		for _, more := range parsed[1:] {
			p.Text += "\n\n" + more.Text
			for _, l := range more.Links {
				p.Links = addLink(p.Links, l.Rel, l.Value)
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE items SET kind=?, text=?, updated_at=? WHERE id=?`, p.Kind, p.Text, now, id); err != nil {
			return Item{}, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM links WHERE item_id=?`, id); err != nil {
			return Item{}, err
		}
		for _, l := range p.Links {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO links(item_id, rel, value) VALUES(?,?,?)`, id, l.Rel, l.Value); err != nil {
				return Item{}, err
			}
		}
		if p.Done && c.Done == nil {
			yes := true
			c.Done = &yes
		}
	}
	set := func(col string, on bool) error {
		v := ""
		if on {
			v = now
		}
		_, err := tx.ExecContext(ctx, `UPDATE items SET `+col+`=?, updated_at=? WHERE id=?`, v, now, id)
		return err
	}
	if c.Done != nil && *c.Done != cur.Done {
		if err := set("done_at", *c.Done); err != nil {
			return Item{}, err
		}
	}
	if c.Archived != nil && *c.Archived != cur.Archived {
		if err := set("archived_at", *c.Archived); err != nil {
			return Item{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Item{}, err
	}
	it, _ := s.Get(ctx, owner, id)
	return it, nil
}

// Count is something the items can be viewed by, and how many there are.
type Count struct {
	Value string `json:"value"`
	N     int    `json:"n"`
}

// Facets are the ways into the scratch: recent days, kinds, and what items are tied to,
// each with how many items (not put away) it has.
type Facets struct {
	Days     []Count `json:"days"`
	Kinds    []Count `json:"kinds"`
	Projects []Count `json:"projects"`
	Tasks    []Count `json:"tasks"`
	Agents   []Count `json:"agents"`
	Tags     []Count `json:"tags"`
	Open     int     `json:"open"`
	Total    int     `json:"total"`
}

func (s *Store) Facets(ctx context.Context, owner string) (Facets, error) {
	f := Facets{Days: []Count{}, Kinds: []Count{}, Projects: []Count{}, Tasks: []Count{}, Agents: []Count{}, Tags: []Count{}}
	counts := func(query string, args ...any) ([]Count, error) {
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []Count{}
		for rows.Next() {
			var c Count
			if err := rows.Scan(&c.Value, &c.N); err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		return out, rows.Err()
	}
	var err error
	if f.Days, err = counts(`SELECT day, COUNT(*) FROM items WHERE owner=? AND archived_at='' GROUP BY day ORDER BY day DESC LIMIT 60`, owner); err != nil {
		return f, err
	}
	if f.Kinds, err = counts(`SELECT kind, COUNT(*) FROM items WHERE owner=? AND archived_at='' GROUP BY kind ORDER BY COUNT(*) DESC, kind`, owner); err != nil {
		return f, err
	}
	for rel, into := range map[string]*[]Count{RelProject: &f.Projects, RelTask: &f.Tasks, RelAgent: &f.Agents, RelTag: &f.Tags} {
		if *into, err = counts(`SELECT l.value, COUNT(*) FROM links l JOIN items i ON i.id=l.item_id WHERE i.owner=? AND i.archived_at='' AND l.rel=? GROUP BY l.value ORDER BY COUNT(*) DESC, l.value LIMIT 60`, owner, rel); err != nil {
			return f, err
		}
	}
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE owner=? AND archived_at='' AND kind IN ('todo','follow-up','question') AND done_at=''`, owner).Scan(&f.Open)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE owner=? AND archived_at=''`, owner).Scan(&f.Total)
	return f, nil
}

// Markdown writes items as one page: the projection a view becomes when it is read or
// saved as a file. Items are in the order given, each with its marker, so the page reads
// back in as the same items.
func Markdown(title string, items []Item) string {
	var b strings.Builder
	if title != "" {
		b.WriteString("# " + title + "\n\n")
	}
	for _, it := range items {
		switch it.Kind {
		case KindNote:
			b.WriteString(it.Text)
		case KindCommand:
			b.WriteString("$ " + it.Text)
		case KindTodo:
			box := "[ ] "
			if it.Done {
				box = "[x] "
			}
			b.WriteString(box + it.Text)
		default:
			b.WriteString("::" + it.Kind + " " + it.Text)
			if it.Done {
				b.WriteString("  (done)")
			}
		}
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}
