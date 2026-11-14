package inbox

import (
	"context"
	"encoding/json"
	"testing"
)

type fake struct {
	calls []map[string]any
	tools []string
}

func (f *fake) Configured() bool { return true }
func (f *fake) CallAll(ctx context.Context, tool string, args map[string]any) ([]byte, error) {
	return f.Call(ctx, tool, args)
}
func (f *fake) Call(_ context.Context, tool string, args map[string]any) ([]byte, error) {
	f.tools = append(f.tools, tool)
	f.calls = append(f.calls, args)
	switch tool {
	case "tether_message_list":
		return []byte(`{"total":7,"messages":[
		 {"id":"m1","kind":"request","from":"msg://agent/x/a1","to":"msg://user/local/operator","thread_id":"t1",
		  "payload":{"kind":"decision","subject":"Pick","body":"Which?","options":[{"id":"a","label":"A","recommended":true},{"id":"b","label":"B"}],"default":{"option":"a","at":"2026-11-15T17:00Z"}},
		  "created_at":"2026-11-14T10:00:00Z","subject":"Pick","body":"Which?"},
		 {"id":"m2","kind":"notice","from":"msg://agent/x/a1","to":"msg://user/local/operator","payload":{"text":"hello there"},
		  "created_at":"2026-11-14T11:00:00Z","read_at":"2026-11-14T11:01:00Z"}]}`), nil
	case "tether_registry_lookup":
		return []byte(`{"urn":"msg://agent/x/a1","display_name":"Archie","role":"architect"}`), nil
	case "tether_message_get":
		return []byte(`{"message":{"id":"m1","kind":"request","from":"msg://agent/x/a1","to":"msg://user/local/operator","thread_id":"t1","payload":{"kind":"decision","subject":"Pick","options":[{"id":"a","label":"A"}]},"subject":"Pick"}}`), nil
	}
	return []byte(`{}`), nil
}

func TestTether(t *testing.T) {
	f := &fake{}
	p := &Tether{Link: f, Addresses: []string{"msg://user/local/operator"}}
	in := New(p)
	items, src := in.List(context.Background(), Filter{})
	if len(items) != 2 || src[0].Error != "" {
		t.Fatalf("items %d, source %+v", len(items), src)
	}
	if items[0].ID != "m2" || items[0].Body != "hello there" || items[0].Subject != "hello there" || !items[0].Read {
		t.Fatalf("newest first, text payload: %+v", items[0])
	}
	a := items[1]
	if a.Ask == nil || a.Ask.Kind != "decision" || len(a.Ask.Options) != 2 || a.Ask.Default != "a" || a.FromName != "Archie" {
		t.Fatalf("ask: %+v", a)
	}
	if n := in.Unread(context.Background()); n != 7 {
		t.Fatalf("unread %d", n)
	}
	if _, err := in.Provider("tether", "msg://agent/x/other"); err == nil {
		t.Fatal("an account that is not the owner's must be refused")
	}
	if err := p.Reply(context.Background(), "msg://user/local/operator", "m1", Reply{Choice: "a", Body: "go"}); err != nil {
		t.Fatal(err)
	}
	last := f.calls[len(f.calls)-1]
	var payload map[string]any
	_ = json.Unmarshal([]byte(last["payload_json"].(string)), &payload)
	if f.tools[len(f.tools)-1] != "tether_message_notify" || last["to"] != "msg://agent/x/a1" || last["from"] != "msg://user/local/operator" ||
		last["in_reply_to"] != "m1" || last["thread_id"] != "t1" || payload["choice"] != "a" || payload["choice_label"] != "A" || payload["subject"] != "re: Pick" {
		t.Fatalf("reply: %v %v", last, payload)
	}
}
