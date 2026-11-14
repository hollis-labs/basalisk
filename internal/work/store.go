package work

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite" // the driver, as the rest of the app uses it
)

// Store is the working copy of the Work items (work.db, beside the app's database): a mirror
// of the Caster items for quick views and reading when Caster is away, and the owner's notes
// not yet sent. Nothing lives only here once it is in Caster.
//
// Writes go to Caster at once, each after reading the item again: Caster's put has no "only
// if still at revision N", so a contended item (a parent's children, a view's layout, a
// card's stage) is re-read and the change applied to what is there, and an edit begun on an
// older revision does not overwrite the newer one; the owner's text is kept as a suggestion
// on it instead.
type Store struct {
	db     *sql.DB
	caster Caster

	mu        sync.Mutex // one write or refresh at a time
	refreshed map[string]time.Time
	lastErr   map[string]string
}

var migrations = []string{
	`CREATE TABLE items (
		ns TEXT NOT NULL,             -- "work" (the index) or w-<workspace>
		name TEXT NOT NULL,           -- i.<id>, kind.<kind>, v.<id>, r.<id>.<n>; <workspace> in "work"
		rev INTEGER NOT NULL,
		title TEXT NOT NULL DEFAULT '',
		content TEXT NOT NULL,
		updated TEXT NOT NULL DEFAULT '', -- Caster's time of that revision
		fetched_at TEXT NOT NULL,
		PRIMARY KEY (ns, name)
	);
	CREATE TABLE notes (
		id TEXT PRIMARY KEY,
		ws TEXT NOT NULL,
		item TEXT NOT NULL DEFAULT '',
		item_rev INTEGER NOT NULL DEFAULT 0,
		kind TEXT NOT NULL,             -- comment, suggestion
		section TEXT NOT NULL DEFAULT '',
		quote TEXT NOT NULL DEFAULT '',
		replace TEXT NOT NULL DEFAULT '',
		text TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		round INTEGER NOT NULL DEFAULT 0 -- 0: not sent
	);
	CREATE INDEX notes_item ON notes(ws, item, round);`,
}

// Open opens (and creates or upgrades) the file at path. caster may be nil (no gateway):
// the copy is then read only and says why.
func Open(ctx context.Context, path string, caster Caster) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
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
		return nil, fmt.Errorf("work: %s was written by a newer Basalisk", path)
	}
	for i := have; i < len(migrations); i++ {
		if _, err := db.ExecContext(ctx, migrations[i]); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("work: migration %d: %w", i+1, err)
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", i+1)); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return &Store{db: db, caster: caster, refreshed: map[string]time.Time{}, lastErr: map[string]string{}}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// ErrNoCaster: no gateway is configured, so nothing can be written.
var ErrNoCaster = errors.New("Caster is not reachable from this Basalisk (no Tether gateway)")

// ErrNotFound: no such workspace or item.
var ErrNotFound = errors.New("there is no such workspace or item")

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// ---- Read-back ----

// Refresh brings the copy of one namespace up to date with Caster: one listing, and a fetch
// for each item whose revision moved. At most every 15 s per namespace unless forced.
func (s *Store) Refresh(ctx context.Context, ns string, force bool) (int, error) {
	if s.caster == nil {
		return 0, ErrNoCaster
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !force && time.Since(s.refreshed[ns]) < 15*time.Second {
		if e := s.lastErr[ns]; e != "" {
			return 0, errors.New(e)
		}
		return 0, nil
	}
	n, err := s.refreshLocked(ctx, ns)
	s.refreshed[ns] = time.Now()
	s.lastErr[ns] = ""
	if err != nil {
		s.lastErr[ns] = err.Error()
	}
	return n, err
}

func (s *Store) refreshLocked(ctx context.Context, ns string) (int, error) {
	heads, err := s.caster.List(ctx, ns)
	if err != nil {
		return 0, fmt.Errorf("Caster could not be listed: %w", err)
	}
	have := map[string]int{}
	rows, err := s.db.QueryContext(ctx, `SELECT name, rev FROM items WHERE ns=?`, ns)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var name string
		var rev int
		if rows.Scan(&name, &rev) == nil {
			have[name] = rev
		}
	}
	_ = rows.Close()
	changed := 0
	for _, h := range heads {
		if have[h.Name] == h.Rev || !wanted(ns, h.Name) {
			continue
		}
		it, ok, err := s.caster.Get(ctx, ns, h.Name, 0)
		if err != nil {
			return changed, err
		}
		if !ok {
			continue
		}
		if err := s.keep(ctx, it, h.Updated); err != nil {
			return changed, err
		}
		changed++
	}
	return changed, nil
}

// wanted says whether a listed name is one the engine reads.
func wanted(ns, name string) bool {
	if ns == IndexNS {
		return ValidSlug(name)
	}
	k, _ := ParseName(name)
	return k != NameOther
}

// keep puts an item's revision in the copy (never an older one over a newer).
func (s *Store) keep(ctx context.Context, it Item, updated string) error {
	if updated == "" {
		updated = now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO items(ns, name, rev, title, content, updated, fetched_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(ns, name) DO UPDATE SET rev=excluded.rev, title=excluded.title, content=excluded.content, updated=excluded.updated, fetched_at=excluded.fetched_at
		WHERE excluded.rev >= items.rev`, it.NS, it.Name, it.Rev, it.Title, it.Content, updated, now())
	return err
}

type row struct {
	name, title, content, updated string
	rev                           int
}

func (s *Store) rows(ctx context.Context, ns, prefix string) ([]row, error) {
	rs, err := s.db.QueryContext(ctx, `SELECT name, rev, title, content, updated FROM items WHERE ns=? AND substr(name, 1, ?)=?`, ns, len(prefix), prefix)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []row
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.name, &r.rev, &r.title, &r.content, &r.updated); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

func (s *Store) copyOf(ctx context.Context, ns, name string) (row, bool) {
	var r row
	err := s.db.QueryRowContext(ctx, `SELECT name, rev, title, content, updated FROM items WHERE ns=? AND name=?`, ns, name).
		Scan(&r.name, &r.rev, &r.title, &r.content, &r.updated)
	return r, err == nil
}

// ---- Views ----

// Workspace is one body of work, from its index item.
type Workspace struct {
	Slug    string   `json:"slug"`
	Title   string   `json:"title"`
	App     string   `json:"app"`
	Preset  string   `json:"preset"`
	NS      string   `json:"ns"`
	Owners  []string `json:"owners"`
	Created string   `json:"created,omitempty"`
	Body    string   `json:"body"`
	Items   int      `json:"items"`
	Rev     int      `json:"rev"`
	Updated string   `json:"updated"`
	Problem string   `json:"problem,omitempty"`
}

func workspaceOf(r row) Workspace {
	w := Workspace{Slug: r.name, Title: r.name, NS: NS(r.name), Owners: []string{}, Rev: r.rev, Updated: r.updated}
	d, err := ParseDoc(r.content)
	if err != nil {
		w.Problem = err.Error()
		return w
	}
	if t := strings.TrimSpace(d.Str("title")); t != "" {
		w.Title = t
	}
	w.App, w.Preset, w.Created, w.Body = d.Str("app"), d.Str("preset"), d.Str("created"), d.Body
	if ns := d.Str("ns"); ns != "" {
		w.NS = ns
	}
	if o := d.Strings("owners"); o != nil {
		w.Owners = o
	}
	return w
}

// Workspaces lists the workspaces (for one app when app is set), newest change first.
func (s *Store) Workspaces(ctx context.Context, app string) ([]Workspace, error) {
	all, err := s.rows(ctx, IndexNS, "")
	if err != nil {
		return nil, err
	}
	out := []Workspace{}
	for _, r := range all {
		if !ValidSlug(r.name) {
			continue
		}
		w := workspaceOf(r)
		if app != "" && w.App != app {
			continue
		}
		_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM items WHERE ns=? AND substr(name,1,2)='i.'`, w.NS).Scan(&w.Items)
		out = append(out, w)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated > out[j].Updated })
	return out, nil
}

// workspace reads a workspace's index from the copy.
func (s *Store) workspace(ctx context.Context, ws string) (Workspace, bool) {
	if !ValidSlug(ws) {
		return Workspace{}, false
	}
	r, ok := s.copyOf(ctx, IndexNS, ws)
	if !ok {
		return Workspace{}, false
	}
	return workspaceOf(r), true
}

// NSOf is the namespace a workspace's items are in ("" when there is no such workspace).
func (s *Store) NSOf(ctx context.Context, ws string) string {
	w, ok := s.workspace(ctx, ws)
	if !ok {
		return ""
	}
	return w.NS
}

// Card is an item as the views show it.
type Card struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Title     string         `json:"title"`
	Stage     string         `json:"stage"`
	Fields    map[string]any `json:"fields"`
	Parent    string         `json:"parent,omitempty"`
	Children  []string       `json:"children"`
	Links     []Link         `json:"links"`
	Material  []Material     `json:"material"`
	Published []Published    `json:"published"`
	Task      string         `json:"task,omitempty"`
	Tags      []string       `json:"tags"`
	Cut       bool           `json:"cut,omitempty"`
	Excerpt   string         `json:"excerpt,omitempty"`
	Words     int            `json:"words"`
	Rev       int            `json:"rev"`
	Updated   string         `json:"updated"`
	Problem   string         `json:"problem,omitempty"`
}

// Detail is one item opened: its card, its sections and the owner's notes on it.
type Detail struct {
	Card
	Ref      string            `json:"ref"`
	Sections map[string]string `json:"sections"`
	Order    []string          `json:"order"` // the sections shown: the kind's, then any other heading
	Lead     string            `json:"lead,omitempty"`
	Notes    []Note            `json:"notes"`
}

func cardOf(id string, r row, kinds map[string]Kind) (Card, *Doc) {
	c := Card{ID: id, Title: id, Rev: r.rev, Updated: r.updated, Fields: map[string]any{}, Children: []string{},
		Links: []Link{}, Material: []Material{}, Published: []Published{}, Tags: []string{}}
	d, err := ParseDoc(r.content)
	if err != nil {
		c.Problem = err.Error()
		return c, nil
	}
	if t := strings.TrimSpace(d.Str("title")); t != "" {
		c.Title = t
	}
	c.Kind, c.Stage, c.Parent, c.Task, c.Cut = d.Str("kind"), d.Str("stage"), d.Str("parent"), d.Str("task"), d.Bool("cut")
	_ = d.Decode("fields", &c.Fields)
	_ = d.Decode("links", &c.Links)
	_ = d.Decode("material", &c.Material)
	_ = d.Decode("published", &c.Published)
	if v := d.Strings("children"); v != nil {
		c.Children = v
	}
	if v := d.Strings("tags"); v != nil {
		c.Tags = v
	}
	// A key present but empty (null) decodes to nil; the views want empty lists.
	if c.Fields == nil {
		c.Fields = map[string]any{}
	}
	if c.Links == nil {
		c.Links = []Link{}
	}
	if c.Material == nil {
		c.Material = []Material{}
	}
	if c.Published == nil {
		c.Published = []Published{}
	}
	if c.Stage == "" {
		if k, ok := kinds[c.Kind]; ok && len(k.Stages) > 0 {
			c.Stage = k.Stages[0]
		}
	}
	b := ParseBody(d.Body)
	for _, sec := range b.Sections {
		c.Words += len(strings.Fields(sec.Text))
		if c.Excerpt == "" {
			if line := firstLine(sec.Text); line != "" {
				c.Excerpt = line
			}
		}
	}
	if c.Excerpt == "" {
		c.Excerpt = firstLine(b.Lead)
	}
	c.Words += len(strings.Fields(b.Lead))
	return c, d
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if len(l) > 160 {
				l = l[:160] + "…"
			}
			return l
		}
	}
	return ""
}

// Kinds is a workspace's kinds from the copy, by name.
func (s *Store) Kinds(ctx context.Context, ns string) (map[string]Kind, []Kind) {
	rs, _ := s.rows(ctx, ns, "kind.")
	byName := map[string]Kind{}
	list := []Kind{}
	for _, r := range rs {
		_, name := ParseName(r.name)
		if name == "" {
			continue
		}
		k := Kind{Kind: name, Label: name}
		if err := yaml.Unmarshal([]byte(r.content), &k); err != nil {
			k.Problem = err.Error()
		}
		k.Kind, k.Rev = name, r.rev
		for _, p := range []*[]string{&k.Stages, &k.Sections, &k.Children} {
			if *p == nil {
				*p = []string{}
			}
		}
		if k.Fields == nil {
			k.Fields = []Field{}
		}
		byName[name] = k
		list = append(list, k)
	}
	return byName, list
}

// Views is a workspace's saved views from the copy.
func (s *Store) Views(ctx context.Context, ns string) []View {
	rs, _ := s.rows(ctx, ns, "v.")
	out := []View{}
	for _, r := range rs {
		_, id := ParseName(r.name)
		if id == "" {
			continue
		}
		v := View{}
		if err := yaml.Unmarshal([]byte(r.content), &v); err != nil {
			v.Problem = err.Error()
		}
		v.ID, v.Rev = id, r.rev
		if v.Kinds == nil {
			v.Kinds = []string{}
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out
}

// Space is a workspace opened: its index, kinds, items and views.
type Space struct {
	Workspace Workspace `json:"workspace"`
	Kinds     []Kind    `json:"kinds"`
	Items     []Card    `json:"items"`
	Views     []View    `json:"views"`
}

// Open is a workspace from the copy. ok is false when there is no such workspace.
func (s *Store) Open(ctx context.Context, ws string) (Space, bool, error) {
	w, ok := s.workspace(ctx, ws)
	if !ok {
		return Space{}, false, nil
	}
	kinds, list := s.Kinds(ctx, w.NS)
	rs, err := s.rows(ctx, w.NS, "i.")
	if err != nil {
		return Space{}, false, err
	}
	items := make([]Card, 0, len(rs))
	for _, r := range rs {
		_, id := ParseName(r.name)
		if id == "" {
			continue
		}
		c, _ := cardOf(id, r, kinds)
		items = append(items, c)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Updated > items[j].Updated })
	// Kinds in the preset's order (a kind made later goes after them, by name).
	rank := map[string]int{}
	if p, ok := Presets()[w.Preset]; ok {
		for i, k := range p.Kinds {
			rank[k.Kind] = i + 1
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		ri, rj := rank[list[i].Kind], rank[list[j].Kind]
		if ri == 0 {
			ri = 1 << 20
		}
		if rj == 0 {
			rj = 1 << 20
		}
		if ri != rj {
			return ri < rj
		}
		return list[i].Kind < list[j].Kind
	})
	w.Items = len(items)
	return Space{Workspace: w, Kinds: list, Items: items, Views: s.Views(ctx, w.NS)}, true, nil
}

// Detail is one item from the copy, with its sections and notes.
func (s *Store) Detail(ctx context.Context, ws, id string) (Detail, bool, error) {
	ns := s.NSOf(ctx, ws)
	if ns == "" || !ValidID(id) {
		return Detail{}, false, nil
	}
	r, ok := s.copyOf(ctx, ns, ItemName(id))
	if !ok {
		return Detail{}, false, nil
	}
	kinds, _ := s.Kinds(ctx, ns)
	c, d := cardOf(id, r, kinds)
	out := Detail{Card: c, Ref: Ref(ns, ItemName(id)), Sections: map[string]string{}, Order: []string{}, Notes: []Note{}}
	if d != nil {
		b := ParseBody(d.Body)
		out.Lead = strings.TrimSpace(b.Lead)
		seen := map[string]bool{}
		for _, sec := range kinds[c.Kind].Sections {
			out.Order = append(out.Order, sec)
			out.Sections[sec] = b.Get(sec)
			seen[norm(sec)] = true
		}
		for _, sec := range b.Sections {
			if !seen[norm(sec.Heading)] {
				seen[norm(sec.Heading)] = true
				out.Order = append(out.Order, sec.Heading)
				out.Sections[sec.Heading] = sec.Text
			}
		}
	}
	out.Notes, _ = s.Notes(ctx, ws, id)
	return out, true, nil
}

// ---- Writes ----

// write puts an item and keeps the new revision in the copy.
func (s *Store) write(ctx context.Context, ns, name, content, title string) (int, error) {
	rev, err := s.caster.Put(ctx, ns, name, content, title)
	if err != nil {
		return 0, fmt.Errorf("Caster did not take the change: %w", err)
	}
	return rev, s.keep(ctx, Item{NS: ns, Name: name, Rev: rev, Title: title, Content: content}, now())
}

// fresh reads an item from Caster as it is now, keeping it in the copy too.
func (s *Store) fresh(ctx context.Context, ns, name string) (Item, bool, error) {
	it, ok, err := s.caster.Get(ctx, ns, name, 0)
	if err != nil {
		return Item{}, false, fmt.Errorf("Caster could not be read: %w", err)
	}
	if ok {
		_ = s.keep(ctx, it, "")
	}
	return it, ok, nil
}

// space is a workspace's namespace, read from Caster when the copy does not have it yet.
func (s *Store) space(ctx context.Context, ws string) (string, error) {
	if ns := s.NSOf(ctx, ws); ns != "" {
		return ns, nil
	}
	if !ValidSlug(ws) {
		return "", ErrNotFound
	}
	it, ok, err := s.fresh(ctx, IndexNS, ws)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNotFound
	}
	return workspaceOf(row{name: ws, content: it.Content, rev: it.Rev}).NS, nil
}

// change re-reads an item and applies fn to it as it is now (the lock is held).
func (s *Store) changeLocked(ctx context.Context, ns, id string, fn func(d *Doc) error) (int, error) {
	it, ok, err := s.fresh(ctx, ns, ItemName(id))
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, ErrNotFound
	}
	d, err := ParseDoc(it.Content)
	if err != nil {
		return 0, fmt.Errorf("item %s cannot be read: %w", id, err)
	}
	if err := fn(d); err != nil {
		return 0, err
	}
	title := d.Str("title")
	if title == "" {
		title = id
	}
	return s.write(ctx, ns, ItemName(id), d.String(), title)
}

func (s *Store) change(ctx context.Context, ws, id string, fn func(d *Doc) error) (int, error) {
	if s.caster == nil {
		return 0, ErrNoCaster
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, err := s.space(ctx, ws)
	if err != nil {
		return 0, err
	}
	return s.changeLocked(ctx, ns, id, fn)
}

// NewWorkspace makes a workspace from a preset: its kinds and views in its own namespace,
// then its index in "work" (the index is what makes it exist). Refused when it exists.
func (s *Store) NewWorkspace(ctx context.Context, slug, title, preset, about string) (string, error) {
	if s.caster == nil {
		return "", ErrNoCaster
	}
	if !ValidSlug(slug) {
		return "", errors.New("a workspace name takes lowercase letters, digits and '-'")
	}
	p, ok := Presets()[preset]
	if !ok {
		return "", fmt.Errorf("there is no preset %q", preset)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok, err := s.fresh(ctx, IndexNS, slug); err != nil {
		return "", err
	} else if ok {
		return "", fmt.Errorf("there is already a workspace %s", slug)
	}
	ns := NS(slug)
	for _, k := range p.Kinds {
		if _, err := s.write(ctx, ns, KindName(k.Kind), k.YAML(), k.Label); err != nil {
			return "", err
		}
	}
	for _, v := range p.Views {
		if _, err := s.write(ctx, ns, ViewName(NewID()), viewYAML(v), v.Title); err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(title) == "" {
		title = slug
	}
	d, _ := ParseDoc("")
	for _, kv := range []struct {
		k string
		v any
	}{{"work", "v1"}, {"workspace", slug}, {"title", title}, {"app", p.App}, {"preset", p.ID}, {"ns", ns},
		{"owners", []string{}}, {"created", time.Now().UTC().Format(time.RFC3339)}} {
		_ = d.Set(kv.k, kv.v)
	}
	d.Body = strings.TrimSpace(about) + "\n"
	_, err := s.write(ctx, IndexNS, slug, d.String(), title)
	return slug, err
}

func viewYAML(v View) string {
	if v.Kinds == nil {
		v.Kinds = []string{}
	}
	b, _ := yaml.Marshal(v)
	return string(b)
}

// NewItem is what the owner asks to make.
type NewItem struct {
	Kind   string         `json:"kind"`
	Title  string         `json:"title"`
	Stage  string         `json:"stage"`
	Parent string         `json:"parent"`
	Fields map[string]any `json:"fields"`
}

// MakeItem makes an item of a kind the workspace has, at the stage given (or the kind's
// first), and puts it at the end of its parent's children when it has one.
func (s *Store) MakeItem(ctx context.Context, ws string, in NewItem) (string, error) {
	if s.caster == nil {
		return "", ErrNoCaster
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, err := s.space(ctx, ws)
	if err != nil {
		return "", err
	}
	kinds, _ := s.Kinds(ctx, ns)
	k, ok := kinds[in.Kind]
	if !ok {
		return "", fmt.Errorf("this workspace has no kind %q", in.Kind)
	}
	if in.Stage == "" && len(k.Stages) > 0 {
		in.Stage = k.Stages[0]
	}
	if in.Stage != "" && !k.HasStage(in.Stage) {
		return "", fmt.Errorf("a %s has no stage %q", k.Label, in.Stage)
	}
	if in.Parent != "" {
		pr, ok := s.copyOf(ctx, ns, ItemName(in.Parent))
		if !ok {
			return "", fmt.Errorf("there is no item %s to put it in", in.Parent)
		}
		pc, _ := cardOf(in.Parent, pr, kinds)
		if pk := kinds[pc.Kind]; len(pk.Children) > 0 && !contains(pk.Children, in.Kind) {
			return "", fmt.Errorf("a %s does not hold a %s", pk.Label, k.Label)
		}
	}
	id := ""
	for range 8 {
		c := NewID()
		if _, taken := s.copyOf(ctx, ns, ItemName(c)); taken {
			continue
		}
		if _, ok, err := s.fresh(ctx, ns, ItemName(c)); err != nil {
			return "", err
		} else if !ok {
			id = c
			break
		}
	}
	if id == "" {
		return "", errors.New("no free item id was found")
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "New " + strings.ToLower(k.Label)
	}
	d, _ := ParseDoc("")
	fields := map[string]any{}
	for key, v := range in.Fields {
		if _, ok := k.Field(key); ok && v != nil && v != "" {
			fields[key] = v
		}
	}
	for _, kv := range []struct {
		k string
		v any
	}{{"work", "v1"}, {"id", id}, {"kind", in.Kind}, {"title", title}, {"stage", in.Stage}, {"fields", fields},
		{"parent", in.Parent}, {"children", []string{}}, {"links", []Link{}}, {"material", []Material{}},
		{"published", []Published{}}, {"tags", []string{}}} {
		_ = d.Set(kv.k, kv.v)
	}
	var b Body
	for _, sec := range k.Sections {
		b.Put(sec, "", k.Sections)
	}
	d.Body = b.String()
	if _, err := s.write(ctx, ns, ItemName(id), d.String(), title); err != nil {
		return "", err
	}
	if in.Parent != "" {
		if _, err := s.changeLocked(ctx, ns, in.Parent, func(pd *Doc) error {
			list := pd.Strings("children")
			if contains(list, id) {
				return nil
			}
			return pd.Set("children", append(list, id))
		}); err != nil {
			return id, fmt.Errorf("the item was made, but its parent's list was not changed: %w", err)
		}
	}
	return id, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Move sets one value on an item as it is now: its stage (key "stage") or an enum or date
// field (a board column, a calendar day). Nothing else of the item changes.
func (s *Store) Move(ctx context.Context, ws, id, key, value string) (int, error) {
	ns := s.NSOf(ctx, ws)
	kinds, _ := s.Kinds(ctx, ns)
	return s.change(ctx, ws, id, func(d *Doc) error {
		k, ok := kinds[d.Str("kind")]
		if !ok {
			return fmt.Errorf("item %s is of a kind this workspace does not describe", id)
		}
		if key == "stage" {
			if !k.HasStage(value) {
				return fmt.Errorf("a %s has no stage %q", k.Label, value)
			}
			return d.Set("stage", value)
		}
		f, ok := k.Field(key)
		if !ok {
			return fmt.Errorf("a %s has no field %q", k.Label, key)
		}
		if value == "" {
			return d.SetField(key, nil)
		}
		switch f.Type {
		case "enum":
			if !contains(f.Options, value) {
				return fmt.Errorf("%q is not one of %s's options", value, f.Key)
			}
		case "date":
			if _, err := time.Parse("2006-01-02", value); err != nil {
				return fmt.Errorf("a date is YYYY-MM-DD")
			}
		default:
			return fmt.Errorf("%s is not set by moving the card", f.Key)
		}
		return d.SetField(key, value)
	})
}

// Cut takes an item out of the views (cut: true) and out of its parent's children; it stays
// in Caster and can be restored.
func (s *Store) Cut(ctx context.Context, ws, id string) error {
	if s.caster == nil {
		return ErrNoCaster
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, err := s.space(ctx, ws)
	if err != nil {
		return err
	}
	parent := ""
	if _, err := s.changeLocked(ctx, ns, id, func(d *Doc) error {
		parent = d.Str("parent")
		return d.Set("cut", true)
	}); err != nil {
		return err
	}
	if parent != "" {
		_, err = s.changeLocked(ctx, ns, parent, func(d *Doc) error {
			list := d.Strings("children")
			next := []string{}
			for _, c := range list {
				if c != id {
					next = append(next, c)
				}
			}
			return d.Set("children", next)
		})
		if errors.Is(err, ErrNotFound) {
			err = nil
		}
	}
	return err
}

// Restore puts a cut item back, at the end of its parent's children.
func (s *Store) Restore(ctx context.Context, ws, id string) error {
	if s.caster == nil {
		return ErrNoCaster
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, err := s.space(ctx, ws)
	if err != nil {
		return err
	}
	parent := ""
	if _, err := s.changeLocked(ctx, ns, id, func(d *Doc) error {
		parent = d.Str("parent")
		d.Del("cut")
		return nil
	}); err != nil {
		return err
	}
	if parent != "" {
		_, err = s.changeLocked(ctx, ns, parent, func(d *Doc) error {
			list := d.Strings("children")
			if contains(list, id) {
				return nil
			}
			return d.Set("children", append(list, id))
		})
		if errors.Is(err, ErrNotFound) {
			err = nil
		}
	}
	return err
}

// Edit is the owner's change to an item, made on the revision BaseRev. Nil fields are left
// alone; Fields sets the keys given (null takes one out).
type Edit struct {
	BaseRev  int               `json:"base_rev"`
	Title    *string           `json:"title"`
	Fields   map[string]any    `json:"fields"`
	Sections map[string]string `json:"sections"`
	Tags     *[]string         `json:"tags"`
}

// Conflict: the item moved on in Caster after the owner began the edit. Nothing was
// overwritten; the owner's changed sections were kept as suggestions on the newer revision
// (and any other change as a comment).
type Conflict struct {
	Rev         int
	Suggestions int
}

func (c *Conflict) Error() string {
	return fmt.Sprintf("this item was changed in Caster meanwhile (now revision %d); your text was kept as %d suggestion(s) on it, not written over it", c.Rev, c.Suggestions)
}

// EditItem writes the owner's edit as a new revision, unless the item moved on since
// BaseRev (then *Conflict).
func (s *Store) EditItem(ctx context.Context, ws, id string, e Edit) (int, error) {
	if s.caster == nil {
		return 0, ErrNoCaster
	}
	for sec := range e.Sections {
		if strings.TrimSpace(sec) == "" || strings.ContainsAny(sec, "\n#") {
			return 0, fmt.Errorf("%q is not a section heading", sec)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, err := s.space(ctx, ws)
	if err != nil {
		return 0, err
	}
	name := ItemName(id)
	it, ok, err := s.fresh(ctx, ns, name)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, ErrNotFound
	}
	d, err := ParseDoc(it.Content)
	if err != nil {
		return 0, fmt.Errorf("the item cannot be read: %w", err)
	}
	kinds, _ := s.Kinds(ctx, ns)
	k := kinds[d.Str("kind")]
	for key := range e.Fields {
		if _, ok := k.Field(key); !ok && k.Kind != "" {
			return 0, fmt.Errorf("a %s has no field %q", k.Label, key)
		}
	}
	if it.Rev != e.BaseRev {
		// Keep the owner's words as suggestions against the newer text; never overwrite it.
		base, _, _ := s.caster.Get(ctx, ns, name, e.BaseRev)
		baseBody := Body{}
		if bd, err := ParseDoc(base.Content); err == nil {
			baseBody = ParseBody(bd.Body)
		}
		nowBody := ParseBody(d.Body)
		n := 0
		for sec, text := range e.Sections {
			if strings.TrimSpace(text) == strings.TrimSpace(baseBody.Get(sec)) {
				continue // not changed by the owner
			}
			if err := s.addNote(ctx, Note{WS: ws, Item: id, ItemRev: it.Rev, Kind: "suggestion", Section: sec,
				Quote: nowBody.Get(sec), Replace: text,
				Text: fmt.Sprintf("My edit to %s, made on revision %d, which changed meanwhile.", sec, e.BaseRev)}); err != nil {
				return 0, err
			}
			n++
		}
		var other []string
		if e.Title != nil {
			other = append(other, fmt.Sprintf("title: %s", *e.Title))
		}
		for key, v := range e.Fields {
			other = append(other, fmt.Sprintf("%s: %v", key, v))
		}
		if e.Tags != nil {
			other = append(other, "tags: "+strings.Join(*e.Tags, ", "))
		}
		if len(other) > 0 {
			sort.Strings(other)
			if err := s.addNote(ctx, Note{WS: ws, Item: id, ItemRev: it.Rev, Kind: "comment",
				Text: fmt.Sprintf("My change made on revision %d, not written: %s", e.BaseRev, strings.Join(other, "; "))}); err != nil {
				return 0, err
			}
			n++
		}
		return 0, &Conflict{Rev: it.Rev, Suggestions: n}
	}
	if e.Title != nil && strings.TrimSpace(*e.Title) != "" {
		_ = d.Set("title", strings.TrimSpace(*e.Title))
	}
	for key, v := range e.Fields {
		if sv, ok := v.(string); ok && strings.TrimSpace(sv) == "" {
			v = nil
		}
		if err := d.SetField(key, v); err != nil {
			return 0, err
		}
	}
	if e.Tags != nil {
		tags := []string{}
		for _, t := range *e.Tags {
			if t = strings.TrimSpace(t); t != "" && !contains(tags, t) {
				tags = append(tags, t)
			}
		}
		_ = d.Set("tags", tags)
	}
	if len(e.Sections) > 0 {
		b := ParseBody(d.Body)
		keys := make([]string, 0, len(e.Sections))
		for sec := range e.Sections {
			keys = append(keys, sec)
		}
		sort.Strings(keys)
		for _, sec := range keys {
			b.Put(sec, e.Sections[sec], k.Sections)
		}
		d.Body = b.String()
	}
	title := d.Str("title")
	if title == "" {
		title = id
	}
	return s.write(ctx, ns, name, d.String(), title)
}

// SaveView changes a view as it is now: the keys given (view, title, kinds, group_by,
// date_field, root, rel, filter, layout); other keys stay as they are.
func (s *Store) SaveView(ctx context.Context, ws, id string, set map[string]any) (int, error) {
	if s.caster == nil {
		return 0, ErrNoCaster
	}
	allowed := map[string]bool{"view": true, "title": true, "kinds": true, "group_by": true, "date_field": true,
		"root": true, "rel": true, "filter": true, "layout": true}
	for k := range set {
		if !allowed[k] {
			return 0, fmt.Errorf("a view has no key %q", k)
		}
	}
	if v, ok := set["view"].(string); ok && !contains(ViewKinds, v) {
		return 0, fmt.Errorf("there is no view %q", v)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, err := s.space(ctx, ws)
	if err != nil {
		return 0, err
	}
	if id == "" {
		id = NewID()
	}
	if !ValidID(id) {
		return 0, errors.New("that is not a view id")
	}
	d, _ := ParsePlain("")
	if it, ok, err := s.fresh(ctx, ns, ViewName(id)); err != nil {
		return 0, err
	} else if ok {
		if d, err = ParsePlain(it.Content); err != nil {
			return 0, fmt.Errorf("the view cannot be read: %w", err)
		}
	} else if _, has := set["view"]; !has {
		return 0, errors.New("a new view says which view it is")
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if set[k] == nil {
			d.Del(k)
			continue
		}
		if err := d.Set(k, set[k]); err != nil {
			return 0, err
		}
	}
	title := d.Str("title")
	if title == "" {
		title = d.Str("view")
	}
	return s.write(ctx, ns, ViewName(id), d.String(), title)
}

// Log is an item's history.
func (s *Store) Log(ctx context.Context, ws, name string) ([]Revision, error) {
	if s.caster == nil {
		return nil, ErrNoCaster
	}
	ns, err := s.space(ctx, ws)
	if err != nil {
		return nil, err
	}
	return s.caster.Log(ctx, ns, name)
}

// At is an item at a revision.
func (s *Store) At(ctx context.Context, ws, name string, rev int) (Item, bool, error) {
	if s.caster == nil {
		return Item{}, false, ErrNoCaster
	}
	ns, err := s.space(ctx, ws)
	if err != nil {
		return Item{}, false, err
	}
	return s.caster.Get(ctx, ns, name, rev)
}

// ---- Notes ----

// Note is one piece of the owner's feedback on an item, not yet sent.
type Note struct {
	ID        string `json:"id"`
	WS        string `json:"ws"`
	Item      string `json:"item"`
	ItemRev   int    `json:"item_rev,omitempty"`
	Kind      string `json:"kind"`
	Section   string `json:"section,omitempty"`
	Quote     string `json:"quote,omitempty"`
	Replace   string `json:"replace,omitempty"`
	Text      string `json:"text,omitempty"`
	CreatedAt string `json:"created_at"`
}

func (s *Store) addNote(ctx context.Context, n Note) error {
	if n.ID == "" {
		n.ID = "n" + NewID()
	}
	if n.CreatedAt == "" {
		n.CreatedAt = now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO notes(id, ws, item, item_rev, kind, section, quote, replace, text, created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.WS, n.Item, n.ItemRev, n.Kind, n.Section, n.Quote, n.Replace, n.Text, n.CreatedAt)
	return err
}

// Notes are the owner's notes on an item, not yet sent.
func (s *Store) Notes(ctx context.Context, ws, item string) ([]Note, error) {
	rs, err := s.db.QueryContext(ctx, `SELECT id, ws, item, item_rev, kind, section, quote, replace, text, created_at FROM notes WHERE ws=? AND item=? AND round=0 ORDER BY created_at`, ws, item)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	out := []Note{}
	for rs.Next() {
		var n Note
		if err := rs.Scan(&n.ID, &n.WS, &n.Item, &n.ItemRev, &n.Kind, &n.Section, &n.Quote, &n.Replace, &n.Text, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rs.Err()
}

// DropNote takes a note out.
func (s *Store) DropNote(ctx context.Context, ws, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM notes WHERE ws=? AND id=? AND round=0`, ws, id)
	return err
}
