package agent

import (
	"context"
	"encoding/json"
	"errors"
)

// GenUIShowTools exposes only reviewed surface presentation. It neither submits
// nor confirms an action, and inherits no legacy tool approval. The inner JSON
// string preserves duplicate keys and numeric lexemes for the strict host parser.
func GenUIShowTools(show func(context.Context, string, []byte) (json.RawMessage, error)) ToolSource {
	tool := ServerTool{Spec: ToolSpec{Name: "genui_show_pattern", Level: LevelAct, Description: "Present the account preferences review form. This does not change preferences or grant approval.", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"payload_json": map[string]any{"type": "string", "description": "Exact JSON: {\"pattern\":\"form\",\"params\":{}}"}}, "required": []string{"payload_json"}, "additionalProperties": false}}, Run: func(ctx context.Context, input map[string]any) (string, error) {
		r := runOf(ctx)
		if r == nil || r.owner == "" || ctx.Err() != nil || show == nil {
			return "", errors.New("GenUI presentation requires a live enrolled run")
		}
		if r.rt == nil || r.rt.store == nil {
			return "", errors.New("genui run unavailable")
		}
		owner := r.rt.store.RunOwner(ctx, r.id)
		var running int
		if err := r.rt.store.db.QueryRowContext(ctx, "SELECT count(*) FROM runs WHERE id=? AND owner=? AND status='running'", r.id, owner).Scan(&running); err != nil || running != 1 {
			return "", errors.New("genui run is no longer active")
		}
		if owner != r.owner || owner == "" {
			return "", errors.New("GenUI run owner unavailable")
		}
		raw, ok := input["payload_json"].(string)
		if !ok || len(input) != 1 {
			return "", errors.New("GenUI presentation requires payload_json only")
		}
		out, e := show(ctx, owner, []byte(raw))
		return string(out), e
	}}
	return func(context.Context) []ServerTool { return []ServerTool{tool} }
}
