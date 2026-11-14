package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Context slots. The owner moves between kinds of work (their mail, a plan, a draft, a
// terminal) and talks to one assistant throughout. A slot is what the assistant brings to
// one kind of work: the tools that are top of mind there, its own note of where things
// stand, and where the rest is kept. Moving to another slot changes those; it does not
// change who the assistant is, what it knows, or which tools it has.
//
// The slot is chosen from the window in front (or by the assistant, with context_switch).
// When it changes, the owner's message carries a <context> block; the tool definitions and
// the system prompt stay the same from turn to turn, so the provider's cache still holds.

// Slot is one kind of work.
type Slot struct {
	Name  string `json:"name"`
	About string `json:"about"`
	// Apps are the apps whose window in front selects this slot.
	Apps []string `json:"apps,omitempty"`
	// Tools are top of mind here, by name; ones this run does not have are left out.
	Tools []string `json:"tools,omitempty"`
	// Where is where the state of this kind of work is kept, for finding what the note lacks.
	Where string `json:"where,omitempty"`
}

// Slots are the built-in slots, in the order they are tried. The last is the default.
var Slots = []Slot{
	{Name: "messaging", About: "the owner's mail and their agents: what came in, who waits on whom, replies and delegations in the owner's name",
		Apps:  []string{"inbox", "assist"},
		Tools: []string{"owner_inbox", "owner_mail_read", "owner_ack", "owner_mail_settle", "owner_status_ask", "owner_reply", "owner_send", "status_overview", "consult", "tether_registry_search", "torque_task_get"},
		Where: "the owner's inbox (owner_inbox); what you already did for them (the Done for you window); the work a message is about (Torque, by task id)"},
	{Name: "planning", About: "plans and what is in flight: goals, milestones, tasks, content on its way to being published",
		Apps:  []string{"planner", "content", "sessions", "scratch"},
		Tools: []string{"torque_task_list", "torque_task_get", "torque_project_list", "scratch_list", "scratch_add", "tesseract_recall", "task_start", "owner_status_ask", "status_overview", "consult"},
		Where: "Torque (tasks and their state); the owner's scratch (quick items, follow-ups); Tesseract (decisions and designs); the Planner and Content workspaces"},
	{Name: "writing", About: "the owner's own writing and scripts: drafts, scenes, chapters, review rounds",
		Apps:  []string{"writer", "story", "drafts"},
		Tools: []string{"collab_list", "collab_read", "collab_mark", "collab_reply", "collab_write", "story_list", "story_get", "story_note", "notes_read"},
		Where: "the document in front (collab_read with its marks, story_get); its revisions in Caster; the owner's notes"},
	{Name: "notes", About: "notes and quick capture: what the owner wrote down and wants kept or found",
		Apps:  []string{"notes", "frag", "outbox"},
		Tools: []string{"notes_list", "notes_read", "notes_write", "notes_edit", "frag_capture", "frag_recent", "scratch_add", "tesseract_recall"},
		Where: "the notes folder (notes_list); scratch; Fragments Engine (frag_recent); Tesseract"},
	{Name: "engineering", About: "building and fixing: code, files, terminals, diagrams, artifacts under review",
		Apps:  []string{"terminal", "files", "files-find", "diagram", "viewer", "shell"},
		Tools: []string{"files_list", "files_read", "files_grep", "terminal_list", "terminal_read", "terminal_prepare", "terminal_run", "collab_read", "collab_look", "desktop_look", "torque_task_get", "consult", "chief_delegate"},
		Where: "the files and the shell in front; the diagram or artifact with its marks (collab_read); the task this is for (Torque)"},
	{Name: "reading", About: "reading and watching: pages, saved articles, videos, feeds",
		Apps:  []string{"browser", "browser-history", "reader", "watch"},
		Tools: []string{"browser_read", "browser_find", "browser_links", "frag_capture", "notes_write"},
		Where: "the page in front (browser_read); what was saved (Fragments Engine)"},
	{Name: "general", About: "whatever is in front of the owner"},
}

func slotNamed(name string) (Slot, bool) {
	for _, s := range Slots {
		if s.Name == name {
			return s, true
		}
	}
	return Slot{}, false
}

// slotForApp is the slot a focused app selects, or "".
func slotForApp(app string) string {
	for _, s := range Slots {
		for _, a := range s.Apps {
			if a == app {
				return s.Name
			}
		}
	}
	return ""
}

// focusedApp reads the app of the window in front from the page's snapshot.
func focusedApp(raw json.RawMessage) string {
	var c struct {
		Focused *struct {
			App string `json:"app"`
		} `json:"focused"`
	}
	if json.Unmarshal(raw, &c) != nil || c.Focused == nil {
		return ""
	}
	return c.Focused.App
}

// slotState is which slot the owner's conversation is in, and what each thread was last told.
var slotState = struct {
	sync.Mutex
	current string            // the slot now
	pinned  bool              // set by context_switch: holds until the window in front selects another
	told    map[string]string // thread -> slot and note stamp last put in a message
}{current: "general", told: map[string]string{}}

// slotBlock decides the slot for a message of the owner's and returns the <context> block
// to put in front of it, or "" when the thread already has it.
func (rt *Runtime) slotBlock(ctx context.Context, thread string, pageContext json.RawMessage, have map[string]ToolSpec) string {
	slotState.Lock()
	if byApp := slotForApp(focusedApp(pageContext)); byApp != "" && byApp != slotState.current {
		// The window in front wins, unless the assistant just chose one and the window has
		// no slot of its own to say otherwise.
		slotState.current, slotState.pinned = byApp, false
	}
	name := slotState.current
	// Where this thread was before, to ask for a note on leaving it.
	left, _, _ := strings.Cut(slotState.told[thread], "@")
	slotState.Unlock()
	slot, ok := slotNamed(name)
	if !ok {
		return ""
	}
	note, when := rt.store.slotNote(ctx, name)
	stamp := name + "@" + when
	slotState.Lock()
	same := slotState.told[thread] == stamp
	slotState.told[thread] = stamp
	slotState.Unlock()
	if same {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<context name=%q>\nThe owner is now in their %s context: %s.\n", slot.Name, slot.Name, slot.About)
	var top []string
	for _, t := range slot.Tools {
		if have[t].Name != "" {
			top = append(top, t)
		}
	}
	if len(top) > 0 {
		b.WriteString("Top of mind here: " + strings.Join(top, ", ") + ". Your other tools are still there.\n")
	}
	if note != "" {
		fmt.Fprintf(&b, "Your note for this context (you wrote it %s):\n%s\n", when, note)
	} else {
		b.WriteString("You have no note for this context yet. When something here is worth carrying to next time (where things stand, what is next, ids and names to pick up from), write it with context_note.\n")
	}
	if slot.Where != "" {
		b.WriteString("Where the rest is: " + slot.Where + ".\n")
	}
	if left != "" && left != name && left != "general" {
		fmt.Fprintf(&b, "You have just come from the %s context. If something there is worth carrying and your note for it does not have it yet, write it now with context_note (context: %q) before you go on; say nothing of this to the owner.\n", left, left)
	}
	b.WriteString("</context>")
	return b.String()
}

const maxSlotNote = 6000

// SlotTools are the assistant's own tools for its contexts.
func (rt *Runtime) SlotTools() ToolSource {
	names := make([]string, 0, len(Slots))
	for _, s := range Slots {
		names = append(names, s.Name)
	}
	which := map[string]any{"type": "string", "enum": names, "description": "The context. The current one when left out."}
	pick := func(in map[string]any) (Slot, error) {
		name, _ := in["context"].(string)
		if name == "" {
			slotState.Lock()
			name = slotState.current
			slotState.Unlock()
		}
		s, ok := slotNamed(name)
		if !ok {
			return s, errors.New("there is no such context: " + strings.Join(names, ", "))
		}
		return s, nil
	}
	tools := []ServerTool{
		{
			Spec: ToolSpec{Name: "context_note", Level: LevelAct,
				Description: "Write your note for a context: what you want to know the next time the owner is back in this kind of work. Where things stand, what was decided, what is next, and pointers to the rest (task ids, document names, Tesseract keys, file paths, who is waiting on whom), not the detail itself. It replaces the note that was there, so carry forward what still holds. Keep it under a page. Write it when something worth keeping happened, and before the owner moves on to something else.",
				InputSchema: map[string]any{"type": "object", "properties": map[string]any{"note": map[string]any{"type": "string", "description": "The whole note, plain text or Markdown."}, "context": which}, "required": []string{"note"}, "additionalProperties": false}},
			Run: func(ctx context.Context, in map[string]any) (string, error) {
				s, err := pick(in)
				if err != nil {
					return "", err
				}
				note := strings.TrimSpace(fmt.Sprint(in["note"]))
				if len(note) > maxSlotNote {
					return "", fmt.Errorf("the note is %d characters; keep it under %d: pointers, not detail", len(note), maxSlotNote)
				}
				if err := rt.store.setSlotNote(ctx, s.Name, note); err != nil {
					return "", err
				}
				return "kept for " + s.Name, nil
			},
		},
		{
			Spec: ToolSpec{Name: "context_get", Level: LevelRead,
				Description: "Read your note for a context (the current one, or another by name), with the list of your contexts and when each note was last written. Use it to pick up something from another kind of work without the owner going there.",
				InputSchema: map[string]any{"type": "object", "properties": map[string]any{"context": which}, "additionalProperties": false}},
			Run: func(ctx context.Context, in map[string]any) (string, error) {
				s, err := pick(in)
				if err != nil {
					return "", err
				}
				note, when := rt.store.slotNote(ctx, s.Name)
				type row struct {
					Name    string `json:"name"`
					About   string `json:"about"`
					Written string `json:"note_written,omitempty"`
				}
				all := []row{}
				for _, x := range Slots {
					_, w := rt.store.slotNote(ctx, x.Name)
					all = append(all, row{x.Name, x.About, w})
				}
				raw, _ := json.Marshal(map[string]any{"context": s.Name, "note": note, "written": when, "where_the_rest_is": s.Where, "contexts": all})
				return string(raw), nil
			},
		},
		{
			Spec: ToolSpec{Name: "context_switch", Level: LevelAct,
				Description: "Move to another context when what the owner is asking about has changed kind and the window in front does not say so (they ask about mail while a terminal is in front). You get that context's note and what is top of mind there. Nothing else about you changes.",
				InputSchema: map[string]any{"type": "object", "properties": map[string]any{"context": which}, "required": []string{"context"}, "additionalProperties": false}},
			Run: func(ctx context.Context, in map[string]any) (string, error) {
				s, err := pick(in)
				if err != nil {
					return "", err
				}
				slotState.Lock()
				slotState.current, slotState.pinned = s.Name, true
				slotState.Unlock()
				note, when := rt.store.slotNote(ctx, s.Name)
				raw, _ := json.Marshal(map[string]any{"context": s.Name, "about": s.About, "top_of_mind": s.Tools, "note": note, "written": when, "where_the_rest_is": s.Where})
				return string(raw), nil
			},
		},
	}
	return func(context.Context) []ServerTool { return tools }
}

func (s *Store) slotNote(ctx context.Context, name string) (note, when string) {
	_ = s.db.QueryRowContext(ctx, `SELECT note, updated_at FROM slots WHERE name=?`, name).Scan(&note, &when)
	return note, when
}

func (s *Store) setSlotNote(ctx context.Context, name, note string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO slots(name, note, updated_at) VALUES(?,?,?)
		ON CONFLICT(name) DO UPDATE SET note=excluded.note, updated_at=excluded.updated_at`, name, note, time.Now().UTC().Format("2006-01-02 15:04 UTC"))
	return err
}
