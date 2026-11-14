package story

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeCaster is Caster in memory: named items with revisions, a new one only when the
// content changes (as Caster does).
type fakeCaster struct {
	mu    sync.Mutex
	items map[string][]Item // name -> revisions, oldest first
	puts  int
	down  bool
}

func newFake() *fakeCaster { return &fakeCaster{items: map[string][]Item{}} }

func (f *fakeCaster) List(context.Context) ([]Head, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return nil, errors.New("gateway down")
	}
	var out []Head
	for name, revs := range f.items {
		last := revs[len(revs)-1]
		out = append(out, Head{Name: name, Rev: last.Rev, Title: last.Title})
	}
	return out, nil
}

func (f *fakeCaster) Get(_ context.Context, name string, rev int) (Item, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return Item{}, false, errors.New("gateway down")
	}
	revs := f.items[name]
	if len(revs) == 0 {
		return Item{}, false, nil
	}
	if rev == 0 {
		return revs[len(revs)-1], true, nil
	}
	for _, it := range revs {
		if it.Rev == rev {
			return it, true, nil
		}
	}
	return Item{}, false, nil
}

func (f *fakeCaster) Put(_ context.Context, name, content, title string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return 0, errors.New("gateway down")
	}
	f.puts++
	revs := f.items[name]
	if n := len(revs); n > 0 && revs[n-1].Content == content {
		return revs[n-1].Rev, nil
	}
	it := Item{Name: name, Rev: len(revs) + 1, Title: title, Content: content}
	f.items[name] = append(revs, it)
	return it.Rev, nil
}

func (f *fakeCaster) Log(_ context.Context, name string) ([]Revision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Revision
	revs := f.items[name]
	for i := len(revs) - 1; i >= 0; i-- {
		out = append(out, Revision{Rev: revs[i].Rev, Title: revs[i].Title})
	}
	return out, nil
}

// by hand, as the team (or an agent) would put them.
func (f *fakeCaster) team(name, content string) {
	_, _ = f.Put(context.Background(), name, content, "")
}

const manifest = `---
story: v1
project: qspace
script: launch-teaser
title: QSpace launch teaser
stage: review
round: 1
length_s: 45
owners: ["msg://agent/agent-mux/agt_ndfltf559l"]
custom_field: keep me
scenes: [s01, s02, s03]
approved: {}
---
Logline: a short teaser.
`

func scene(id, title, narration string) string {
	return "---\nstory: v1\nid: " + id + "\ntitle: " + title + "\nstatus: draft\nduration_s: 6\nframes:\n  - {ref: \"design-asset:abc\", caption: wide}\n---\n## Visual\nA hangar.\n\n## On-screen text\nQSPACE\n\n## Narration\n" + narration + "\n\n## Notes\nnone\n## Extra\nkept as is\n"
}

func seeded(t *testing.T) (*Store, *fakeCaster) {
	t.Helper()
	f := newFake()
	f.team("qspace.launch-teaser", manifest)
	f.team("qspace.launch-teaser.s01", scene("s01", "Open", "Every launch starts somewhere."))
	f.team("qspace.launch-teaser.s02", scene("s02", "The claim", "This one starts in space."))
	f.team("qspace.launch-teaser.s03", scene("s03", "Close", "QSpace. Coming soon."))
	f.team("other-thing", "not a story item")
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "story.db"), f)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.Refresh(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	return s, f
}

func TestParseName(t *testing.T) {
	for in, want := range map[string]Name{
		"qspace.launch-teaser":         {Project: "qspace", Script: "launch-teaser", Kind: KindManifest},
		"qspace.launch-teaser.s02":     {Project: "qspace", Script: "launch-teaser", Scene: "s02", Kind: KindScene},
		"qspace.launch-teaser.round-3": {Project: "qspace", Script: "launch-teaser", Round: 3, Kind: KindRound},
		"QSpace.x":                     {},
		"one":                          {},
		"a.b.c.d":                      {},
		"a.b.round-0":                  {},
	} {
		if got := ParseName(in); got != want {
			t.Errorf("%s: %+v", in, got)
		}
	}
}

func TestDocKeepsWhatItDoesNotKnow(t *testing.T) {
	d, err := ParseDoc(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if d.Str("title") != "QSpace launch teaser" || d.Num("length_s") != 45 || strings.Join(d.Strings("scenes"), ",") != "s01,s02,s03" || len(d.Strings("owners")) != 1 {
		t.Fatalf("read: %q %v %v", d.Str("title"), d.Num("length_s"), d.Strings("scenes"))
	}
	_ = d.Set("scenes", []string{"s02", "s01", "s03"})
	_ = d.Set("stage", "revising")
	out := d.String()
	for _, want := range []string{"custom_field: keep me", "scenes: [s02, s01, s03]", "stage: revising", "Logline: a short teaser.", "approved: {}"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	again, err := ParseDoc(out)
	if err != nil || again.Str("custom_field") != "keep me" || again.Body != "Logline: a short teaser.\n" {
		t.Fatalf("round trip: %v\n%s", err, out)
	}
}

func TestSceneBody(t *testing.T) {
	d, _ := ParseDoc(scene("s01", "Open", "Every launch."))
	b := ParseSceneBody(d.Body)
	if b.Get("Narration") != "Every launch." || b.Get("On-screen text") != "QSPACE" || b.Get("Visual") != "A hangar." {
		t.Fatalf("%+v", b)
	}
	b.Put("Narration", "Every launch starts\nsomewhere.")
	out := b.String()
	if !strings.Contains(out, "## Narration\nEvery launch starts\nsomewhere.\n") || !strings.Contains(out, "## Extra\nkept as is") {
		t.Fatalf("%s", out)
	}
	// A section the body lacks goes in the team's order.
	var nb SceneBody
	nb.Put("Narration", "n")
	nb.Put("Visual", "v")
	if got := nb.String(); strings.Index(got, "## Visual") > strings.Index(got, "## Narration") {
		t.Fatalf("order: %s", got)
	}
}

func TestBoardAndStoryboard(t *testing.T) {
	s, _ := seeded(t)
	cards, err := s.Board(context.Background())
	if err != nil || len(cards) != 1 {
		t.Fatal(err, cards)
	}
	c := cards[0]
	if c.Key != "qspace.launch-teaser" || c.Title != "QSpace launch teaser" || c.Stage != "review" || c.Scenes != 3 || c.DurationS != 18 || c.Next != "waiting on you" || c.LengthS != 45 {
		t.Fatalf("%+v", c)
	}
	sc, ok, err := s.Script(context.Background(), c.Key)
	if err != nil || !ok || len(sc.List) != 3 || sc.List[1].ID != "s02" || sc.List[1].Title != "The claim" || sc.List[1].Sections["Narration"] != "This one starts in space." || sc.List[0].Frames[0].Ref != "design-asset:abc" || sc.List[0].Words != 4 {
		t.Fatalf("%+v %v", sc, err)
	}
}

// A script put into Caster by hand shows up at the next read-back.
func TestReadBackPicksUpNewScripts(t *testing.T) {
	s, f := seeded(t)
	f.team("qspace.second", "---\nstory: v1\ntitle: Second\nstage: idea\nscenes: []\n---\n")
	if n, err := s.Refresh(context.Background(), true); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	cards, _ := s.Board(context.Background())
	if len(cards) != 2 {
		t.Fatal(cards)
	}
	// Unchanged items are not fetched again.
	if n, _ := s.Refresh(context.Background(), true); n != 0 {
		t.Fatal(n)
	}
}

// Reordering applies to the manifest as it is now: a scene the team added meanwhile stays.
func TestOrderAppliesToTheManifestAsItIsNow(t *testing.T) {
	s, f := seeded(t)
	f.team("qspace.launch-teaser", strings.Replace(manifest, "scenes: [s01, s02, s03]", "scenes: [s01, s02, s03, s04]", 1))
	if err := s.SetOrder(context.Background(), "qspace.launch-teaser", []string{"s03", "s01", "s02"}); err != nil {
		t.Fatal(err)
	}
	it, _, _ := f.Get(context.Background(), "qspace.launch-teaser", 0)
	d, _ := ParseDoc(it.Content)
	if strings.Join(d.Strings("scenes"), ",") != "s03,s01,s02,s04" || d.Str("custom_field") != "keep me" || it.Rev != 3 {
		t.Fatalf("%v rev %d\n%s", d.Strings("scenes"), it.Rev, it.Content)
	}
	sc, _, _ := s.Script(context.Background(), "qspace.launch-teaser")
	if sc.List[0].ID != "s03" {
		t.Fatal("the copy did not follow")
	}
}

func TestEditScene(t *testing.T) {
	s, f := seeded(t)
	ctx := context.Background()
	n := "Every launch starts somewhere. Ours starts here."
	title := "Opening"
	rev, err := s.EditScene(ctx, "qspace.launch-teaser", "s01", SceneEdit{BaseRev: 1, Title: &title, Sections: map[string]string{"Narration": n}})
	if err != nil || rev != 2 {
		t.Fatal(rev, err)
	}
	it, _, _ := f.Get(ctx, "qspace.launch-teaser.s01", 0)
	d, _ := ParseDoc(it.Content)
	b := ParseSceneBody(d.Body)
	if b.Get("Narration") != n || d.Str("title") != "Opening" || b.Get("Visual") != "A hangar." || !strings.Contains(it.Content, "## Extra\nkept as is") || len(f.items["qspace.launch-teaser.s01"]) != 2 {
		t.Fatalf("%s", it.Content)
	}
	if _, err := s.EditScene(ctx, "qspace.launch-teaser", "s01", SceneEdit{BaseRev: 2, Sections: map[string]string{"Bogus": "x"}}); err == nil {
		t.Fatal("unknown section taken")
	}
}

// The scene moved on in Caster after the owner began: nothing is overwritten, and the
// owner's text is kept as a suggestion on the newer revision.
func TestEditConflictKeepsASuggestion(t *testing.T) {
	s, f := seeded(t)
	ctx := context.Background()
	f.team("qspace.launch-teaser.s02", scene("s02", "The claim", "The team's newer line."))
	_, err := s.EditScene(ctx, "qspace.launch-teaser", "s02", SceneEdit{BaseRev: 1, Sections: map[string]string{"Narration": "My line.", "Visual": "A hangar."}})
	var c *Conflict
	if !errors.As(err, &c) || c.Rev != 2 || c.Suggestions != 1 {
		t.Fatalf("%v", err)
	}
	it, _, _ := f.Get(ctx, "qspace.launch-teaser.s02", 0)
	if it.Rev != 2 || !strings.Contains(it.Content, "The team's newer line.") {
		t.Fatal("overwritten")
	}
	notes, _ := s.Drafts(ctx, "qspace.launch-teaser")
	if len(notes) != 1 || notes[0].Kind != "suggestion" || notes[0].Section != "Narration" || notes[0].Quote != "The team's newer line." || notes[0].Replace != "My line." || notes[0].SceneRev != 2 {
		t.Fatalf("%+v", notes)
	}
	cards, _ := s.Board(ctx)
	if cards[0].DraftNotes != 1 {
		t.Fatal(cards[0].DraftNotes)
	}
}

func TestAddCutRestoreAndNewScript(t *testing.T) {
	s, f := seeded(t)
	ctx := context.Background()
	key := "qspace.launch-teaser"
	if err := s.CutScene(ctx, key, "s03"); err != nil {
		t.Fatal(err)
	}
	// s03 still exists in Caster, so a new scene is s04, after s01.
	id, err := s.AddScene(ctx, key, "s01", "", "Beat")
	if err != nil || id != "s04" {
		t.Fatal(id, err)
	}
	dup, err := s.AddScene(ctx, key, "", "s02", "")
	if err != nil || dup != "s05" {
		t.Fatal(dup, err)
	}
	sc, _, _ := s.Script(ctx, key)
	ids := []string{}
	for _, x := range sc.List {
		ids = append(ids, x.ID)
	}
	if strings.Join(ids, ",") != "s01,s04,s02,s05" || len(sc.Unused) != 1 || sc.Unused[0].ID != "s03" || sc.List[3].Title != "The claim (copy)" || sc.List[3].Sections["Narration"] != "This one starts in space." {
		t.Fatalf("%v unused %v", ids, sc.Unused)
	}
	if err := s.RestoreScene(ctx, key, "s03"); err != nil {
		t.Fatal(err)
	}
	sc, _, _ = s.Script(ctx, key)
	if sc.List[len(sc.List)-1].ID != "s03" || len(sc.Unused) != 0 {
		t.Fatal("restore")
	}
	if _, err := s.NewScript(ctx, "qspace", "launch-teaser", "dup"); err == nil {
		t.Fatal("an existing script was made again")
	}
	if _, err := s.NewScript(ctx, "QSpace", "x", "bad"); err == nil {
		t.Fatal("a bad slug was taken")
	}
	k, err := s.NewScript(ctx, "basalisk", "story-seed", "Seed")
	if err != nil || k != "basalisk.story-seed" {
		t.Fatal(k, err)
	}
	it, _, _ := f.Get(ctx, "basalisk.story-seed", 0)
	if d, _ := ParseDoc(it.Content); d.Str("stage") != "idea" || d.Str("story") != "v1" || len(d.Strings("scenes")) != 0 {
		t.Fatalf("%s", it.Content)
	}
	if err := s.SetStage(ctx, k, "outline"); err != nil || s.SetStage(ctx, k, "nonsense") == nil {
		t.Fatal("stage")
	}
}

func TestWithoutCaster(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "story.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Refresh(context.Background(), true); !errors.Is(err, ErrNoCaster) {
		t.Fatal(err)
	}
	if err := s.SetStage(context.Background(), "a.b", "draft"); !errors.Is(err, ErrNoCaster) {
		t.Fatal(err)
	}
}
