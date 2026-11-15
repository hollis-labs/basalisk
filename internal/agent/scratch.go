package agent

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/hollis-labs/basalisk/internal/scratch"
)

// ScratchTools are the agent's tools for the owner's scratch (internal/scratch): add
// items, list a view, mark one done. kick is called after a change so it is written on
// to Caster; auto says what an item written now is tied to (the work session going).
func ScratchTools(store *scratch.Store, kick func(), auto func(ctx context.Context, owner string) scratch.Auto) ToolSource {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	tools := []ServerTool{
		{
			Spec: ToolSpec{
				Name:  "scratch_add",
				Level: LevelWrite,
				Description: "Keep something in the owner's scratch: a quick note, a follow-up not to forget, a command, an idea. " +
					"Write it as they would: a line starting '::follow-up', '::idea', '::todo', '::q' or '$ ' says what it is (plain text is a note); " +
					"+project, @agent, #tag and task ids in the text tie it to those. Several items may be given, a blank line between them.",
				InputSchema: map[string]any{
					"type":                 "object",
					"properties":           map[string]any{"text": str("What to keep, with its marker if it has one.")},
					"required":             []string{"text"},
					"additionalProperties": false,
				},
			},
			Run: func(ctx context.Context, input map[string]any) (string, error) {
				r := runOf(ctx)
				if r == nil {
					return "", errors.New("no run")
				}
				text, _ := input["text"].(string)
				origin := "agent"
				if r.route != nil {
					origin = "owner" // typed as a command, word for word
				}
				items, err := store.Add(ctx, r.owner, text, origin, nil, time.Now(), auto(ctx, r.owner))
				if err != nil {
					return "", err
				}
				kick()
				return scratchJSON(items), nil
			},
		},
		{
			Spec: ToolSpec{
				Name:  "scratch_list",
				Level: LevelRead,
				Description: "The owner's scratch items in a view: a day (YYYY-MM-DD), a kind (note, todo, follow-up, idea, cmd, question), " +
					"what they are tied to (rel project, agent, task or tag, with value), only the open follow-ups and todos, or words to search for. Oldest first.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"day": str("One day, YYYY-MM-DD."), "kind": str("One kind."),
						"rel": str("project, agent, task or tag; with value."), "value": str("The project, agent, task id or tag."),
						"open": map[string]any{"type": "boolean", "description": "Only todos, follow-ups and questions that are not done."},
						"q":    str("Words that must all appear."),
					},
					"additionalProperties": false,
				},
			},
			Run: func(ctx context.Context, input map[string]any) (string, error) {
				r := runOf(ctx)
				if r == nil {
					return "", errors.New("no run")
				}
				s := func(k string) string { v, _ := input[k].(string); return v }
				open, _ := input["open"].(bool)
				items, err := store.List(ctx, r.owner, scratch.Query{Day: s("day"), Kind: s("kind"), Rel: s("rel"), Value: s("value"), Open: open, Text: s("q"), Limit: 200})
				if err != nil {
					return "", err
				}
				return scratchJSON(items), nil
			},
		},
		{
			Spec: ToolSpec{
				Name:        "scratch_done",
				Level:       LevelWrite,
				Description: "Mark a scratch item done (or not done again), by its id from scratch_list.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":   str("The item's id."),
						"done": map[string]any{"type": "boolean", "description": "False to mark it not done. Default true."},
					},
					"required":             []string{"id"},
					"additionalProperties": false,
				},
			},
			Run: func(ctx context.Context, input map[string]any) (string, error) {
				r := runOf(ctx)
				if r == nil {
					return "", errors.New("no run")
				}
				id, _ := input["id"].(string)
				done := true
				if v, ok := input["done"].(bool); ok {
					done = v
				}
				it, err := store.Update(ctx, r.owner, id, scratch.Change{Done: &done}, time.Now())
				if err != nil {
					return "", err
				}
				kick()
				return scratchJSON([]scratch.Item{it}), nil
			},
		},
	}

	tools = append(tools, ServerTool{
		Spec: ToolSpec{Name: "scratch_edit", Level: LevelWrite, Description: "Edit one scratch item's text by id. Keeps its identity and reparses markers and links; use scratch_list to read first.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": str("Item id"), "text": str("Replacement text for one item")}, "required": []string{"id", "text"}, "additionalProperties": false}},
		Run: func(ctx context.Context, in map[string]any) (string, error) {
			r := runOf(ctx)
			if r == nil {
				return "", errors.New("no run")
			}
			id, _ := in["id"].(string)
			text, ok := in["text"].(string)
			if !ok {
				return "", errors.New("text is required")
			}
			item, err := store.Update(ctx, r.owner, id, scratch.Change{Text: &text}, time.Now())
			if err != nil {
				return "", err
			}
			kick()
			return scratchJSON([]scratch.Item{item}), nil
		},
	}, ServerTool{
		Spec: ToolSpec{Name: "scratch_delete", Level: LevelConfirm, Description: "Put a scratch item away (archive it), preserving its text and Caster record. Always asks.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": str("Item id")}, "required": []string{"id"}, "additionalProperties": false}},
		Run: func(ctx context.Context, in map[string]any) (string, error) {
			r := runOf(ctx)
			if r == nil {
				return "", errors.New("no run")
			}
			id, _ := in["id"].(string)
			archived := true
			item, err := store.Update(ctx, r.owner, id, scratch.Change{Archived: &archived}, time.Now())
			if err != nil {
				return "", err
			}
			kick()
			return scratchJSON([]scratch.Item{item}), nil
		},
	})

	tools = append(tools, ServerTool{
		Spec: ToolSpec{Name: "scratch_get", Level: LevelRead, Description: "Read the full text and metadata of one scratch item, including a put-away item, before editing it.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": str("Item id")}, "required": []string{"id"}, "additionalProperties": false}},
		Run: func(ctx context.Context, in map[string]any) (string, error) {
			r := runOf(ctx)
			if r == nil {
				return "", errors.New("no run")
			}
			id, _ := in["id"].(string)
			item, ok := store.Get(ctx, r.owner, id)
			if !ok {
				return "", errors.New("no such scratch item")
			}
			raw, err := json.Marshal(item)
			return string(raw), err
		},
	}, ServerTool{
		Spec: ToolSpec{Name: "scratch_restore", Level: LevelWrite, Description: "Restore a put-away scratch item by id.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": str("Item id")}, "required": []string{"id"}, "additionalProperties": false}},
		Run: func(ctx context.Context, in map[string]any) (string, error) {
			r := runOf(ctx)
			if r == nil {
				return "", errors.New("no run")
			}
			id, _ := in["id"].(string)
			archived := false
			item, err := store.Update(ctx, r.owner, id, scratch.Change{Archived: &archived}, time.Now())
			if err != nil {
				return "", err
			}
			kick()
			return scratchJSON([]scratch.Item{item}), nil
		},
	})
	return func(context.Context) []ServerTool { return tools }
}

func scratchJSON(items []scratch.Item) string {
	type row struct {
		ID    string         `json:"id"`
		Kind  string         `json:"kind"`
		Text  string         `json:"text"`
		Done  bool           `json:"done,omitempty"`
		Day   string         `json:"day"`
		Links []scratch.Link `json:"links,omitempty"`
	}
	out := make([]row, 0, len(items))
	for _, it := range items {
		out = append(out, row{it.ID, it.Kind, clip(it.Text, 2000), it.Done, it.Day, it.Links})
	}
	raw, _ := json.Marshal(map[string]any{"items": out})
	return string(raw)
}
