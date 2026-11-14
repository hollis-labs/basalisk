package collab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fake is a stand-in Caster: items by name, each a list of revisions.
type fake struct{ items map[string][]string }

func (f *fake) Configured() bool { return true }
func (f *fake) CallAll(ctx context.Context, tool string, args map[string]any) ([]byte, error) {
	return f.Call(ctx, tool, args)
}
func (f *fake) Call(_ context.Context, tool string, args map[string]any) ([]byte, error) {
	switch tool {
	case "caster_put":
		name := strings.TrimPrefix(args["name"].(string), Namespace+"/")
		f.items[name] = append(f.items[name], args["content"].(string))
		return []byte(fmt.Sprintf(`{"ref":"caster://%s/%s@%d"}`, Namespace, name, len(f.items[name]))), nil
	case "caster_get":
		ref := strings.TrimPrefix(args["ref"].(string), "caster://"+Namespace+"/")
		name, at, pinned := strings.Cut(ref, "@")
		revs := f.items[name]
		if len(revs) == 0 {
			return []byte(`{"state":"not_found"}`), nil
		}
		n := len(revs)
		if pinned {
			_, _ = fmt.Sscan(at, &n)
		}
		out, _ := json.Marshal(map[string]any{"state": "resolved", "content": revs[n-1], "revision": fmt.Sprint(n)})
		return out, nil
	case "caster_ls":
		var items []map[string]any
		for name, revs := range f.items {
			items = append(items, map[string]any{"ref": "caster://" + Namespace + "/" + name, "rev": len(revs), "title": name, "updated": "2026-11-14T00:00:00Z"})
		}
		out, _ := json.Marshal(map[string]any{"items": items})
		return out, nil
	}
	return []byte(`{}`), nil
}

func TestDocs(t *testing.T) {
	ctx := context.Background()
	s := &Store{Link: &fake{items: map[string][]string{}}}
	d, err := s.Create(ctx, Doc{Kind: KindDraft, Title: "Launch Plan: v2!", Lang: "markdown", Body: "one\ntwo\n"}, "user")
	if err != nil || d.Name != "draft.launch-plan-v2" || d.Rev != 1 {
		t.Fatalf("%v %+v", err, d)
	}
	d2, _ := s.Create(ctx, Doc{Kind: KindDraft, Title: "Launch Plan: v2!"}, "user")
	if d2.Name != "draft.launch-plan-v2-2" {
		t.Fatalf("a second document of the same title gets its own name: %s", d2.Name)
	}
	got, err := s.Get(ctx, d.Name, 0)
	if err != nil || got.Body != "one\ntwo\n" || got.Lang != "markdown" || got.Title != "Launch Plan: v2!" {
		t.Fatalf("%v %+v", err, got)
	}
	saved, err := s.Save(ctx, d.Name, 1, nil, "one\nTWO\n", "agent")
	if err != nil || saved.Rev != 2 {
		t.Fatalf("%v %+v", err, saved)
	}
	var c *Conflict
	if _, err := s.Save(ctx, d.Name, 1, nil, "stale", "user"); !errors.As(err, &c) || c.Rev != 2 {
		t.Fatalf("an edit begun on an old revision is refused: %v", err)
	}
	if old, _ := s.Get(ctx, d.Name, 1); old.Body != "one\ntwo\n" {
		t.Fatalf("revision 1 is still readable: %q", old.Body)
	}
	list, _ := s.List(ctx, KindDraft)
	if len(list) != 2 {
		t.Fatalf("marks files are not listed as documents: %+v", list)
	}
	marks, err := s.MarkOp(ctx, d.Name, "add", Mark{Rev: 2, Kind: "suggestion", Anchor: Anchor{Quote: "TWO", LineStart: 2, LineEnd: 2}, Replace: "two"})
	if err != nil || len(marks) != 1 || marks[0].State != "open" || marks[0].By != "user" || marks[0].ID == "" {
		t.Fatalf("%v %+v", err, marks)
	}
	marks, err = s.MarkOp(ctx, d.Name, "update", Mark{ID: marks[0].ID, State: "done", Reply: "taken"})
	if err != nil || marks[0].State != "done" || marks[0].Reply != "taken" || marks[0].Replace != "two" {
		t.Fatalf("%v %+v", err, marks)
	}
	if list, _ := s.List(ctx, ""); len(list) != 2 {
		t.Fatalf("%+v", list)
	}
	if _, err := s.Get(ctx, "draft.../x", 0); !errors.Is(err, ErrNotFound) {
		t.Fatal("a name that is not a document's is refused")
	}
}
