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
)

// fakeCaster is Caster in memory: every put a revision, per namespace.
type fakeCaster struct {
	mu   sync.Mutex
	revs map[string][]string // ns/name -> contents, revision i+1 at i
	puts int
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
