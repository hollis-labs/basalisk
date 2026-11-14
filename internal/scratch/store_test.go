package scratch

import (
	"context"
	"encoding/json"
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
	if _, err := s.Add(t.Context(), "u1", "::config project=basalisk\n::todo fix the icon\n::idea reader as playlists +fe\n$ git status", "owner", nil, yesterday, Auto{}); err != nil {
		t.Fatal(err)
	}
	added, err := s.Add(t.Context(), "u1", ":follow-up ask @devtoo about CW-20261114-0045\n\nplain note for today", "owner", []byte(`{"window":{"app":"notes"}}`), today, Auto{})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 2 || added[0].Kind != KindFollowUp || added[1].Kind != KindNote || added[0].Day != "2026-11-14" || string(added[0].Context) != `{"window":{"app":"notes"}}` {
		t.Fatalf("added: %+v", added)
	}
	if _, err := s.Add(t.Context(), "u2", "someone else's", "owner", nil, today, Auto{}); err != nil {
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
	if len(f.Days) != 2 || f.Days[0] != (Count{Value: "2026-11-14", N: 2}) || f.Days[1] != (Count{Value: "2026-11-13", N: 2}) || f.Open != 1 || f.Total != 4 {
		t.Errorf("facets: %+v", f)
	}
	if len(f.Projects) != 2 || f.Projects[0] != (Count{Value: "basalisk", N: 2}) || len(f.Tasks) != 1 || len(f.Agents) != 1 {
		t.Errorf("facet links: %+v", f)
	}
}

func TestAViewReadsAsAPageAndBackIn(t *testing.T) {
	s := open(t)
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.Local)
	in := "a loose note\n::todo fix it\n::idea write it up\n$ make test\n::follow-up ask about it"
	if _, err := s.Add(t.Context(), "u1", in, "owner", nil, at, Auto{}); err != nil {
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
	added, _ := s.Add(t.Context(), "u1", "::todo fix the icon +basalisk\n\n$ make test", "owner", nil, at, Auto{})
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

func TestItemsAreTiedToTheSessionThatWasGoing(t *testing.T) {
	s := open(t)
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.Local)
	auto := Auto{SessionID: "ses_1", SessionTitle: "Scratch manager", Project: "Basalisk"}
	added, err := s.Add(t.Context(), "u1", "::idea from inside a session\n\nfor another +fe", "owner", nil, at, auto)
	if err != nil {
		t.Fatal(err)
	}
	has := func(it Item, rel, value string) bool {
		for _, l := range it.Links {
			if l.Rel == rel && l.Value == value {
				return true
			}
		}
		return false
	}
	// Both are tied to the session; the project comes from it unless the item names one.
	if !has(added[0], RelSession, "ses_1") || !has(added[0], RelProject, "basalisk") {
		t.Errorf("first: %+v", added[0].Links)
	}
	if !has(added[1], RelSession, "ses_1") || !has(added[1], RelProject, "fe") || has(added[1], RelProject, "basalisk") {
		t.Errorf("second: %+v", added[1].Links)
	}
	if _, err := s.Add(t.Context(), "u1", "no session going", "owner", nil, at, Auto{}); err != nil {
		t.Fatal(err)
	}
	items, _ := s.List(t.Context(), "u1", Query{Rel: RelSession, Value: "ses_1"})
	if len(items) != 2 {
		t.Errorf("the session's view: %s", texts(items))
	}
	f, _ := s.Facets(t.Context(), "u1")
	if len(f.Sessions) != 1 || f.Sessions[0] != (Count{Value: "ses_1", N: 2, Label: "Scratch manager"}) {
		t.Errorf("session facet: %+v", f.Sessions)
	}
	if got := s.Labels(t.Context())["session:ses_1"]; got != "Scratch manager" {
		t.Errorf("label: %q", got)
	}
}

// listCaster answers caster_ls and caster_get from what it holds, and records puts.
type listCaster struct {
	fakeCaster
	held map[string]string // name -> content
	gets int
}

func (f *listCaster) Call(ctx context.Context, tool string, args map[string]any) ([]byte, error) {
	switch tool {
	case "caster_ls":
		var b strings.Builder
		b.WriteString(`{"items":[`)
		i := 0
		for name := range f.held {
			if i > 0 {
				b.WriteString(",")
			}
			i++
			b.WriteString(`{"ref":"caster://scratch/` + name + `","rev":1,"title":"t","updated":"2026-11-13T10:00:00Z"}`)
		}
		b.WriteString(`]}`)
		return []byte(b.String()), nil
	case "caster_get":
		f.gets++
		name := strings.TrimPrefix(args["ref"].(string), "caster://scratch/")
		content := f.held[name]
		out, _ := json.Marshal(map[string]any{"content": content, "state": "resolved", "size": len(content)})
		return out, nil
	}
	return f.fakeCaster.Call(ctx, tool, args)
}

func TestItemsPutInCasterByOthersAreReadBack(t *testing.T) {
	s := open(t)
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.Local)
	mine, _ := s.Add(t.Context(), "u1", "my own item", "owner", nil, at, Auto{})
	caster := &listCaster{held: map[string]string{
		"agent-note":   "::follow-up check the deploy +tachyon\n",
		mine[0].ID:     "my own item\n",
		"big-doc":      strings.Repeat("a long document ", 1000),
		"scr_fromelse": "[ ] from another machine\n",
	}}
	y := NewSyncer(s, caster)
	y.Pass(t.Context())
	n, err := y.Import(t.Context(), "u1")
	if err != nil || n != 2 {
		t.Fatalf("imported %d: %v", n, err)
	}
	all, _ := s.List(t.Context(), "u1", Query{})
	if got := texts(all); got != "follow-up:check the deploy +tachyon | todo:from another machine | note:my own item" {
		t.Fatalf("after the import: %s", got)
	}
	found, _ := s.List(t.Context(), "u1", Query{Kind: KindFollowUp})
	it := found[0]
	if it.ID != "cst_agent-note" || it.Origin != "caster" || !it.Synced || it.CasterRef != "caster://scratch/agent-note" || it.Day != "2026-11-13" || len(it.Links) != 1 {
		t.Errorf("the adopted item: %+v", it)
	}
	// Nothing found is written back, and a second look fetches nothing again.
	puts, gets := len(caster.calls), caster.gets
	y.Pass(t.Context())
	if n, _ := y.Import(t.Context(), "u1"); n != 0 || len(caster.calls) != puts || caster.gets != gets {
		t.Errorf("second look: imported %d, puts %d (was %d), gets %d (was %d)", n, len(caster.calls), puts, caster.gets, gets)
	}
	// An edit to an adopted item goes back under the name it has in Caster.
	text := "check the deploy again +tachyon"
	if _, err := s.Update(t.Context(), "u1", "cst_agent-note", Change{Text: &text}, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	y.Pass(t.Context())
	if last := caster.calls[len(caster.calls)-1]; last["name"] != "scratch/agent-note" || last["content"] != "::follow-up check the deploy again +tachyon\n" {
		t.Errorf("written back as: %+v", last)
	}
}

func TestEarlierNotesWithAMarkerAtTheEndAreReadAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scratch.db")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.Local)
	// As the first version kept it: a note with the marker left in its text.
	if _, err := s.db.Exec(`INSERT INTO items(id, owner, kind, text, day, created_at, updated_at) VALUES('scr_old','u1','note','API gateway as a peer ::idea','2026-11-14',?,?)`, stamp(at), stamp(at)); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	it, _ := s.Get(t.Context(), "u1", "scr_old")
	if it.Kind != KindIdea || it.Text != "API gateway as a peer" || it.Synced {
		t.Fatalf("after reopening: %+v", it)
	}
}

func TestWhatAnItemBecameIsKeptThroughAnEdit(t *testing.T) {
	s := open(t)
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.Local)
	added, _ := s.Add(t.Context(), "u1", "::todo write it up +basalisk", "owner", nil, at, Auto{SessionID: "ses_1", SessionTitle: "S"})
	// Promoted to a Torque task: tied to it, and recorded as what it became.
	it, err := s.Update(t.Context(), "u1", added[0].ID, Change{Link: &Link{RelTask, "CW-20261114-0099"}}, at)
	if err != nil {
		t.Fatal(err)
	}
	it, _ = s.Update(t.Context(), "u1", it.ID, Change{Link: &Link{RelBecame, "task:CW-20261114-0099"}}, at)
	it, _ = s.Update(t.Context(), "u1", it.ID, Change{Link: &Link{RelBecame, "fragment"}}, at)
	text := "write it up properly"
	it, _ = s.Update(t.Context(), "u1", it.ID, Change{Text: &text}, at)
	var got []string
	for _, l := range it.Links {
		got = append(got, l.Rel+"="+l.Value)
	}
	// The project was only in the text, so an edit that drops it drops the link. The
	// session, what it became, and the task it became stay.
	if strings.Join(got, " ") != "became=fragment became=task:CW-20261114-0099 session=ses_1 task=CW-20261114-0099" {
		t.Errorf("links after the edit: %v", got)
	}
}
