package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/basalisk/internal/assist"
	"github.com/hollis-labs/basalisk/internal/inbox"
)

type stubLink struct{ calls []string }

func (f *stubLink) Call(_ context.Context, tool string, args map[string]any) ([]byte, error) {
	raw, _ := json.Marshal(args)
	f.calls = append(f.calls, tool+" "+string(raw))
	switch tool {
	case "tether_registry_search":
		return []byte(`[{"urn":"msg://agent/x/chief1","display_name":"chief"},{"urn":"msg://agent/x/other","display_name":"Chief of Staff"}]`), nil
	case "torque_task_create":
		return []byte(`{"data":{"ID":"CW-TEST-1"},"ok":true}`), nil
	case "torque_task_list":
		return []byte(`{"data":{"items":[{"id":"CW-1","title":"A","status":"doing","tags":["cell-x","seat-a"],"updated_at":"2026-11-14T00:00:00Z"},{"id":"CW-2","title":"B","status":"review","tags":["cell-x"],"updated_at":"2026-11-13T00:00:00Z"}],"meta":{"total":40}},"ok":true}`), nil
	}
	return []byte(`{}`), nil
}

func TestOwnerWiderTools(t *testing.T) {
	ctx := context.Background()
	log, err := assist.Open(ctx, filepath.Join(t.TempDir(), "assist.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = log.Close() }()
	mail := &stubMail{}
	box := inbox.New(mail)
	watch := &assist.Watcher{Mail: box, Log: log}
	if err := watch.Init(ctx); err != nil {
		t.Fatal(err)
	}
	link := &stubLink{}
	tools := map[string]ServerTool{}
	for _, tl := range OwnerTools(box, log, OwnerDeps{Watch: watch, Link: link, DevAddress: "msg://agent/x/dev"})(ctx) {
		tools[tl.Spec.Name] = tl
	}
	for name, level := range map[string]string{"status_overview": LevelRead, "mail_rules": LevelRead, "chief_delegate": LevelWrite, "mail_rule_add": LevelWrite, "mail_rule_remove": LevelWrite, "dev_request": LevelWrite} {
		if tools[name].Spec.Level != level {
			t.Fatalf("%s is %q, want %q", name, tools[name].Spec.Level, level)
		}
	}
	got, err := tools["status_overview"].Run(ctx, map[string]any{})
	if err != nil || !strings.Contains(got, `"total":40`) || !strings.Contains(got, `"cell-x":2`) || !strings.Contains(got, `"doing":1`) {
		t.Fatalf("%v %s", err, got)
	}
	if _, err := tools["chief_delegate"].Run(ctx, map[string]any{"title": "Restart X", "goal": "X is running the new build", "context": "host h", "scope_in": "service X", "scope_out": "", "acceptance": []any{"X answers"}, "evidence": "status output", "risk": "elevated"}); err == nil {
		t.Fatal("a delegation without what not to touch is refused")
	}
	res, err := tools["chief_delegate"].Run(ctx, map[string]any{"title": "Restart X", "goal": "X is running the new build", "context": "host h", "scope_in": "service X", "scope_out": "the database", "acceptance": []any{"X answers on its port"}, "evidence": "status output", "risk": "elevated"})
	if err != nil || !strings.Contains(res, "CW-TEST-1") {
		t.Fatalf("%v %s", err, res)
	}
	last := mail.sent[len(mail.sent)-1]
	if last.To != "msg://agent/x/chief1" || !strings.Contains(last.Body, "CW-TEST-1") || last.Extra["re"] != "task:CW-TEST-1" || last.Extra["sent_by"] != "assistant" {
		t.Fatalf("@chief is the seat named exactly chief, told of the task: %+v", last)
	}
	create := ""
	for _, c := range link.calls {
		if strings.HasPrefix(c, "torque_task_create") {
			create = c
		}
	}
	for _, want := range []string{"## Goal", "## Scope", "Out: the database", "- [ ] X answers on its port", "risk: elevated", "seat-chief"} {
		if !strings.Contains(create, want) {
			t.Fatalf("the task body lacks %q: %s", want, create)
		}
	}
	if _, err := tools["mail_rule_add"].Run(ctx, map[string]any{"kind": "status_update", "class": "digest", "note": "too many"}); err != nil {
		t.Fatal(err)
	}
	rules, _ := tools["mail_rules"].Run(ctx, map[string]any{})
	if !strings.Contains(rules, `"kind":"status_update"`) {
		t.Fatal(rules)
	}
	if _, err := tools["dev_request"].Run(ctx, map[string]any{"title": "A tool for calendars", "what": "Wanted: …"}); err != nil || mail.sent[len(mail.sent)-1].To != "msg://agent/x/dev" {
		t.Fatalf("%v", err)
	}
}
