package work

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func yamlUnmarshal(s string, v any) error { return yaml.Unmarshal([]byte(s), v) }

// fakeCaster is Caster in memory: every put a revision, per namespace.
type fakeCaster struct {
	mu   sync.Mutex
	revs map[string][]string // ns/name -> contents, revision i+1 at i
	puts int
	// Torque: tasks made, and what each was made with.
	tasks int
	made  []map[string]any
}

func newFake() *fakeCaster { return &fakeCaster{revs: map[string][]string{}} }

func (f *fakeCaster) List(_ context.Context, ns string) ([]Head, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Head
	for k, v := range f.revs {
		if n, ok := strings.CutPrefix(k, ns+"/"); ok {
			out = append(out, Head{Name: n, Rev: len(v)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *fakeCaster) Get(_ context.Context, ns, name string, rev int) (Item, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.revs[ns+"/"+name]
	if len(v) == 0 {
		return Item{}, false, nil
	}
	if rev == 0 {
		rev = len(v)
	}
	if rev > len(v) {
		return Item{}, false, nil
	}
	return Item{NS: ns, Name: name, Rev: rev, Content: v[rev-1]}, true, nil
}

func (f *fakeCaster) Put(_ context.Context, ns, name, content, _ string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := ns + "/" + name
	v := f.revs[k]
	if len(v) > 0 && v[len(v)-1] == content {
		return len(v), nil
	}
	f.revs[k] = append(v, content)
	f.puts++
	return len(f.revs[k]), nil
}

func (f *fakeCaster) Log(_ context.Context, ns, name string) ([]Revision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.revs[ns+"/"+name]
	out := []Revision{}
	for i := len(v); i >= 1; i-- {
		out = append(out, Revision{Rev: i})
	}
	return out, nil
}

func (f *fakeCaster) latest(ns, name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.revs[ns+"/"+name]
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}

func open(t *testing.T) (*Store, *fakeCaster) {
	t.Helper()
	f := newFake()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "work.db"), f)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, f
}

func mkspace(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.NewWorkspace(context.Background(), "channel", "My channel", "content", "Videos and articles."); err != nil {
		t.Fatal(err)
	}
}

func TestNewWorkspaceLayout(t *testing.T) {
	ctx := context.Background()
	s, f := open(t)
	mkspace(t, s)
	idx := f.latest("work", "channel")
	for _, want := range []string{"work: v1", "workspace: channel", "app: content", "preset: content", "ns: w-channel", "owners: []"} {
		if !strings.Contains(idx, want) {
			t.Errorf("index lacks %q:\n%s", want, idx)
		}
	}
	if k := f.latest("w-channel", "kind.video"); !strings.Contains(k, "label: Video") || !strings.Contains(k, "- Script") {
		t.Errorf("kind.video:\n%s", k)
	}
	heads, _ := f.List(ctx, "w-channel")
	views := 0
	for _, h := range heads {
		if k, _ := ParseName(h.Name); k == NameView {
			views++
		}
	}
	if views != 2 {
		t.Errorf("views = %d, want 2 (board, calendar)", views)
	}
	if _, err := s.NewWorkspace(ctx, "channel", "Again", "content", ""); err == nil {
		t.Error("a second workspace of the same name was made")
	}
	list, _ := s.Workspaces(ctx, "content")
	if len(list) != 1 || list[0].Title != "My channel" {
		t.Fatalf("workspaces = %+v", list)
	}
	sp, ok, _ := s.Open(ctx, "channel")
	if !ok || len(sp.Kinds) != 7 || len(sp.Views) != 2 || sp.Views[0].GroupBy != "stage" ||
		sp.Views[1].View != "calendar" || sp.Views[1].DateFld != "publish_on" {
		t.Fatalf("space = %+v", sp)
	}
}

func TestItemPutByHandAppears(t *testing.T) {
	ctx := context.Background()
	s, f := open(t)
	mkspace(t, s)
	_, _ = f.Put(ctx, "w-channel", "i.hand0001", "---\nwork: v1\nid: hand0001\nkind: article\ntitle: Written by hand\nstage: draft\n---\n## Summary\nHi there.\n", "")
	if _, err := s.Refresh(ctx, "w-channel", true); err != nil {
		t.Fatal(err)
	}
	sp, _, _ := s.Open(ctx, "channel")
	if len(sp.Items) != 1 || sp.Items[0].Title != "Written by hand" || sp.Items[0].Stage != "draft" || sp.Items[0].Excerpt != "Hi there." {
		t.Fatalf("items = %+v", sp.Items)
	}
}

func TestMoveChangesOnlyStage(t *testing.T) {
	ctx := context.Background()
	s, f := open(t)
	mkspace(t, s)
	id, err := s.MakeItem(ctx, "channel", NewItem{Kind: "video", Title: "Launch"})
	if err != nil {
		t.Fatal(err)
	}
	// The team adds a key the app does not know.
	before := f.latest("w-channel", ItemName(id))
	withKey := strings.Replace(before, "stage: idea\n", "stage: idea\nmood: upbeat\n", 1)
	_, _ = f.Put(ctx, "w-channel", ItemName(id), withKey, "")
	if _, err := s.Move(ctx, "channel", id, "stage", "draft"); err != nil {
		t.Fatal(err)
	}
	after := f.latest("w-channel", ItemName(id))
	if strings.Replace(withKey, "stage: idea", "stage: draft", 1) != after {
		t.Fatalf("more than the stage changed:\n--- before\n%s\n--- after\n%s", withKey, after)
	}
	if _, err := s.Move(ctx, "channel", id, "stage", "nonsense"); err == nil {
		t.Error("an unknown stage was taken")
	}
	if _, err := s.Move(ctx, "channel", id, "publish_on", "2026-12-01"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.latest("w-channel", ItemName(id)), "publish_on: \"2026-12-01\"") && !strings.Contains(f.latest("w-channel", ItemName(id)), "publish_on: 2026-12-01") {
		t.Errorf("date not set:\n%s", f.latest("w-channel", ItemName(id)))
	}
}

func TestEditAndConflict(t *testing.T) {
	ctx := context.Background()
	s, f := open(t)
	mkspace(t, s)
	id, _ := s.MakeItem(ctx, "channel", NewItem{Kind: "article", Title: "Essay"})
	d, _, _ := s.Detail(ctx, "channel", id)
	title := "Essay, retitled"
	rev, err := s.EditItem(ctx, "channel", id, Edit{BaseRev: d.Rev, Title: &title,
		Sections: map[string]string{"Draft": "First words.", "Summary": "What it says."},
		Fields:   map[string]any{"format": "long read"}})
	if err != nil {
		t.Fatal(err)
	}
	got := f.latest("w-channel", ItemName(id))
	if !strings.Contains(got, "title: Essay, retitled") || !strings.Contains(got, "format: long read") ||
		strings.Index(got, "## Summary\nWhat it says.") > strings.Index(got, "## Draft\nFirst words.") {
		t.Fatalf("edit:\n%s", got)
	}
	// The team writes a revision; the owner's edit on the old one is not written over it.
	team := strings.Replace(got, "First words.", "The team's words.", 1)
	_, _ = f.Put(ctx, "w-channel", ItemName(id), team, "")
	_, err = s.EditItem(ctx, "channel", id, Edit{BaseRev: rev, Sections: map[string]string{"Draft": "My words."}})
	var c *Conflict
	if !errors.As(err, &c) || c.Suggestions != 1 {
		t.Fatalf("err = %v", err)
	}
	if f.latest("w-channel", ItemName(id)) != team {
		t.Fatal("the team's revision was written over")
	}
	notes, _ := s.Notes(ctx, "channel", id)
	if len(notes) != 1 || notes[0].Quote != "The team's words." || notes[0].Replace != "My words." {
		t.Fatalf("notes = %+v", notes)
	}
	if _, err := s.EditItem(ctx, "channel", id, Edit{BaseRev: c.Rev, Fields: map[string]any{"nope": 1}}); err == nil {
		t.Error("an unknown field was taken")
	}
}

func TestParentCutRestore(t *testing.T) {
	ctx := context.Background()
	s, f := open(t)
	mkspace(t, s)
	series, _ := s.MakeItem(ctx, "channel", NewItem{Kind: "series", Title: "Series"})
	a, err := s.MakeItem(ctx, "channel", NewItem{Kind: "video", Title: "Ep 1", Parent: series})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.MakeItem(ctx, "channel", NewItem{Kind: "video", Title: "Ep 2", Parent: series})
	if _, err := s.MakeItem(ctx, "channel", NewItem{Kind: "channel", Title: "x", Parent: series}); err == nil {
		t.Error("a series took a channel")
	}
	children := func() string {
		d, _ := ParseDoc(f.latest("w-channel", ItemName(series)))
		return strings.Join(d.Strings("children"), ",")
	}
	if children() != a+","+b {
		t.Fatalf("children = %s", children())
	}
	if err := s.Cut(ctx, "channel", a); err != nil {
		t.Fatal(err)
	}
	if children() != b || !strings.Contains(f.latest("w-channel", ItemName(a)), "cut: true") {
		t.Fatalf("after cut: %s", children())
	}
	if err := s.Restore(ctx, "channel", a); err != nil {
		t.Fatal(err)
	}
	if children() != b+","+a || strings.Contains(f.latest("w-channel", ItemName(a)), "cut:") {
		t.Fatalf("after restore: %s", children())
	}
}

func TestSaveViewKeepsUnknownKeys(t *testing.T) {
	ctx := context.Background()
	s, f := open(t)
	mkspace(t, s)
	sp, _, _ := s.Open(ctx, "channel")
	v := sp.Views[0]
	cur := f.latest("w-channel", ViewName(v.ID))
	_, _ = f.Put(ctx, "w-channel", ViewName(v.ID), cur+"colour: green\n", "")
	if _, err := s.SaveView(ctx, "channel", v.ID, map[string]any{"kinds": []string{"video"}}); err != nil {
		t.Fatal(err)
	}
	got := f.latest("w-channel", ViewName(v.ID))
	if !strings.Contains(got, "colour: green") || !strings.Contains(got, "kinds: [video]") || strings.HasPrefix(got, "---") {
		t.Fatalf("view:\n%s", got)
	}
}

// A 300-item workspace opens from the copy well under 2 s.
func TestOpenThreeHundred(t *testing.T) {
	ctx := context.Background()
	s, f := open(t)
	mkspace(t, s)
	for i := range 300 {
		id := fmt.Sprintf("x%07d", i)
		_, _ = f.Put(ctx, "w-channel", ItemName(id), fmt.Sprintf("---\nwork: v1\nid: %s\nkind: article\ntitle: Item %d\nstage: draft\nfields: {publish_on: \"2026-12-01\"}\n---\n## Summary\n%s\n## Draft\n%s\n", id, i, strings.Repeat("word ", 50), strings.Repeat("text ", 800)), "")
	}
	if _, err := s.Refresh(ctx, "w-channel", true); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	sp, _, err := s.Open(ctx, "channel")
	took := time.Since(start)
	if err != nil || len(sp.Items) != 300 {
		t.Fatalf("items = %d, err = %v", len(sp.Items), err)
	}
	t.Logf("opened 300 items in %v", took)
	if took > 2*time.Second {
		t.Fatalf("took %v", took)
	}
}

func TestCalendarMoveAndPublicationLog(t *testing.T) {
	ctx := context.Background()
	s, f := open(t)
	mkspace(t, s)
	ch, err := s.MakeItem(ctx, "channel", NewItem{Kind: "channel", Title: "YouTube"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.MakeItem(ctx, "channel", NewItem{Kind: "video", Title: "Launch", Fields: map[string]any{"channel": ch}})
	if err != nil {
		t.Fatal(err)
	}
	// A calendar drag: the date field, nothing else.
	before := f.latest("w-channel", ItemName(id))
	if _, err := s.Move(ctx, "channel", id, "publish_on", "2026-11-02"); err != nil {
		t.Fatal(err)
	}
	after := f.latest("w-channel", ItemName(id))
	if !strings.Contains(after, "  publish_on: \"2026-11-02\"\n") || strings.Replace(after, "  publish_on: \"2026-11-02\"\n", "", 1) != before {
		t.Errorf("calendar move changed more than the date:\n%s\n---\n%s", before, after)
	}
	// Dropped on "no date": the field goes.
	if _, err := s.Move(ctx, "channel", id, "publish_on", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.latest("w-channel", ItemName(id)), "publish_on") {
		t.Error("the date was not taken out")
	}
	if _, err := s.Move(ctx, "channel", id, "publish_on", "2026-11-02"); err != nil {
		t.Fatal(err)
	}
	// Reaching "published" starts the log: when (the past publish_on) and where (the channel).
	if _, err := s.Move(ctx, "channel", id, "stage", "published"); err != nil {
		t.Fatal(err)
	}
	d, _, _ := s.Detail(ctx, "channel", id)
	if d.Stage != "published" || len(d.Published) != 1 || d.Published[0].At != "2026-11-02" || d.Published[0].Where != "YouTube" {
		t.Fatalf("published = %+v (stage %s)", d.Published, d.Stage)
	}
	// The owner corrects it and adds a second place; moving away and back adds nothing.
	log := []Published{{At: "2026-11-02", Where: "YouTube", URL: "https://example.com/v/1"}, {At: "2026-11-03T09:00:00Z", Where: "Blog", Note: "cross-post"}}
	mat := []Material{{Ref: "tach:fragment/" + strings.Repeat("ab", 32), Caption: "Research"}, {Ref: "caster://story/demo.teaser"}}
	if _, err := s.EditItem(ctx, "channel", id, Edit{BaseRev: d.Rev, Published: &log, Material: &mat}); err != nil {
		t.Fatal(err)
	}
	_, _ = s.Move(ctx, "channel", id, "stage", "review")
	_, _ = s.Move(ctx, "channel", id, "stage", "published")
	d, _, _ = s.Detail(ctx, "channel", id)
	if len(d.Published) != 2 || d.Published[1].Where != "Blog" || len(d.Material) != 2 || d.Material[0].Caption != "Research" {
		t.Fatalf("published %+v material %+v", d.Published, d.Material)
	}
	// Bad entries are refused, and nothing is written.
	puts := f.puts
	for _, bad := range []Edit{
		{BaseRev: d.Rev, Published: &[]Published{{At: "last week", Where: "YouTube"}}},
		{BaseRev: d.Rev, Published: &[]Published{{At: "2026-11-02", Where: "x", URL: "javascript:alert(1)"}}},
		{BaseRev: d.Rev, Material: &[]Material{{Ref: "two\nlines"}}},
		{BaseRev: d.Rev, Stage: ptr("nonsense")},
	} {
		if _, err := s.EditItem(ctx, "channel", id, bad); err == nil {
			t.Errorf("taken: %+v", bad)
		}
	}
	if f.puts != puts {
		t.Error("a refused edit was written")
	}
	// A stale log edit becomes a comment, not a write.
	stale := []Published{}
	_, err = s.EditItem(ctx, "channel", id, Edit{BaseRev: 1, Published: &stale})
	var c *Conflict
	if !errors.As(err, &c) {
		t.Fatalf("stale edit: %v", err)
	}
	if d2, _, _ := s.Detail(ctx, "channel", id); len(d2.Published) != 2 {
		t.Error("a stale edit emptied the log")
	}
}

func ptr[T any](v T) *T { return &v }

func TestSetFieldKeepsOneLineMap(t *testing.T) {
	d, err := ParseDoc("---\nwork: v1\nfields: {\"publish_on\": \"2026-11-24\", \"format\": \"post\"}\n---\nBody\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetField("publish_on", "2026-11-26"); err != nil {
		t.Fatal(err)
	}
	want := "fields: {\"publish_on\": \"2026-11-26\", \"format\": \"post\"}\n"
	if !strings.Contains(d.String(), want) {
		t.Fatalf("got:\n%s", d.String())
	}
	if err := d.SetField("format", nil); err != nil {
		t.Fatal(err)
	}
	if err := d.SetField("publish_on", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.String(), "fields: {}\n") {
		t.Fatalf("emptied:\n%s", d.String())
	}
	if err := d.SetField("length", "9 min"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.String(), "fields:\n    length: 9 min\n") && !strings.Contains(d.String(), "fields:\n  length: 9 min\n") {
		t.Fatalf("opened:\n%s", d.String())
	}
}

func TestWriterOutlinePlace(t *testing.T) {
	ctx := context.Background()
	s, f := open(t)
	if _, err := s.NewWorkspace(ctx, "novel", "The novel", "writer", ""); err != nil {
		t.Fatal(err)
	}
	sp, _, _ := s.Open(ctx, "novel")
	if len(sp.Kinds) != 10 || sp.Kinds[0].Kind != "part" {
		t.Fatalf("kinds = %d, first %s", len(sp.Kinds), sp.Kinds[0].Kind)
	}
	var outline string
	for _, v := range sp.Views {
		if v.View == "outline" {
			outline = v.ID
		}
	}
	if outline == "" || sp.Default != outline {
		t.Fatalf("default view %q, outline %q", sp.Default, outline)
	}
	mk := func(kind, title, parent string) string {
		t.Helper()
		id, err := s.MakeItem(ctx, "novel", NewItem{Kind: kind, Title: title, Parent: parent})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	ch1, ch2 := mk("chapter", "One", ""), mk("chapter", "Two", "")
	a, b, c := mk("scene", "A", ch1), mk("scene", "B", ch1), mk("scene", "C", ch2)
	kids := func(id string) []string {
		d, _ := ParseDoc(f.latest("w-novel", ItemName(id)))
		return d.Strings("children")
	}
	parentOf := func(id string) string {
		d, _ := ParseDoc(f.latest("w-novel", ItemName(id)))
		return d.Str("parent")
	}
	// B to chapter Two, before C: both lists and B's parent change.
	if err := s.Place(ctx, "novel", b, ch2, c, outline); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(kids(ch1)) != fmt.Sprint([]string{a}) || fmt.Sprint(kids(ch2)) != fmt.Sprint([]string{b, c}) || parentOf(b) != ch2 {
		t.Fatalf("one %v two %v parent %s", kids(ch1), kids(ch2), parentOf(b))
	}
	// Reorder within a parent: C first. B's parent is untouched (no new revision of B).
	revB := len(f.revs["w-novel/"+ItemName(b)])
	if err := s.Place(ctx, "novel", c, ch2, b, outline); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(kids(ch2)) != fmt.Sprint([]string{c, b}) || len(f.revs["w-novel/"+ItemName(b)]) != revB {
		t.Fatalf("two %v", kids(ch2))
	}
	// The kinds' rules: a chapter does not hold a chapter; nothing goes inside itself.
	if err := s.Place(ctx, "novel", ch1, ch2, "", outline); err == nil {
		t.Error("a chapter went into a chapter")
	}
	if err := s.Place(ctx, "novel", a, a, "", outline); err == nil {
		t.Error("an item went inside itself")
	}
	// The top level's order is in the outline view: Two before One.
	if err := s.Place(ctx, "novel", ch2, "", ch1, outline); err != nil {
		t.Fatal(err)
	}
	v := f.latest("w-novel", ViewName(outline))
	if !strings.Contains(v, "order:") || strings.Index(v, ch2) > strings.Index(v, ch1) {
		t.Fatalf("view:\n%s", v)
	}
	// A scene to the top level: out of its chapter, parent emptied.
	if err := s.Place(ctx, "novel", a, "", "", outline); err != nil {
		t.Fatal(err)
	}
	if len(kids(ch1)) != 0 || parentOf(a) != "" {
		t.Fatalf("one %v parent %q", kids(ch1), parentOf(a))
	}
}

func TestEditLinks(t *testing.T) {
	ctx := context.Background()
	s, _ := open(t)
	if _, err := s.NewWorkspace(ctx, "novel", "The novel", "writer", ""); err != nil {
		t.Fatal(err)
	}
	sc, _ := s.MakeItem(ctx, "novel", NewItem{Kind: "scene", Title: "Opening"})
	ch, _ := s.MakeItem(ctx, "novel", NewItem{Kind: "character", Title: "Mara"})
	d, _, _ := s.Detail(ctx, "novel", ch)
	links := []Link{{Rel: "appears-in", To: sc}, {Rel: "appears-in", To: sc}, {Rel: "relates", To: ch}}
	if _, err := s.EditItem(ctx, "novel", ch, Edit{BaseRev: d.Rev, Links: &links}); err != nil {
		t.Fatal(err)
	}
	d, _, _ = s.Detail(ctx, "novel", ch)
	if len(d.Links) != 1 || d.Links[0] != (Link{Rel: "appears-in", To: sc}) {
		t.Fatalf("links = %+v (duplicates and self-links dropped)", d.Links)
	}
	for _, bad := range [][]Link{{{Rel: "contains", To: sc}}, {{Rel: "Appears In", To: sc}}, {{Rel: "relates", To: "nope"}}} {
		if _, err := s.EditItem(ctx, "novel", ch, Edit{BaseRev: d.Rev, Links: &bad}); err == nil {
			t.Errorf("taken: %+v", bad)
		}
	}
}

func TestCanvasLayout(t *testing.T) {
	ctx := context.Background()
	s, f := open(t)
	if _, err := s.NewWorkspace(ctx, "novel", "The novel", "writer", ""); err != nil {
		t.Fatal(err)
	}
	sp, _, _ := s.Open(ctx, "novel")
	var canvas, outline string
	for _, v := range sp.Views {
		switch v.View {
		case "canvas":
			canvas = v.ID
		case "outline":
			outline = v.ID
		}
	}
	if canvas == "" {
		t.Fatal("the writer preset has no canvas")
	}
	// The outline's own layout key is kept by a canvas patch on another view, and by one on itself.
	a, b := "aaaaaaaa", "bbbbbbbb"
	if _, err := s.PatchLayout(ctx, "novel", canvas, LayoutPatch{Cards: map[string]*Pos{a: {X: 10.4, Y: 20}, b: {X: 300, Y: 20}}}); err != nil {
		t.Fatal(err)
	}
	st := &Stack{ID: "stack001", Title: "Act one", X: 600, Y: 40, Items: []string{a}, Bind: &Bind{Key: "stage", Value: "draft"}}
	if _, err := s.PatchLayout(ctx, "novel", canvas, LayoutPatch{Stacks: map[string]*Stack{"stack001": st},
		Notes: map[string]*CanvasNote{"note0001": {ID: "note0001", X: 5, Y: 400, Text: "Is the storm too early?"}}}); err != nil {
		t.Fatal(err)
	}
	v := f.latest("w-novel", ViewName(canvas))
	var lay struct {
		Layout struct {
			Cards  map[string]Pos `yaml:"cards"`
			Stacks []Stack        `yaml:"stacks"`
			Notes  []CanvasNote   `yaml:"notes"`
		} `yaml:"layout"`
	}
	if err := yamlUnmarshal(v, &lay); err != nil {
		t.Fatal(err)
	}
	if _, onCanvas := lay.Layout.Cards[a]; onCanvas || lay.Layout.Cards[b].X != 300 {
		t.Fatalf("a card in a stack stays on the canvas: %+v", lay.Layout.Cards)
	}
	if len(lay.Layout.Stacks) != 1 || lay.Layout.Stacks[0].Items[0] != a || lay.Layout.Stacks[0].Bind.Value != "draft" || len(lay.Layout.Notes) != 1 {
		t.Fatalf("layout:\n%s", v)
	}
	// Back to the canvas: it leaves the stack.
	if _, err := s.PatchLayout(ctx, "novel", canvas, LayoutPatch{Cards: map[string]*Pos{a: {X: 50, Y: 50}}}); err != nil {
		t.Fatal(err)
	}
	lay.Layout.Cards, lay.Layout.Stacks, lay.Layout.Notes = nil, nil, nil
	_ = yamlUnmarshal(f.latest("w-novel", ViewName(canvas)), &lay)
	if len(lay.Layout.Stacks[0].Items) != 0 || lay.Layout.Cards[a].X != 50 {
		t.Fatalf("after: %+v", lay.Layout)
	}
	// Taking things out, and refusing what is not a layout.
	if _, err := s.PatchLayout(ctx, "novel", canvas, LayoutPatch{Stacks: map[string]*Stack{"stack001": nil}, Notes: map[string]*CanvasNote{"note0001": nil}, Cards: map[string]*Pos{b: nil}}); err != nil {
		t.Fatal(err)
	}
	lay.Layout.Cards, lay.Layout.Stacks, lay.Layout.Notes = nil, nil, nil
	_ = yamlUnmarshal(f.latest("w-novel", ViewName(canvas)), &lay)
	if len(lay.Layout.Stacks) != 0 || len(lay.Layout.Notes) != 0 || len(lay.Layout.Cards) != 1 {
		t.Fatalf("after taking out: %+v", lay.Layout)
	}
	for _, bad := range []LayoutPatch{
		{},
		{Cards: map[string]*Pos{"x": {}}},
		{Cards: map[string]*Pos{a: {X: 2e7}}},
		{Stacks: map[string]*Stack{"stack002": {ID: "stack002", Bind: &Bind{Key: "kind", Value: "x"}}}},
		{Stacks: map[string]*Stack{"stack002": {ID: "other123"}}},
	} {
		if _, err := s.PatchLayout(ctx, "novel", canvas, bad); err == nil {
			t.Errorf("taken: %+v", bad)
		}
	}
	// The outline's order survives a patch to the outline view's layout too.
	ch, _ := s.MakeItem(ctx, "novel", NewItem{Kind: "chapter", Title: "One"})
	if err := s.Place(ctx, "novel", ch, "", "", outline); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchLayout(ctx, "novel", outline, LayoutPatch{Cards: map[string]*Pos{b: {X: 1, Y: 1}}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.latest("w-novel", ViewName(outline)), "order:") {
		t.Errorf("order lost:\n%s", f.latest("w-novel", ViewName(outline)))
	}
}

// Torque in the fake: tasks made and read through Call.
func (f *fakeCaster) Call(_ context.Context, tool string, args map[string]any) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch tool {
	case "torque_task_create":
		f.tasks++
		id := fmt.Sprintf("CW-TEST-%04d", f.tasks)
		f.made = append(f.made, args)
		return []byte(fmt.Sprintf(`{"data":{"ID":%q}}`, id)), nil
	case "torque_task_get":
		return []byte(fmt.Sprintf(`{"data":{"id":%q,"title":"t","status":"todo"}}`, args["id"])), nil
	}
	return nil, fmt.Errorf("no tool %s", tool)
}

func TestPlannerTemplateAndTorque(t *testing.T) {
	ctx := context.Background()
	s, f := open(t)
	if _, err := s.NewWorkspace(ctx, "launch", "Launch", "planner", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyTemplate(ctx, "launch", "software"); err != nil {
		t.Fatal(err)
	}
	sp, _, _ := s.Open(ctx, "launch")
	if len(sp.Items) != 14 || len(sp.Kinds) != 10 || len(sp.Views) != 5 {
		t.Fatalf("items %d kinds %d views %d", len(sp.Items), len(sp.Kinds), len(sp.Views))
	}
	by := map[string]Card{}
	for _, c := range sp.Items {
		by[c.Title] = c
	}
	plan := by["Version 1 plan"]
	if plan.Parent != by["Ship version 1"].ID || len(plan.Children) != 3 {
		t.Fatalf("plan %+v", plan)
	}
	if by["Discovery"].Stage != "active" || by["Pricing model"].Stage != "open" {
		t.Errorf("stages %s %s", by["Discovery"].Stage, by["Pricing model"].Stage)
	}
	core := by["The core workflow"]
	if len(core.Links) != 2 {
		t.Errorf("core links %+v", core.Links)
	}
	// Send a task: one Torque task, its id kept; a second send makes none.
	task := by["CI and deploys"].ID
	id, created, err := s.SendToTorque(ctx, "launch", task)
	if err != nil || !created || id != "CW-TEST-0001" {
		t.Fatalf("send: %s %v %v", id, created, err)
	}
	if !strings.Contains(f.latest("w-launch", ItemName(task)), "task: CW-TEST-0001") {
		t.Errorf("id not kept:\n%s", f.latest("w-launch", ItemName(task)))
	}
	if !strings.Contains(fmt.Sprint(f.made[0]["description"]), "caster://w-launch/i."+task) {
		t.Errorf("description %v", f.made[0]["description"])
	}
	id2, created2, err := s.SendToTorque(ctx, "launch", task)
	if err != nil || created2 || id2 != id || f.tasks != 1 {
		t.Fatalf("second send: %s %v %v (made %d)", id2, created2, err, f.tasks)
	}
	if _, _, err := s.SendToTorque(ctx, "launch", by["Ship version 1"].ID); err == nil {
		t.Error("a goal was sent to Torque")
	}
	st, err := s.TorqueStatus(ctx, id)
	if err != nil || st.Status != "todo" {
		t.Errorf("status %+v %v", st, err)
	}
	if err := s.ApplyTemplate(ctx, "launch", "nope"); err == nil {
		t.Error("an unknown template was taken")
	}
}

func TestPlannerPortfolioExternalLinks(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	if _, err := s.NewWorkspace(ctx, "portfolio", "Portfolio", "planner", ""); err != nil {
		t.Fatal(err)
	}
	plan, err := s.MakeItem(ctx, "portfolio", NewItem{Kind: "plan", Title: "Access"})
	if err != nil {
		t.Fatal(err)
	}
	refs := []Link{{Rel: "relates", To: "torque://task/CW-20261114-0029"}, {Rel: "relates", To: "tesseract://item/example"}, {Rel: "uses", To: "caster://work/brief"}, {Rel: "relates", To: "https://example.com/plan"}}
	id, err := s.MakeItem(ctx, "portfolio", NewItem{Kind: "task", Title: "Write tools", Parent: plan, Links: refs, Sections: map[string]string{"Summary": "Initial text"}, Tags: []string{"access"}})
	if err != nil {
		t.Fatal(err)
	}
	d, ok, err := s.Detail(ctx, "portfolio", id)
	if err != nil || !ok || d.Parent != plan || len(d.Links) != 4 || d.Sections["Summary"] != "Initial text" {
		t.Fatalf("detail: %+v, %v", d, err)
	}
	_, err = s.EditItem(ctx, "portfolio", id, Edit{BaseRev: d.Rev, Sections: map[string]string{"Summary": "Revised"}, Links: &refs})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateWorkspace(ctx, "portfolio", ptrTitle("Our portfolio"), nil, nil); err != nil {
		t.Fatal(err)
	}
	removed := true
	if err := s.UpdateWorkspace(ctx, "portfolio", nil, nil, &removed); err != nil {
		t.Fatal(err)
	}
	list, err := s.Workspaces(ctx, "planner")
	if err != nil || len(list) != 0 {
		t.Fatalf("removed workspace still listed: %+v %v", list, err)
	}
	if _, ok, err := s.Detail(ctx, "portfolio", id); err != nil || !ok {
		t.Fatalf("removal lost item history: %v", err)
	}
}
func ptrTitle(s string) *string { return &s }

func TestExternalLinksRejectUnsafeTargets(t *testing.T) {
	for _, target := range []string{"javascript:alert(1)", "file:///etc/passwd", "https://example.com/\nother", "torque://"} {
		links := []Link{{Rel: "relates", To: target}}
		if err := (Edit{Links: &links}).check(); err == nil {
			t.Fatalf("accepted %q", target)
		}
	}
}
