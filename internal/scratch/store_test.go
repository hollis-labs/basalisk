package scratch

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "scratch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func texts(items []Item) string {
	var out []string
	for _, it := range items {
		out = append(out, it.Kind+":"+it.Text)
	}
	return strings.Join(out, " | ")
}

func TestItemsAreKeptAndViewedSeveralWays(t *testing.T) {
	s := open(t)
	yesterday := time.Date(2026, 11, 13, 22, 0, 0, 0, time.Local)
	today := time.Date(2026, 11, 14, 9, 0, 0, 0, time.Local)
	if _, err := s.Add(t.Context(), "u1", "::config project=basalisk\n::todo fix the icon\n::idea reader as playlists +fe\n$ git status", "owner", nil, yesterday); err != nil {
		t.Fatal(err)
	}
	added, err := s.Add(t.Context(), "u1", ":follow-up ask @devtoo about CW-20261114-0045\n\nplain note for today", "owner", []byte(`{"window":{"app":"notes"}}`), today)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 2 || added[0].Kind != KindFollowUp || added[1].Kind != KindNote || added[0].Day != "2026-11-14" || string(added[0].Context) != `{"window":{"app":"notes"}}` {
		t.Fatalf("added: %+v", added)
	}
	if _, err := s.Add(t.Context(), "u2", "someone else's", "owner", nil, today); err != nil {
		t.Fatal(err)
	}
	view := func(q Query) string {
		items, err := s.List(t.Context(), "u1", q)
		if err != nil {
			t.Fatal(err)
		}
		return texts(items)
	}
	if got := view(Query{Day: "2026-11-13"}); got != "todo:fix the icon | idea:reader as playlists +fe | cmd:git status" {
		t.Errorf("yesterday: %s", got)
	}
	if got := view(Query{Day: "2026-11-14"}); got != "follow-up:ask @devtoo about CW-20261114-0045 | note:plain note for today" {
		t.Errorf("today: %s", got)
	}
	if got := view(Query{Rel: RelProject, Value: "Basalisk"}); got != "todo:fix the icon | idea:reader as playlists +fe | cmd:git status" {
		t.Errorf("project: %s", got)
	}
	if got := view(Query{Rel: RelProject, Value: "fe"}); got != "idea:reader as playlists +fe" {
		t.Errorf("second project: %s", got)
	}
	if got := view(Query{Rel: RelTask, Value: "CW-20261114-0045"}); got != "follow-up:ask @devtoo about CW-20261114-0045" {
		t.Errorf("task: %s", got)
	}
	if got := view(Query{Rel: RelAgent, Value: "devtoo"}); got != "follow-up:ask @devtoo about CW-20261114-0045" {
		t.Errorf("agent: %s", got)
	}
	if got := view(Query{Open: true}); got != "todo:fix the icon | follow-up:ask @devtoo about CW-20261114-0045" {
		t.Errorf("open: %s", got)
	}
	if got := view(Query{Kind: KindCommand}); got != "cmd:git status" {
		t.Errorf("commands: %s", got)
	}
	if got := view(Query{Text: "ICON fix"}); got != "todo:fix the icon" {
		t.Errorf("search: %s", got)
	}
	if got := view(Query{}); strings.Contains(got, "someone else") || strings.Count(got, "|") != 4 {
		t.Errorf("everything of the owner's: %s", got)
	}

	// Done, edited, put away.
	todo, _ := s.List(t.Context(), "u1", Query{Kind: KindTodo})
	yes, no := true, false
	done, err := s.Update(t.Context(), "u1", todo[0].ID, Change{Done: &yes}, today)
	if err != nil || !done.Done || done.DoneAt == "" {
		t.Fatalf("done: %+v %v", done, err)
	}
	if got := view(Query{Open: true}); got != "follow-up:ask @devtoo about CW-20261114-0045" {
		t.Errorf("open after done: %s", got)
	}
	if undone, _ := s.Update(t.Context(), "u1", todo[0].ID, Change{Done: &no}, today); undone.Done {
		t.Errorf("not undone: %+v", undone)
	}
	text := "fix the icon at 16 px +design"
	edited, err := s.Update(t.Context(), "u1", todo[0].ID, Change{Text: &text}, today)
	if err != nil || edited.Kind != KindTodo || edited.Text != text || len(edited.Links) != 1 || edited.Links[0] != (Link{RelProject, "design"}) {
		t.Fatalf("edited: %+v %v", edited, err)
	}
	asIdea := ":idea an icon set"
	if e, _ := s.Update(t.Context(), "u1", todo[0].ID, Change{Text: &asIdea}, today); e.Kind != KindIdea || e.Text != "an icon set" {
		t.Errorf("re-marked: %+v", e)
	}
	empty := "  "
	if _, err := s.Update(t.Context(), "u1", todo[0].ID, Change{Text: &empty}, today); err == nil {
		t.Error("an item was emptied")
	}
	if _, err := s.Update(t.Context(), "u2", todo[0].ID, Change{Done: &yes}, today); err == nil {
		t.Error("another owner changed the item")
	}
	if a, _ := s.Update(t.Context(), "u1", todo[0].ID, Change{Archived: &yes}, today); !a.Archived {
		t.Errorf("not put away: %+v", a)
	}
	if got := view(Query{Day: "2026-11-13"}); strings.Contains(got, "icon") {
		t.Errorf("a put-away item is still in the day: %s", got)
	}
	if got := view(Query{Archived: true}); got != "idea:an icon set" {
		t.Errorf("put away: %s", got)
	}

	f, err := s.Facets(t.Context(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Days) != 2 || f.Days[0] != (Count{"2026-11-14", 2}) || f.Days[1] != (Count{"2026-11-13", 2}) || f.Open != 1 || f.Total != 4 {
		t.Errorf("facets: %+v", f)
	}
	if len(f.Projects) != 2 || f.Projects[0] != (Count{"basalisk", 2}) || len(f.Tasks) != 1 || len(f.Agents) != 1 {
		t.Errorf("facet links: %+v", f)
	}
}

func TestAViewReadsAsAPageAndBackIn(t *testing.T) {
	s := open(t)
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.Local)
	in := "a loose note\n::todo fix it\n::idea write it up\n$ make test\n::follow-up ask about it"
	if _, err := s.Add(t.Context(), "u1", in, "owner", nil, at); err != nil {
		t.Fatal(err)
	}
	items, _ := s.List(t.Context(), "u1", Query{})
	page := Markdown("Scratch: 2026-11-14", items)
	want := "# Scratch: 2026-11-14\n\na loose note\n\n[ ] fix it\n\n::idea write it up\n\n$ make test\n\n::follow-up ask about it\n"
	if page != want {
		t.Fatalf("page:\n%s", page)
	}
	// The page, without its heading, reads back in as the same items.
	back := Parse(strings.TrimPrefix(page, "# Scratch: 2026-11-14\n\n"))
	if len(back) != len(items) {
		t.Fatalf("read back %d items, had %d: %+v", len(back), len(items), back)
	}
	for i := range back {
		if back[i].Kind != items[i].Kind || back[i].Text != items[i].Text {
			t.Errorf("item %d: %+v, was %s:%s", i, back[i], items[i].Kind, items[i].Text)
		}
	}
}

type fakeCaster struct {
	fail  bool
	calls []map[string]any
}

func (f *fakeCaster) Configured() bool { return true }
func (f *fakeCaster) Call(_ context.Context, tool string, args map[string]any) ([]byte, error) {
	if f.fail {
		return nil, errors.New("gateway down")
	}
	if tool != "caster_put" {
		return nil, errors.New("unexpected tool " + tool)
	}
	f.calls = append(f.calls, args)
	return []byte(`{"ref":"x"}`), nil
}

func TestItemsAreWrittenThroughToCaster(t *testing.T) {
	s := open(t)
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.Local)
	added, _ := s.Add(t.Context(), "u1", "::todo fix the icon +basalisk\n\n$ make test", "owner", nil, at)
	caster := &fakeCaster{fail: true}
	y := NewSyncer(s, caster)
	if _, failed := y.Pass(t.Context()); !failed {
		t.Fatal("a failed write was not reported")
	}
	if it, _ := s.Get(t.Context(), "u1", added[0].ID); it.Synced || it.SyncError == "" {
		t.Fatalf("after a failure: %+v", it)
	}
	caster.fail = false
	if more, failed := y.Pass(t.Context()); more || failed || len(caster.calls) != 2 {
		t.Fatalf("pass: more=%v failed=%v calls=%d", more, failed, len(caster.calls))
	}
	first := caster.calls[0]
	if first["name"] != "scratch/"+added[0].ID || first["content"] != "[ ] fix the icon +basalisk\n" || first["title"] != "fix the icon +basalisk" ||
		first["tags"] != "scratch kind:todo day:2026-11-14 project:basalisk" || first["mime"] != "text/markdown" {
		t.Errorf("what was put: %+v", first)
	}
	if caster.calls[1]["content"] != "$ make test\n" {
		t.Errorf("the command: %+v", caster.calls[1])
	}
	it, _ := s.Get(t.Context(), "u1", added[0].ID)
	if !it.Synced || it.CasterRef != "caster://scratch/"+it.ID || it.SyncError != "" {
		t.Fatalf("after the write: %+v", it)
	}
	// Nothing changed: nothing is written again.
	if y.Pass(t.Context()); len(caster.calls) != 2 {
		t.Fatalf("written again with no change: %d", len(caster.calls))
	}
	// Done is a change, and goes as a tag.
	yes := true
	if _, err := s.Update(t.Context(), "u1", it.ID, Change{Done: &yes}, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if y.Pass(t.Context()); len(caster.calls) != 3 || !strings.HasSuffix(caster.calls[2]["tags"].(string), " done") || caster.calls[2]["content"] != "[x] fix the icon +basalisk\n" {
		t.Fatalf("after done: %+v", caster.calls[len(caster.calls)-1])
	}
}
