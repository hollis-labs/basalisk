package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/basalisk/internal/tetherlink"
)

// recLink offers the write and mail tools and remembers what it was called with.
type recLink struct {
	calls []string
	args  []map[string]any
}

func (f *recLink) Configured() bool { return true }
func (f *recLink) Tools(context.Context) ([]tetherlink.ToolInfo, error) {
	names := []string{"memory_write", "knowledge_write", "workspace_write", "tesseract_skills",
		"tether_registry_search", "tether_message_list", "tether_message_get", "tether_message_mark_read", "tether_message_notify",
		"tether_message_send", "tether_message_purge", "tether_registry_register"}
	var out []tetherlink.ToolInfo
	for _, n := range names {
		out = append(out, tetherlink.ToolInfo{Name: n, Description: n + "."})
	}
	return out, nil
}
func (f *recLink) Call(_ context.Context, tool string, args map[string]any) ([]byte, error) {
	f.calls = append(f.calls, tool)
	f.args = append(f.args, args)
	return []byte(`{"ok":true}`), nil
}

func loadTools(t *testing.T, src ToolSource) map[string]ServerTool {
	t.Helper()
	src(t.Context())
	deadline := time.Now().Add(2 * time.Second)
	for len(src(t.Context())) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	out := map[string]ServerTool{}
	for _, tool := range src(t.Context()) {
		out[tool.Spec.Name] = tool
	}
	return out
}

func TestTheAgentWritesToTesseractAsItselfAndNotInTheOwnersScope(t *testing.T) {
	link := &recLink{}
	tools := loadTools(t, TetherTools(link))
	for _, name := range []string{"memory_write", "knowledge_write", "workspace_write"} {
		if tools[name].Spec.Level != LevelWrite {
			t.Errorf("%s is not a write tool: %+v", name, tools[name].Spec)
		}
	}
	if tools["tesseract_skills"].Spec.Level != LevelRead {
		t.Error("tesseract_skills is not offered as a read")
	}
	// Without an address of its own the agent has no mail tools.
	for name := range mailTools {
		if _, ok := tools[name]; ok {
			t.Errorf("%s offered without an address", name)
		}
	}
	for _, ns := range []string{"user/chrispian/memory/decisions", "User/x", "system", "system/memory/rules"} {
		if _, err := tools["memory_write"].Run(t.Context(), map[string]any{"namespace": ns, "payload_summary": "x"}); err == nil || !strings.Contains(err.Error(), "owner's own") {
			t.Errorf("a write to %q: %v", ns, err)
		}
	}
	if len(link.calls) != 0 {
		t.Fatalf("a refused write reached the gateway: %v", link.calls)
	}
	if _, err := tools["knowledge_write"].Run(t.Context(), map[string]any{"namespace": "app/basalisk/knowledge/notes", "author_agent_id": "chrispian", "actor": "user", "summary": "x"}); err != nil {
		t.Fatal(err)
	}
	got := link.args[0]
	if got["author_agent_id"] != "basalisk" || got["actor"] != nil || got["namespace"] != "app/basalisk/knowledge/notes" {
		t.Errorf("what was written: %+v", got)
	}
}

func TestTheAgentsMailIsFromAndToItsOwnAddress(t *testing.T) {
	const urn = "msg://agent/agent-mux/agt_basalisk"
	link := &recLink{}
	tools := loadTools(t, TetherTools(link, TetherOptions{URN: urn}))
	for name, level := range mailTools {
		if tools[name].Spec.Level != level {
			t.Errorf("%s: %+v", name, tools[name].Spec)
		}
	}
	// Tools that are not on either list stay out, whatever the gateway offers.
	for _, name := range []string{"tether_message_send", "tether_message_purge", "tether_registry_register"} {
		if _, ok := tools[name]; ok {
			t.Errorf("%s was offered", name)
		}
	}
	other := "msg://user/local/operator"
	calls := []struct {
		tool string
		in   map[string]any
		key  string
	}{
		{"tether_message_notify", map[string]any{"from": other, "to": "msg://agent/agent-mux/agt_x", "payload_json": `{"body":"hi"}`}, "from"},
		{"tether_message_list", map[string]any{"to": other}, "to"},
		{"tether_message_list", map[string]any{}, "to"},
		{"tether_message_get", map[string]any{"as": other, "message_id": "m1"}, "as"},
		{"tether_message_mark_read", map[string]any{"message_id": "m1"}, "as"},
	}
	for i, c := range calls {
		if _, err := tools[c.tool].Run(t.Context(), c.in); err != nil {
			t.Fatal(err)
		}
		if got := link.args[i][c.key]; got != urn {
			t.Errorf("%s %s = %v, want the agent's own address", c.tool, c.key, got)
		}
	}
	if raw, _ := json.Marshal(link.args); strings.Contains(string(raw), other) {
		t.Errorf("another address reached the gateway: %s", raw)
	}
	if link.args[0]["to"] != "msg://agent/agent-mux/agt_x" {
		t.Errorf("the recipient was changed: %+v", link.args[0])
	}
}
