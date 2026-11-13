package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/ui/ui-go/chatstream"
	"github.com/hollis-labs/libs/ui/ui-go/chatstream/conformance"
	"github.com/hollis-labs/libs/ui/ui-go/chatstream/hubbind"
	"github.com/hollis-labs/libs/ui/ui-go/streamhub"
)

// standIn is a model gateway for tests: it answers each request with the next scripted
// reply, as the Messages API streams it, and remembers what it was sent.
type standIn struct {
	mu      sync.Mutex
	replies []string
	bodies  []map[string]any
	headers []http.Header
	srv     *httptest.Server
}

func sse(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		var t struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(e), &t)
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", t.Type, e)
	}
	return b.String()
}

func textReply(text string) string {
	half := len(text) / 2
	return sse(
		`{"type":"message_start","message":{"usage":{"input_tokens":10}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, text[:half]),
		fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, text[half:]),
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		`{"type":"message_stop"}`,
	)
}

func toolReply(id, name, inputJSON string) string {
	half := len(inputJSON) / 2
	return sse(
		`{"type":"message_start","message":{"usage":{"input_tokens":10}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Looking."}}`,
		`{"type":"content_block_stop","index":0}`,
		fmt.Sprintf(`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":%q,"name":%q,"input":{}}}`, id, name),
		fmt.Sprintf(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":%q}}`, inputJSON[:half]),
		fmt.Sprintf(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":%q}}`, inputJSON[half:]),
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`,
		`{"type":"message_stop"}`,
	)
}

func newStandIn(t *testing.T, replies ...string) *standIn {
	s := &standIn{replies: replies}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.bodies = append(s.bodies, body)
		s.headers = append(s.headers, r.Header.Clone())
		reply := ""
		if len(s.replies) > 0 {
			reply, s.replies = s.replies[0], s.replies[1:]
		}
		s.mu.Unlock()
		switch {
		case reply == "":
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"code":"internal","message":"no reply scripted"}`))
		case strings.HasPrefix(reply, "status:"):
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"code":"unauthenticated","message":"bad key"}`))
		case reply == "hang":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(reply))
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func newRuntime(t *testing.T, s *standIn) *Runtime {
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	if err := os.WriteFile(key, []byte("test-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(t.Context(), filepath.Join(dir, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cfg := Config{GatewayURL: s.srv.URL, KeyFile: key, Model: "test-model"}
	return New(t.Context(), cfg, store, NewGateway(cfg))
}

// follow reads a run's events to the end, handing each to on (which may answer tool calls).
func follow(t *testing.T, rt *Runtime, id string, on func(chatstream.Event)) []chatstream.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	sub, err := rt.Hub().Subscribe(ctx, id, streamhub.SubscribeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	var out []chatstream.Event
	for ev, err := range hubbind.Events(ctx, sub, id) {
		if err != nil {
			t.Fatalf("stream: %v (after %d events)", err, len(out))
		}
		out = append(out, ev)
		if on != nil {
			on(ev)
		}
	}
	conformance.Check(t, out)
	return out
}

func last(evs []chatstream.Event) chatstream.Event { return evs[len(evs)-1] }

func textOf(evs []chatstream.Event) string {
	var b strings.Builder
	kinds := map[string]string{}
	for _, e := range evs {
		if e.Verb == chatstream.VerbPartStart {
			kinds[e.PartID] = e.Kind
		}
		if e.Verb == chatstream.VerbPartDelta && kinds[e.PartID] == "text" {
			b.WriteString(e.Text)
		}
	}
	return b.String()
}

var lookTool = ToolSpec{Name: "desktop_windows", Description: "List the windows.", Level: LevelRead, InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}
var changeTool = ToolSpec{Name: "note_write", Description: "Write a note.", InputSchema: map[string]any{"type": "object"}} // no level: asks

func TestAPlainAnswerIsStreamedAndKept(t *testing.T) {
	s := newStandIn(t, textReply("Hello there."))
	rt := newRuntime(t, s)
	ctxJSON := json.RawMessage(`{"focused":{"winId":"w1","app":"notes","title":"plan.md","file":{"root":"home","path":"Notes/plan.md"},"selection":"the selected words"},"windows":[{"winId":"w1","app":"notes","title":"plan.md"},{"winId":"w2","app":"browser","title":"Docs","url":"https://example.test/a"}]}`)
	id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "  hi  ", ClientID: "c1", Context: ctxJSON})
	if err != nil {
		t.Fatal(err)
	}
	evs := follow(t, rt, id, nil)
	if last(evs).Verb != chatstream.VerbRunFinish || last(evs).Reason != "stop" {
		t.Fatalf("ended with %+v", last(evs))
	}
	if got := textOf(evs); got != "Hello there." {
		t.Fatalf("text = %q", got)
	}
	if u := last(evs).Usage; u == nil || u.Output != 5 || u.UncachedInput != 10 {
		t.Fatalf("usage = %+v", last(evs).Usage)
	}
	// The request: the key as a bearer, the thread as the session, the context ahead of the words.
	h := s.headers[0]
	thread, _ := rt.Store().MainThread(t.Context(), "u1")
	if h.Get("Authorization") != "Bearer test-key" || h.Get("X-Station-Session") != thread {
		t.Fatalf("headers: %v", h)
	}
	sent, _ := json.Marshal(s.bodies[0])
	for _, want := range []string{`"model":"test-model"`, `"cache_control":{"type":"ephemeral"}`, `"stream":true`, `Focused: `, `notes window \"plan.md\" (id w1), file home:Notes/plan.md`, `the selected words`, `browser window \"Docs\" (id w2), page https://example.test/a`, `"text":"hi"`} {
		if !strings.Contains(string(sent), want) {
			t.Errorf("request lacks %s\n%s", want, sent)
		}
	}
	msgs, err := rt.Store().Messages(t.Context(), thread)
	if err != nil || len(msgs) != 2 || msgs[0].Role != "user" || msgs[1].Role != "assistant" || msgs[1].Blocks[0].Text != "Hello there." {
		t.Fatalf("stored: %+v err=%v", msgs, err)
	}
	if !strings.Contains(string(msgs[0].Meta), `"text":"hi"`) {
		t.Fatalf("meta: %s", msgs[0].Meta)
	}
	if rt.ActiveRun(thread) != "" {
		t.Fatal("the thread is still busy")
	}
}

func TestAToolCallGoesToThePageAndItsAnswerToTheModel(t *testing.T) {
	s := newStandIn(t, toolReply("toolu_1", "desktop_windows", `{"all":true}`), textReply("Two windows."))
	rt := newRuntime(t, s)
	id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "what is open?", ClientID: "c1", Tools: []ToolSpec{lookTool}})
	if err != nil {
		t.Fatal(err)
	}
	asked := false
	evs := follow(t, rt, id, func(e chatstream.Event) {
		if e.Verb == chatstream.VerbApprovalRequest {
			t.Error("a tool that only looks asked for permission")
		}
		if e.Verb == chatstream.VerbPartEnd && len(e.Ext) > 0 {
			var exec, client, call string
			_ = json.Unmarshal(e.Ext["basalisk.exec"], &exec)
			_ = json.Unmarshal(e.Ext["basalisk.client"], &client)
			_ = json.Unmarshal(e.Ext["basalisk.call"], &call)
			if exec != "client" || client != "c1" || call != "toolu_1" || string(e.Final) != `{"all":true}` {
				t.Errorf("call event: %+v", e)
			}
			asked = true
			if err := rt.ToolResult("someone-else", id, call, "x", false); !errors.Is(err, ErrNoRun) {
				t.Errorf("another owner's answer: %v", err)
			}
			if err := rt.ToolResult("u1", id, call, `[{"id":"w1"},{"id":"w2"}]`, false); err != nil {
				t.Error(err)
			}
		}
	})
	if !asked || last(evs).Verb != chatstream.VerbRunFinish {
		t.Fatalf("asked=%v ended %+v", asked, last(evs))
	}
	if got := textOf(evs); got != "Looking.Two windows." {
		t.Fatalf("text = %q", got)
	}
	// The second request carries the call and its result.
	sent, _ := json.Marshal(s.bodies[1]["messages"])
	for _, want := range []string{`"type":"tool_use"`, `"id":"toolu_1"`, `"type":"tool_result"`, `"tool_use_id":"toolu_1"`, `w2`} {
		if !strings.Contains(string(sent), want) {
			t.Errorf("second request lacks %s\n%s", want, sent)
		}
	}
	if tools, _ := json.Marshal(s.bodies[0]["tools"]); !strings.Contains(string(tools), `"name":"desktop_windows"`) {
		t.Errorf("tools: %s", tools)
	}
}

func TestAChangingToolAsksFirstAndRemembersTheAnswer(t *testing.T) {
	s := newStandIn(t,
		toolReply("toolu_1", "note_write", `{"text":"a"}`), textReply("Declined."),
		toolReply("toolu_2", "note_write", `{"text":"b"}`), textReply("Written."),
		toolReply("toolu_3", "note_write", `{"text":"c"}`), textReply("Written again."),
	)
	rt := newRuntime(t, s)
	turnOnce := func(decision string, wantAsk bool) (ran bool) {
		id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "write it", ClientID: "c1", Tools: []ToolSpec{changeTool}})
		if err != nil {
			t.Fatal(err)
		}
		askedFor := false
		evs := follow(t, rt, id, func(e chatstream.Event) {
			switch {
			case e.Verb == chatstream.VerbApprovalRequest:
				askedFor = true
				if e.Mode != chatstream.ApprovalInBand || !strings.Contains(string(e.Descriptor), `"tool":"note_write"`) {
					t.Errorf("approval: %+v", e)
				}
				if err := rt.Approve("u1", id, e.ApprovalID, decision); err != nil {
					t.Error(err)
				}
			case e.Verb == chatstream.VerbPartEnd && len(e.Ext) > 0:
				ran = true
				var call string
				_ = json.Unmarshal(e.Ext["basalisk.call"], &call)
				_ = rt.ToolResult("u1", id, call, "ok", false)
			}
		})
		if askedFor != wantAsk {
			t.Errorf("decision %q: asked=%v, want %v", decision, askedFor, wantAsk)
		}
		if last(evs).Verb != chatstream.VerbRunFinish {
			t.Fatalf("ended %+v", last(evs))
		}
		return ran
	}
	if turnOnce("deny", true) {
		t.Fatal("a declined tool ran")
	}
	if sent, _ := json.Marshal(s.bodies[1]["messages"]); !strings.Contains(string(sent), "declined") || !strings.Contains(string(sent), `"is_error":true`) {
		t.Errorf("the model was not told: %s", sent)
	}
	if !turnOnce("always", true) {
		t.Fatal("an allowed tool did not run")
	}
	if !turnOnce("deny", false) { // "always" stands: no question this time
		t.Fatal("a tool allowed for good did not run")
	}
	if g, _ := rt.Store().Grants(t.Context(), "u1"); len(g) != 1 || g[0] != "note_write" {
		t.Fatalf("grants: %v", g)
	}
}

func TestStoppingARunEndsItsStream(t *testing.T) {
	s := newStandIn(t, "hang")
	rt := newRuntime(t, s)
	id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "hi", ClientID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "again", ClientID: "c1"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("a second message while busy: %v", err)
	}
	evs := follow(t, rt, id, func(e chatstream.Event) {
		if e.Verb == chatstream.VerbMessageStart {
			if err := rt.Cancel("u1", id); err != nil {
				t.Error(err)
			}
		}
	})
	if last(evs).Verb != chatstream.VerbRunAbort {
		t.Fatalf("ended %+v", last(evs))
	}
}

func TestARefusedKeyIsReportedNotRetried(t *testing.T) {
	s := newStandIn(t, "status:401")
	rt := newRuntime(t, s)
	id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "hi", ClientID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	evs := follow(t, rt, id, nil)
	e := last(evs)
	if e.Verb != chatstream.VerbRunError || e.Retryable || !strings.Contains(e.Message, "401") || !strings.Contains(e.Message, "bad key") {
		t.Fatalf("ended %+v", e)
	}
	if strings.Contains(e.Message, "test-key") {
		t.Fatal("the key is in the error")
	}
	if len(s.bodies) != 1 {
		t.Fatalf("%d requests", len(s.bodies))
	}
	// The thread can be used again, and the failed message is still in it.
	s.mu.Lock()
	s.replies = []string{textReply("Back.")}
	s.mu.Unlock()
	id, err = rt.Start(t.Context(), StartInput{Owner: "u1", Text: "still there?", ClientID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if e := last(follow(t, rt, id, nil)); e.Verb != chatstream.VerbRunFinish {
		t.Fatalf("ended %+v", e)
	}
}

func TestAnUnansweredCallInAStoredThreadIsRepaired(t *testing.T) {
	in := map[string]any{}
	h := repair(nil)
	if len(h) != 0 {
		t.Fatal(h)
	}
	s := newStandIn(t, textReply("ok"))
	rt := newRuntime(t, s)
	thread, _ := rt.Store().MainThread(t.Context(), "u1")
	_, _ = rt.Store().Append(t.Context(), thread, "", "user", blocksText("do it"), nil)
	_, _ = rt.Store().Append(t.Context(), thread, "", "assistant", append(blocksText("ok"), toolUse("toolu_9", "x", &in)), nil)
	id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "and?", ClientID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	follow(t, rt, id, nil)
	sent, _ := json.Marshal(s.bodies[0]["messages"])
	if !strings.Contains(string(sent), `"tool_use_id":"toolu_9"`) || !strings.Contains(string(sent), "interrupted") {
		t.Fatalf("not repaired: %s", sent)
	}
}

func TestNoModelConfigured(t *testing.T) {
	rt := New(t.Context(), Config{}, nil, nil)
	if _, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "hi"}); !errors.Is(err, ErrNotReady) {
		t.Fatal(err)
	}
}

var runTool = ToolSpec{Name: "terminal_run", Description: "Run a command.", Level: LevelConfirm, InputSchema: map[string]any{"type": "object"}}

func TestAConfirmToolAsksEveryTimeWhateverWasAnswered(t *testing.T) {
	s := newStandIn(t,
		toolReply("toolu_1", "terminal_run", `{"command":"ls"}`), textReply("Ran."),
		toolReply("toolu_2", "terminal_run", `{"command":"rm x"}`), textReply("Ran again."),
	)
	rt := newRuntime(t, s)
	for i, decision := range []string{"always", "session"} {
		id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "run it", ClientID: "c1", Tools: []ToolSpec{runTool}})
		if err != nil {
			t.Fatal(err)
		}
		asked, ran := false, false
		follow(t, rt, id, func(e chatstream.Event) {
			switch {
			case e.Verb == chatstream.VerbApprovalRequest:
				asked = true
				if !strings.Contains(string(e.Descriptor), `"standing":false`) {
					t.Errorf("a standing answer is on offer: %s", e.Descriptor)
				}
				_ = rt.Approve("u1", id, e.ApprovalID, decision)
			case e.Verb == chatstream.VerbPartEnd && len(e.Ext) > 0:
				ran = true
				var call string
				_ = json.Unmarshal(e.Ext["basalisk.call"], &call)
				_ = rt.ToolResult("u1", id, call, "ok", false)
			}
		})
		if !asked || !ran {
			t.Fatalf("turn %d: asked=%v ran=%v", i, asked, ran)
		}
	}
	if g, _ := rt.Store().Grants(t.Context(), "u1"); len(g) != 0 {
		t.Fatalf("a grant was kept: %v", g)
	}
}

func TestACallIsGivenUpOnlyWhenThePageGoesQuiet(t *testing.T) {
	old := toolQuiet
	toolQuiet = 150 * time.Millisecond
	t.Cleanup(func() { toolQuiet = old })
	s := newStandIn(t,
		toolReply("toolu_1", "desktop_windows", `{}`), textReply("Slow but there."),
		toolReply("toolu_2", "desktop_windows", `{}`), textReply("Gone."),
	)
	rt := newRuntime(t, s)

	// A page that keeps saying "still at it" is waited for well past the quiet limit.
	id, _ := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "look", ClientID: "c1", Tools: []ToolSpec{lookTool}})
	follow(t, rt, id, func(e chatstream.Event) {
		if e.Verb == chatstream.VerbPartEnd && len(e.Ext) > 0 {
			go func() {
				for range 6 {
					time.Sleep(60 * time.Millisecond)
					if err := rt.ToolAlive("u1", id, "toolu_1"); err != nil {
						t.Errorf("alive: %v", err)
					}
				}
				if err := rt.ToolResult("u1", id, "toolu_1", "late but fine", false); err != nil {
					t.Errorf("result: %v", err)
				}
			}()
		}
	})
	if sent, _ := json.Marshal(s.bodies[1]["messages"]); !strings.Contains(string(sent), "late but fine") {
		t.Fatalf("the answer did not reach the model: %s", sent)
	}

	// A page that says nothing is given up on, and its late answer is refused.
	id, _ = rt.Start(t.Context(), StartInput{Owner: "u1", Text: "look again", ClientID: "c1", Tools: []ToolSpec{lookTool}})
	follow(t, rt, id, nil)
	if sent, _ := json.Marshal(s.bodies[3]["messages"]); !strings.Contains(string(sent), "did not answer") {
		t.Fatalf("the model was not told: %s", sent)
	}
	if err := rt.ToolAlive("u1", id, "toolu_2"); err == nil {
		t.Fatal("a call that was given up on still counts as waiting")
	}
}
