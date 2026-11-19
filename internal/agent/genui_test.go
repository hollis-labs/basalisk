package agent

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/libs/ui/ui-go/chatstream"
)

func TestGenUIShowToolUsesActualEnrolledRunAndPreservesInnerJSON(t *testing.T) {
	inner := `{"pattern":"form","params":{},"params":{}}`
	outer, _ := json.Marshal(map[string]string{"payload_json": inner})
	model := newStandIn(t, toolReply("show-1", "genui_show_pattern", string(outer)), textReply("Review presented."))
	rt := newRuntime(t, model)
	var calls atomic.Int32
	source := GenUIShowTools(func(ctx context.Context, owner string, raw []byte) (json.RawMessage, error) {
		calls.Add(1)
		if owner != "user:fixture-owner" || string(raw) != inner {
			t.Errorf("run ownership/raw bytes lost %q %s", owner, raw)
		}
		return json.RawMessage(`{"review":"fixture"}`), nil
	})
	rt.AddTools(source)
	id, e := rt.Start(t.Context(), StartInput{Owner: "user:fixture-owner", Text: "show preferences"})
	if e != nil {
		t.Fatal(e)
	}
	events := follow(t, rt, id, nil)
	if calls.Load() != 1 || last(events).Verb != chatstream.VerbRunFinish {
		t.Fatal("server presentation did not run")
	}
	for _, ev := range events {
		if ev.Verb == chatstream.VerbApprovalRequest {
			t.Fatal("presentation inherited legacy grant flow")
		}
	}
	tool := source(t.Context())[0]
	if _, e = tool.Run(t.Context(), map[string]any{"payload_json": inner}); e == nil {
		t.Fatal("no private run accepted")
	}
	// A forged private context still cannot disagree with the persisted owner.
	ctx := context.WithValue(t.Context(), runKey{}, &run{rt: rt, id: id, owner: "user:other"})
	if _, e = tool.Run(ctx, map[string]any{"payload_json": inner}); e == nil {
		t.Fatal("mismatched persisted run owner")
	}
	if calls.Load() != 1 {
		t.Fatal("unbound caller reached host")
	}
}
