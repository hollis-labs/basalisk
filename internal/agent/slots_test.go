package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlots(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	rt := &Runtime{store: store}
	have := map[string]ToolSpec{"owner_inbox": {Name: "owner_inbox"}, "owner_reply": {Name: "owner_reply"}, "files_read": {Name: "files_read"}}
	page := func(app string) json.RawMessage {
		raw, _ := json.Marshal(map[string]any{"focused": map[string]any{"app": app, "title": "x"}})
		return raw
	}
	slotState.Lock()
	slotState.current, slotState.told = "general", map[string]string{}
	slotState.Unlock()

	first := rt.slotBlock(ctx, "t1", page("inbox"), have)
	if !strings.Contains(first, `<context name="messaging">`) || !strings.Contains(first, "owner_inbox, owner_reply") || strings.Contains(first, "owner_send") {
		t.Fatalf("the window in front selects the slot, and only tools this run has are named:\n%s", first)
	}
	if again := rt.slotBlock(ctx, "t1", page("inbox"), have); again != "" {
		t.Fatalf("a thread is told once while nothing changed:\n%s", again)
	}
	tools := map[string]ServerTool{}
	for _, tl := range rt.SlotTools()(ctx) {
		tools[tl.Spec.Name] = tl
	}
	if _, err := tools["context_note"].Run(ctx, map[string]any{"note": "Archie waits on the alias decision (CW-20261114-0067)."}); err != nil {
		t.Fatal(err)
	}
	withNote := rt.slotBlock(ctx, "t1", page("inbox"), have)
	if !strings.Contains(withNote, "CW-20261114-0067") {
		t.Fatalf("a new note is told:\n%s", withNote)
	}
	moved := rt.slotBlock(ctx, "t1", page("terminal"), have)
	if !strings.Contains(moved, `<context name="engineering">`) || !strings.Contains(moved, "no note for this context yet") || !strings.Contains(moved, "files_read") {
		t.Fatalf("moving to a terminal moves the slot:\n%s", moved)
	}
	if !strings.Contains(moved, "just come from the messaging context") {
		t.Fatalf("leaving a context asks for its note:\n%s", moved)
	}
	if _, err := tools["context_switch"].Run(ctx, map[string]any{"context": "messaging"}); err != nil {
		t.Fatal(err)
	}
	// A window with no slot of its own does not undo the assistant's choice.
	back := rt.slotBlock(ctx, "t1", page("settings"), have)
	if !strings.Contains(back, `<context name="messaging">`) || !strings.Contains(back, "CW-20261114-0067") {
		t.Fatalf("context_switch holds, with its note:\n%s", back)
	}
	out, err := tools["context_get"].Run(ctx, map[string]any{"context": "engineering"})
	if err != nil || !strings.Contains(out, `"context":"engineering"`) {
		t.Fatalf("%v %s", err, out)
	}
	if _, err := tools["context_note"].Run(ctx, map[string]any{"note": strings.Repeat("x", maxSlotNote+1)}); err == nil {
		t.Fatal("a note longer than a page is refused")
	}
}

func TestProjects(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	rt := &Runtime{store: store}
	slotState.Lock()
	slotState.current, slotState.project, slotState.told = "general", "", map[string]string{}
	slotState.Unlock()
	if err := store.SaveProject(ctx, Project{Name: "Engineering"}); err == nil {
		t.Fatal("a project may not take the name of a kind of work")
	}
	if err := store.SaveProject(ctx, Project{Name: "basalisk", About: "the desktop", Match: []string{"apps/basalisk", "x"}, Torque: "PRJ-1", Instructions: "Small commits."}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSlotInstructions(ctx, "engineering", "Tests before push."); err != nil {
		t.Fatal(err)
	}
	page, _ := json.Marshal(map[string]any{"focused": map[string]any{"app": "terminal", "title": "zsh", "cwd": "/work/src/apps/Basalisk-wt"}})
	block := rt.slotBlock(ctx, "t9", page, map[string]ToolSpec{})
	for _, want := range []string{`<context name="engineering">`, "Tests before push.", "Project: basalisk (the desktop)", "Torque project PRJ-1", "Small commits.", "no note for this project yet"} {
		if !strings.Contains(block, want) {
			t.Fatalf("missing %q in:\n%s", want, block)
		}
	}
	tools := map[string]ServerTool{}
	for _, tl := range rt.SlotTools()(ctx) {
		tools[tl.Spec.Name] = tl
	}
	if _, err := tools["context_note"].Run(ctx, map[string]any{"project": "basalisk", "note": "Step 5 merged; step 6 next."}); err != nil {
		t.Fatal(err)
	}
	// Another window with no project of its own: the project holds, and its new note is told.
	other, _ := json.Marshal(map[string]any{"focused": map[string]any{"app": "notes", "title": "Ideas"}})
	block = rt.slotBlock(ctx, "t9", other, map[string]ToolSpec{})
	if !strings.Contains(block, `<context name="notes">`) || !strings.Contains(block, "Step 5 merged") {
		t.Fatalf("the project is carried across a change of kind:\n%s", block)
	}
	if err := rt.PickProject(ctx, "none"); err != nil || rt.ProjectNow() != "" {
		t.Fatal("none clears the project")
	}
	if len(store.Projects(ctx)[0].Match) != 1 {
		t.Fatal("a match word shorter than three letters is dropped")
	}
}
