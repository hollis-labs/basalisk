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
