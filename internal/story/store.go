package story

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // the driver, as the rest of the app uses it
)

// Store is the working copy of the story namespace (story.db, beside the app's database): a
// mirror of the Caster items for quick boards and reading when Caster is away, and the
// owner's notes not yet sent in a round. Nothing lives only here once sent.
//
// Writes go to Caster at once (the owner is waiting to see them land), each after reading
// the item again: Caster's put has no "only if still at revision N", so the manifest (the
// contended item: order, stage) is re-read and the change applied to what is there, and a
// scene edit begun on an older revision does not overwrite the newer one; the owner's text
// is kept as a suggestion on it instead.
type Store struct {
	db     *sql.DB
	caster Caster

	mu        sync.Mutex // one write or refresh at a time
	refreshed time.Time
	lastErr   string
}

var migrations = []string{
	`CREATE TABLE items (
		name TEXT PRIMARY KEY,        -- in the story namespace: <project>.<script>[.<scene>|.round-<n>]
		rev INTEGER NOT NULL,
		title TEXT NOT NULL DEFAULT '',
		content TEXT NOT NULL,
		updated TEXT NOT NULL DEFAULT '', -- Caster's time of that revision
		fetched_at TEXT NOT NULL
	);
	CREATE TABLE notes (
		id TEXT PRIMARY KEY,
		key TEXT NOT NULL,              -- the script
		scene TEXT NOT NULL DEFAULT '',
		scene_rev INTEGER NOT NULL DEFAULT 0,
		kind TEXT NOT NULL,             -- comment, suggestion, move, cut, add
		section TEXT NOT NULL DEFAULT '',
		quote TEXT NOT NULL DEFAULT '',
		replace TEXT NOT NULL DEFAULT '',
		text TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		round INTEGER NOT NULL DEFAULT 0 -- 0: in the draft round
	);
	CREATE INDEX notes_key ON notes(key, round);`,
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
		return nil, fmt.Errorf("story: %s was written by a newer Basalisk", path)
	}
	for i := have; i < len(migrations); i++ {
		if _, err := db.ExecContext(ctx, migrations[i]); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("story: migration %d: %w", i+1, err)
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", i+1)); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return &Store{db: db, caster: caster}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// ErrNoCaster: no gateway is configured, so nothing can be written.
var ErrNoCaster = errors.New("Caster is not reachable from this Basalisk (no Tether gateway)")

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// ---- Read-back ----

// Refresh brings the copy up to date with Caster: one listing, and a fetch for each item
// whose revision moved. At most every 15 s unless forced. It reports how many items changed.
func (s *Store) Refresh(ctx context.Context, force bool) (int, error) {
	if s.caster == nil {
		return 0, ErrNoCaster
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !force && time.Since(s.refreshed) < 15*time.Second {
		if s.lastErr != "" {
			return 0, errors.New(s.lastErr)
		}
		return 0, nil
	}
	n, err := s.refreshLocked(ctx)
	s.refreshed = time.Now()
	s.lastErr = ""
	if err != nil {
		s.lastErr = err.Error()
	}
	return n, err
}

func (s *Store) refreshLocked(ctx context.Context) (int, error) {
	heads, err := s.caster.List(ctx)
	if err != nil {
		return 0, fmt.Errorf("Caster could not be listed: %w", err)
	}
	have := map[string]int{}
	rows, err := s.db.QueryContext(ctx, `SELECT name, rev FROM items`)
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
		if ParseName(h.Name).Kind == KindOther || have[h.Name] == h.Rev {
			continue
		}
		it, ok, err := s.caster.Get(ctx, h.Name, 0)
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

// keep puts an item's revision in the copy (never an older one over a newer).
func (s *Store) keep(ctx context.Context, it Item, updated string) error {
	if updated == "" {
		updated = now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO items(name, rev, title, content, updated, fetched_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET rev=excluded.rev, title=excluded.title, content=excluded.content, updated=excluded.updated, fetched_at=excluded.fetched_at
		WHERE excluded.rev >= items.rev`, it.Name, it.Rev, it.Title, it.Content, updated, now())
	return err
}

// LastError is why the last read-back failed ("" when it did not).
func (s *Store) LastError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// ---- Views ----

// Card is a script on the board.
type Card struct {
	Key        string   `json:"key"`
	Project    string   `json:"project"`
	Script     string   `json:"script"`
	Title      string   `json:"title"`
	Stage      string   `json:"stage"`
	Round      int      `json:"round"`
	LengthS    float64  `json:"length_s,omitempty"`
	Platform   string   `json:"platform,omitempty"`
	Owners     []string `json:"owners,omitempty"`
	Task       string   `json:"task,omitempty"`
	Scenes     int      `json:"scenes"`
	DurationS  float64  `json:"duration_s"`
	DraftNotes int      `json:"draft_notes"`
	Next       string   `json:"next,omitempty"` // who has the next move
	Updated    string   `json:"updated"`
	Rev        int      `json:"rev"`
	Problem    string   `json:"problem,omitempty"` // the manifest could not be read
}

// Frame is a storyboard image, by reference.
type Frame struct {
	Ref     string `json:"ref" yaml:"ref"`
	Caption string `json:"caption,omitempty" yaml:"caption,omitempty"`
}

// Scene is a scene as the storyboard shows it.
type Scene struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Rev       int               `json:"rev"`
	Title     string            `json:"title"`
	Status    string            `json:"status"`
	DurationS float64           `json:"duration_s"`
	Sections  map[string]string `json:"sections"`
	Frames    []Frame           `json:"frames"`
	Words     int               `json:"words"` // narration words
	Notes     int               `json:"notes"` // draft notes on it
	Missing   bool              `json:"missing,omitempty"`
	Problem   string            `json:"problem,omitempty"`
	Updated   string            `json:"updated,omitempty"`
}

// Script is a script opened as a storyboard.
type Script struct {
	Card
	Body   string  `json:"body"`
	List   []Scene `json:"scene_list"`
	Unused []Scene `json:"unused"` // scenes in Caster that the manifest does not list (cut)
}

type row struct {
	name, title, content, updated string
	rev                           int
}

func (s *Store) rows(ctx context.Context, where string, args ...any) ([]row, error) {
	q := `SELECT name, rev, title, content, updated FROM items`
	if where != "" {
		q += " WHERE " + where
	}
	rs, err := s.db.QueryContext(ctx, q, args...)
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

func (s *Store) draftCounts(ctx context.Context) (map[string]int, map[string]int) {
	byKey, byScene := map[string]int{}, map[string]int{}
	rs, err := s.db.QueryContext(ctx, `SELECT key, scene FROM notes WHERE round=0`)
	if err != nil {
		return byKey, byScene
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		var k, sc string
		if rs.Scan(&k, &sc) == nil {
			byKey[k]++
			byScene[k+"."+sc]++
		}
	}
	return byKey, byScene
}

func nextMove(stage string) string {
	switch stage {
	case "review":
		return "waiting on you"
	case "revising", "idea", "outline", "draft":
		return "with the team"
	}
	return ""
}

func card(name Name, r row, doc *Doc, perr error) Card {
	c := Card{Key: name.Key(), Project: name.Project, Script: name.Script, Title: name.Script, Rev: r.rev, Updated: r.updated}
	if perr != nil {
		c.Problem = perr.Error()
		return c
	}
	if t := strings.TrimSpace(doc.Str("title")); t != "" {
		c.Title = t
	}
	c.Stage = doc.Str("stage")
	if c.Stage == "" {
		c.Stage = "idea"
	}
	c.Round = int(doc.Num("round"))
	c.LengthS = doc.Num("length_s")
	c.Platform = doc.Str("platform")
	c.Owners = doc.Strings("owners")
	c.Task = doc.Str("task")
	c.Scenes = len(doc.Strings("scenes"))
	c.Next = nextMove(c.Stage)
	return c
}

// Board is every script, newest change first.
func (s *Store) Board(ctx context.Context) ([]Card, error) {
	all, err := s.rows(ctx, "")
	if err != nil {
		return nil, err
	}
	drafts, _ := s.draftCounts(ctx)
	scenes := map[string][]row{}
	var manifests []row
	for _, r := range all {
		switch n := ParseName(r.name); n.Kind {
		case KindManifest:
			manifests = append(manifests, r)
		case KindScene:
			scenes[n.Key()] = append(scenes[n.Key()], r)
		}
	}
	cards := make([]Card, 0, len(manifests))
	for _, r := range manifests {
		n := ParseName(r.name)
		doc, perr := ParseDoc(r.content)
		c := card(n, r, doc, perr)
		c.DraftNotes = drafts[c.Key]
		listed := map[string]bool{}
		if perr == nil {
			for _, id := range doc.Strings("scenes") {
				listed[id] = true
			}
		}
		for _, sr := range scenes[c.Key] {
			if sr.updated > c.Updated {
				c.Updated = sr.updated
			}
			if listed[ParseName(sr.name).Scene] {
				if sd, err := ParseDoc(sr.content); err == nil {
					c.DurationS += sd.Num("duration_s")
				}
			}
		}
		cards = append(cards, c)
	}
	sort.SliceStable(cards, func(i, j int) bool { return cards[i].Updated > cards[j].Updated })
	return cards, nil
}

func sceneView(key string, r row, notes map[string]int) Scene {
	n := ParseName(r.name)
	sc := Scene{ID: n.Scene, Name: r.name, Rev: r.rev, Updated: r.updated, Sections: map[string]string{}, Frames: []Frame{}, Notes: notes[key+"."+n.Scene]}
	doc, err := ParseDoc(r.content)
	if err != nil {
		sc.Problem = err.Error()
		sc.Title = n.Scene
		return sc
	}
	sc.Title = strings.TrimSpace(doc.Str("title"))
	if sc.Title == "" {
		sc.Title = n.Scene
	}
	sc.Status = doc.Str("status")
	if sc.Status == "" {
		sc.Status = "draft"
	}
	sc.DurationS = doc.Num("duration_s")
	_ = doc.Decode("frames", &sc.Frames)
	if sc.Frames == nil {
		sc.Frames = []Frame{}
	}
	body := ParseSceneBody(doc.Body)
	for _, name := range Sections {
		sc.Sections[name] = body.Get(name)
	}
	sc.Words = len(strings.Fields(sc.Sections["Narration"]))
	return sc
}

// Script is one script as a storyboard: the scenes in the manifest's order, then those in
// Caster that it no longer lists. ok is false when there is no such script.
func (s *Store) Script(ctx context.Context, key string) (Script, bool, error) {
	all, err := s.rows(ctx, "name = ? OR name LIKE ?", key, key+".%")
	if err != nil {
		return Script{}, false, err
	}
	var manifest *row
	scenes := map[string]row{}
	for i, r := range all {
		n := ParseName(r.name)
		if n.Key() != key {
			continue
		}
		switch n.Kind {
		case KindManifest:
			manifest = &all[i]
		case KindScene:
			scenes[n.Scene] = r
		}
	}
	if manifest == nil {
		return Script{}, false, nil
	}
	_, perScene := s.draftCounts(ctx)
	drafts, _ := s.draftCounts(ctx)
	doc, perr := ParseDoc(manifest.content)
	out := Script{Card: card(ParseName(manifest.name), *manifest, doc, perr), List: []Scene{}, Unused: []Scene{}}
	out.DraftNotes = drafts[key]
	if perr != nil {
		return out, true, nil
	}
	out.Body = doc.Body
	listed := map[string]bool{}
	for _, id := range doc.Strings("scenes") {
		if listed[id] {
			continue
		}
		listed[id] = true
		r, ok := scenes[id]
		if !ok {
			out.List = append(out.List, Scene{ID: id, Name: SceneName(key, id), Title: id, Missing: true, Sections: map[string]string{}, Frames: []Frame{}})
			continue
		}
		sc := sceneView(key, r, perScene)
		out.DurationS += sc.DurationS
		out.List = append(out.List, sc)
	}
	for id, r := range scenes {
		if !listed[id] {
			out.Unused = append(out.Unused, sceneView(key, r, perScene))
		}
	}
	sort.Slice(out.Unused, func(i, j int) bool { return out.Unused[i].ID < out.Unused[j].ID })
	return out, true, nil
}

// ---- Writes ----

// write puts an item and keeps the new revision in the copy.
func (s *Store) write(ctx context.Context, name, content, title string) (int, error) {
	rev, err := s.caster.Put(ctx, name, content, title)
	if err != nil {
		return 0, fmt.Errorf("Caster did not take the change: %w", err)
	}
	return rev, s.keep(ctx, Item{Name: name, Rev: rev, Title: title, Content: content}, now())
}

// fresh reads an item from Caster as it is now, keeping it in the copy too.
func (s *Store) fresh(ctx context.Context, name string) (Item, bool, error) {
	it, ok, err := s.caster.Get(ctx, name, 0)
	if err != nil {
		return Item{}, false, fmt.Errorf("Caster could not be read: %w", err)
	}
	if ok {
		_ = s.keep(ctx, it, "")
	}
	return it, ok, nil
}

// changeManifest re-reads the manifest and applies change to it as it is now.
func (s *Store) changeManifest(ctx context.Context, key string, change func(d *Doc) error) error {
	if s.caster == nil {
		return ErrNoCaster
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok, err := s.fresh(ctx, ManifestName(key))
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	doc, err := ParseDoc(it.Content)
	if err != nil {
		return fmt.Errorf("the script's manifest cannot be read: %w", err)
	}
	if err := change(doc); err != nil {
		return err
	}
	title := doc.Str("title")
	if title == "" {
		title = key
	}
	_, err = s.write(ctx, ManifestName(key), doc.String(), title)
	return err
}

var ErrNotFound = errors.New("there is no such script")

// NewScript makes a manifest at stage idea. It is refused when the script exists.
func (s *Store) NewScript(ctx context.Context, project, script, title string) (string, error) {
	if s.caster == nil {
		return "", ErrNoCaster
	}
	if !ValidSlug(project) || !ValidSlug(script) {
		return "", errors.New("the project and script names take lowercase letters, digits and '-'")
	}
	key := project + "." + script
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok, err := s.fresh(ctx, ManifestName(key)); err != nil {
		return "", err
	} else if ok {
		return "", fmt.Errorf("there is already a script %s", key)
	}
	doc, _ := ParseDoc("")
	for _, kv := range []struct {
		k string
		v any
	}{{"story", "v1"}, {"project", project}, {"script", script}, {"title", title}, {"stage", "idea"}, {"round", 0}, {"scenes", []string{}}} {
		_ = doc.Set(kv.k, kv.v)
	}
	doc.Body = "Logline, audience, the one thing to remember, voice.\n"
	_, err := s.write(ctx, ManifestName(key), doc.String(), title)
	return key, err
}

// SetStage moves a script to a stage. approved is the owner's sign-off, which is its own step.
func (s *Store) SetStage(ctx context.Context, key, stage string) error {
	known := false
	for _, st := range Stages {
		known = known || st == stage
	}
	if !known {
		return fmt.Errorf("unknown stage %q", stage)
	}
	return s.changeManifest(ctx, key, func(d *Doc) error { return d.Set("stage", stage) })
}

// SetOrder puts the scenes in the order given. Scenes the team added since the owner looked
// keep their place at the end; ids no longer listed are dropped from the request.
func (s *Store) SetOrder(ctx context.Context, key string, order []string) error {
	return s.changeManifest(ctx, key, func(d *Doc) error {
		now := d.Strings("scenes")
		in := map[string]bool{}
		for _, id := range now {
			in[id] = true
		}
		seen := map[string]bool{}
		next := []string{}
		for _, id := range order {
			if in[id] && !seen[id] {
				next = append(next, id)
				seen[id] = true
			}
		}
		for _, id := range now {
			if !seen[id] {
				next = append(next, id)
			}
		}
		return d.Set("scenes", next)
	})
}

// newSceneID is the next sNN not listed and not already an item (cut scenes keep theirs).
func (s *Store) newSceneID(ctx context.Context, key string, listed []string) (string, error) {
	taken := map[string]bool{}
	for _, id := range listed {
		taken[id] = true
	}
	if s.caster != nil {
		if heads, err := s.caster.List(ctx); err == nil {
			for _, h := range heads {
				if n := ParseName(h.Name); n.Kind == KindScene && n.Key() == key {
					taken[n.Scene] = true
				}
			}
		} else {
			return "", fmt.Errorf("Caster could not be listed: %w", err)
		}
	}
	for i := 1; i < 1000; i++ {
		id := fmt.Sprintf("s%02d", i)
		if !taken[id] {
			return id, nil
		}
	}
	return "", errors.New("no scene id is free")
}

// AddScene makes a new scene after the one given (at the end when after is ""), or a copy
// of from when it is set. It returns the new scene's id.
func (s *Store) AddScene(ctx context.Context, key, after, from, title string) (string, error) {
	if s.caster == nil {
		return "", ErrNoCaster
	}
	s.mu.Lock()
	it, ok, err := s.fresh(ctx, ManifestName(key))
	if err != nil || !ok {
		s.mu.Unlock()
		if err == nil {
			err = ErrNotFound
		}
		return "", err
	}
	md, err := ParseDoc(it.Content)
	if err != nil {
		s.mu.Unlock()
		return "", err
	}
	id, err := s.newSceneID(ctx, key, md.Strings("scenes"))
	if err != nil {
		s.mu.Unlock()
		return "", err
	}
	var doc *Doc
	if from != "" {
		src, ok, err := s.fresh(ctx, SceneName(key, from))
		if err != nil || !ok {
			s.mu.Unlock()
			if err == nil {
				err = fmt.Errorf("there is no scene %s", from)
			}
			return "", err
		}
		if doc, err = ParseDoc(src.Content); err != nil {
			s.mu.Unlock()
			return "", err
		}
		if title == "" {
			title = strings.TrimSpace(doc.Str("title")) + " (copy)"
		}
		_ = doc.Set("status", "draft")
	} else {
		doc, _ = ParseDoc("")
		_ = doc.Set("story", "v1")
		_ = doc.Set("status", "draft")
		_ = doc.Set("duration_s", 0)
		var body SceneBody
		for _, sec := range Sections {
			body.Put(sec, "")
		}
		doc.Body = body.String()
	}
	if title == "" {
		title = "New scene"
	}
	_ = doc.Set("id", id)
	_ = doc.Set("title", title)
	if _, err := s.write(ctx, SceneName(key, id), doc.String(), title); err != nil {
		s.mu.Unlock()
		return "", err
	}
	s.mu.Unlock()
	return id, s.changeManifest(ctx, key, func(d *Doc) error {
		list := d.Strings("scenes")
		at := len(list)
		for i, v := range list {
			if v == after {
				at = i + 1
			}
		}
		list = append(list[:at], append([]string{id}, list[at:]...)...)
		return d.Set("scenes", list)
	})
}

// CutScene takes a scene out of the manifest's list; its item stays in Caster.
func (s *Store) CutScene(ctx context.Context, key, id string) error {
	return s.changeManifest(ctx, key, func(d *Doc) error {
		list := d.Strings("scenes")
		next := list[:0:0]
		for _, v := range list {
			if v != id {
				next = append(next, v)
			}
		}
		return d.Set("scenes", next)
	})
}

// RestoreScene puts a cut scene back, at the end.
func (s *Store) RestoreScene(ctx context.Context, key, id string) error {
	return s.changeManifest(ctx, key, func(d *Doc) error {
		list := d.Strings("scenes")
		for _, v := range list {
			if v == id {
				return nil
			}
		}
		return d.Set("scenes", append(list, id))
	})
}

// SceneEdit is the owner's change to a scene, made on the revision BaseRev. Nil fields are
// left alone.
type SceneEdit struct {
	BaseRev   int               `json:"base_rev"`
	Title     *string           `json:"title"`
	DurationS *float64          `json:"duration_s"`
	Sections  map[string]string `json:"sections"`
}

// Conflict: the scene moved on in Caster after the owner began the edit. Nothing was
// overwritten; the owner's changed sections were kept as suggestions on the newer revision.
type Conflict struct {
	Rev         int
	Suggestions int
}

func (c *Conflict) Error() string {
	return fmt.Sprintf("this scene was changed in Caster meanwhile (now revision %d); your text was kept as %d suggestion(s) on it, not written over it", c.Rev, c.Suggestions)
}

// EditScene writes the owner's edit as a new revision of the scene, unless the scene moved
// on since BaseRev (then *Conflict).
func (s *Store) EditScene(ctx context.Context, key, id string, e SceneEdit) (int, error) {
	if s.caster == nil {
		return 0, ErrNoCaster
	}
	for k := range e.Sections {
		known := false
		for _, sec := range Sections {
			known = known || sec == k
		}
		if !known {
			return 0, fmt.Errorf("unknown section %q", k)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	name := SceneName(key, id)
	it, ok, err := s.fresh(ctx, name)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("there is no scene %s", id)
	}
	doc, err := ParseDoc(it.Content)
	if err != nil {
		return 0, fmt.Errorf("the scene cannot be read: %w", err)
	}
	if it.Rev != e.BaseRev {
		// Keep the owner's words as suggestions against the newer text; never overwrite it.
		base, _, _ := s.caster.Get(ctx, name, e.BaseRev)
		baseBody := SceneBody{}
		if bd, err := ParseDoc(base.Content); err == nil {
			baseBody = ParseSceneBody(bd.Body)
		}
		nowBody := ParseSceneBody(doc.Body)
		n := 0
		for sec, text := range e.Sections {
			if strings.TrimSpace(text) == strings.TrimSpace(baseBody.Get(sec)) {
				continue // not changed by the owner
			}
			if err := s.addNote(ctx, Note{Key: key, Scene: id, SceneRev: it.Rev, Kind: "suggestion", Section: sec,
				Quote: nowBody.Get(sec), Replace: text,
				Text: fmt.Sprintf("My edit to %s, made on revision %d, which changed meanwhile.", sec, e.BaseRev)}); err != nil {
				return 0, err
			}
			n++
		}
		return 0, &Conflict{Rev: it.Rev, Suggestions: n}
	}
	if e.Title != nil {
		_ = doc.Set("title", strings.TrimSpace(*e.Title))
	}
	if e.DurationS != nil {
		_ = doc.Set("duration_s", *e.DurationS)
	}
	if len(e.Sections) > 0 {
		body := ParseSceneBody(doc.Body)
		for _, sec := range Sections {
			if text, ok := e.Sections[sec]; ok {
				body.Put(sec, text)
			}
		}
		doc.Body = body.String()
	}
	title := doc.Str("title")
	if title == "" {
		title = id
	}
	return s.write(ctx, name, doc.String(), title)
}

// Log is a scene's (or any item's) history.
func (s *Store) Log(ctx context.Context, name string) ([]Revision, error) {
	if s.caster == nil {
		return nil, ErrNoCaster
	}
	return s.caster.Log(ctx, name)
}

// At is an item at a revision.
func (s *Store) At(ctx context.Context, name string, rev int) (Item, bool, error) {
	if s.caster == nil {
		return Item{}, false, ErrNoCaster
	}
	return s.caster.Get(ctx, name, rev)
}

// ---- Notes (the draft round) ----

// Note is one piece of the owner's feedback.
type Note struct {
	ID        string `json:"id"`
	Key       string `json:"key"`
	Scene     string `json:"scene,omitempty"`
	SceneRev  int    `json:"scene_rev,omitempty"`
	Kind      string `json:"kind"`
	Section   string `json:"section,omitempty"`
	Quote     string `json:"quote,omitempty"`
	Replace   string `json:"replace,omitempty"`
	Text      string `json:"text,omitempty"`
	CreatedAt string `json:"created_at"`
	Round     int    `json:"round,omitempty"`
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	const abc = "abcdefghijkmnopqrstuvwxyz23456789"
	out := []byte(prefix)
	for _, c := range b {
		out = append(out, abc[int(c)%len(abc)])
	}
	return string(out)
}

func (s *Store) addNote(ctx context.Context, n Note) error {
	if n.ID == "" {
		n.ID = newID("n")
	}
	if n.CreatedAt == "" {
		n.CreatedAt = now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO notes(id, key, scene, scene_rev, kind, section, quote, replace, text, created_at, round) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.Key, n.Scene, n.SceneRev, n.Kind, n.Section, n.Quote, n.Replace, n.Text, n.CreatedAt, n.Round)
	return err
}

// Drafts are the notes not yet sent for a script.
func (s *Store) Drafts(ctx context.Context, key string) ([]Note, error) {
	rs, err := s.db.QueryContext(ctx, `SELECT id, key, scene, scene_rev, kind, section, quote, replace, text, created_at, round FROM notes WHERE key=? AND round=0 ORDER BY created_at`, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	out := []Note{}
	for rs.Next() {
		var n Note
		if err := rs.Scan(&n.ID, &n.Key, &n.Scene, &n.SceneRev, &n.Kind, &n.Section, &n.Quote, &n.Replace, &n.Text, &n.CreatedAt, &n.Round); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rs.Err()
}

// Spoken is the narration's length at 150 words a minute, in seconds.
func Spoken(words int) float64 { return float64(words) * 60 / 150 }

// atoi for route values.
func Atoi(s string) int { v, _ := strconv.Atoi(s); return v }
