package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestRuleEditsValidateAndRefuseStaleRevision(t *testing.T) {
	ctx := context.Background()
	rt := &Runtime{router: NewRouter(filepath.Join(t.TempDir(), "rules.json"))}
	tools := map[string]ServerTool{}
	for _, tool := range rt.SettingsTools()(ctx) {
		tools[tool.Spec.Name] = tool
	}
	read := func() map[string]any {
		t.Helper()
		raw, err := tools["agent_rules_get"].Run(ctx, map[string]any{"kind": "router"})
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err = json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := read()
	change := map[string]any{"kind": "router", "revision": first["revision"], "content": `{"version":"own","strategy":"first_match","rules":[]}`}
	if _, err := tools["agent_rules_write"].Run(ctx, change); err != nil {
		t.Fatal(err)
	}
	second := read()
	if second["revision"] == first["revision"] {
		t.Fatal("revision did not change")
	}
	change["content"] = `{"version":"own2","strategy":"first_match","rules":[]}`
	if _, err := tools["agent_rules_write"].Run(ctx, change); err == nil {
		t.Fatal("stale edit accepted")
	}
	change["revision"] = second["revision"]
	change["content"] = `{"invalid"`
	if _, err := tools["agent_rules_write"].Run(ctx, change); err == nil {
		t.Fatal("invalid rules accepted")
	}
	if got := read(); got["revision"] != second["revision"] {
		t.Fatal("invalid write changed rules")
	}
	if tools["agent_rules_write"].Spec.Level != LevelConfirm {
		t.Fatal("rule replacement must ask")
	}
}
