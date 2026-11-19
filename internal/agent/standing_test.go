package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// systemText is the system prompt a request carried, however it was encoded.
func systemText(t *testing.T, body map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(body["system"])
	if err != nil {
		t.Fatal(err)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &blocks)
	var b strings.Builder
	for _, x := range blocks {
		b.WriteString(x.Text)
	}
	return b.String()
}

func standingFile(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "standing.md")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheStandingRecordIsSentAfterANewConversation(t *testing.T) {
	s := newStandIn(t, textReply("One."), textReply("Two."))
	rt := newRuntime(t, s)
	if ok, err := rt.ImportStanding(t.Context(), standingFile(t, "For a delegation, say only: Delivered and it is being executed.")); !ok || err != nil {
		t.Fatalf("seed: %v %v", ok, err)
	}
	say(t, rt, "hello")
	if err := rt.Store().ArchiveMain(t.Context(), "u1"); err != nil {
		t.Fatal(err)
	}
	say(t, rt, "how do you report a delegation?")
	if len(s.bodies) != 2 {
		t.Fatalf("%d requests", len(s.bodies))
	}
	for i, body := range s.bodies {
		if sys := systemText(t, body); !strings.Contains(sys, "<standing>\nFor a delegation, say only: Delivered and it is being executed.\n</standing>") {
			t.Fatalf("request %d did not carry the standing record: ...%s", i+1, sys[max(0, len(sys)-300):])
		}
	}
	// The second request is the first of a new thread: only the one message in it.
	if msgs, _ := s.bodies[1]["messages"].([]any); len(msgs) != 1 {
		t.Fatalf("the new conversation's first request had %d messages", len(msgs))
	}
}

func TestTheStandingRecordIsSeededOnceAndNeverOverwritten(t *testing.T) {
	rt := newRuntime(t, newStandIn(t))
	if ok, err := rt.ImportStanding(t.Context(), filepath.Join(t.TempDir(), "absent.md")); ok || err != nil {
		t.Fatalf("no file: %v %v", ok, err)
	}
	if text, rev := rt.Store().Standing(t.Context()); text != "" || rev != 0 {
		t.Fatalf("a record without a file: %d %q", rev, text)
	}
	if ok, err := rt.ImportStanding(t.Context(), standingFile(t, "first rules")); !ok || err != nil {
		t.Fatalf("seed: %v %v", ok, err)
	}
	if ok, err := rt.ImportStanding(t.Context(), standingFile(t, "other rules")); ok || err != nil {
		t.Fatalf("a second start seeded again: %v %v", ok, err)
	}
	if text, rev := rt.Store().Standing(t.Context()); text != "first rules" || rev != 1 {
		t.Fatalf("record: %d %q", rev, text)
	}
}

func TestStandingUpdateChangesWhatTheNextRequestCarries(t *testing.T) {
	s := newStandIn(t,
		toolReply("toolu_1", "standing_update", `{"text":"Old rule one.\nNew rule: questions go on cards.","why":"the owner asked for cards"}`),
		textReply("Kept."),
		textReply("Hello."),
	)
	rt := newRuntime(t, s)
	if _, err := rt.Store().SetStanding(t.Context(), "Old rule one.", "", "owner"); err != nil {
		t.Fatal(err)
	}
	say(t, rt, "from now on, ask me questions on cards")
	say(t, rt, "hi")
	if len(s.bodies) != 3 {
		t.Fatalf("%d requests", len(s.bodies))
	}
	if sys := systemText(t, s.bodies[0]); strings.Contains(sys, "questions go on cards") {
		t.Fatalf("the rule was there before it was set")
	}
	if sys := systemText(t, s.bodies[2]); !strings.Contains(sys, "Old rule one.\nNew rule: questions go on cards.") {
		t.Fatalf("the next request did not carry the new record: ...%s", sys[max(0, len(sys)-300):])
	}
	if text, rev := rt.Store().Standing(t.Context()); rev != 2 || !strings.HasSuffix(text, "questions go on cards.") {
		t.Fatalf("record: %d %q", rev, text)
	}
}

func TestStandingUpdateRefusesToDropMostOfTheRecord(t *testing.T) {
	rt := newRuntime(t, newStandIn(t))
	if _, err := rt.Store().SetStanding(t.Context(), strings.Repeat("A rule that still holds. ", 20), "", "owner"); err != nil {
		t.Fatal(err)
	}
	tool := rt.standingTools()[0]
	if _, err := tool.Run(t.Context(), map[string]any{"text": "Only the new rule.", "why": "x"}); err == nil || !strings.Contains(err.Error(), "carry forward") {
		t.Fatalf("a much shorter record was kept: %v", err)
	}
	if _, rev := rt.Store().Standing(t.Context()); rev != 1 {
		t.Fatalf("rev %d", rev)
	}
}

func TestTheRunningThreadRepliesWhenItRunsOutOfSteps(t *testing.T) {
	s := newStandIn(t,
		toolReply("toolu_1", "no_such_tool", `{}`),
		toolReply("toolu_2", "no_such_tool", `{}`),
		// Out of steps: one more request, with no tools, for the reply.
		textReply("I wrote the brief; the review is not sent yet. Say go on."),
	)
	rt := newRuntime(t, s)
	rt.cfg.MaxSteps = 2
	id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "write the brief and send it for review", ClientID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	evs := follow(t, rt, id, nil)
	if len(s.bodies) != 3 {
		t.Fatalf("%d requests were made, want 3", len(s.bodies))
	}
	lastBody := s.bodies[2]
	if list, _ := lastBody["tools"].([]any); len(list) > 0 {
		t.Errorf("the reply request offered tools")
	}
	if sent := lastUserText(t, lastBody); !strings.Contains(sent, "Reply to the owner now") {
		t.Errorf("the agent was not asked for its reply: %q", sent)
	}
	if got := textOf(evs); !strings.Contains(got, "I wrote the brief; the review is not sent yet. Say go on.") {
		t.Errorf("the owner was not shown the reply: %q", got)
	}
	thread := mustMain(t, rt, "u1")
	msgs, err := rt.Store().Messages(t.Context(), thread)
	if err != nil {
		t.Fatal(err)
	}
	final := msgs[len(msgs)-1]
	if final.Role != "assistant" || len(final.Blocks) != 1 || !strings.HasPrefix(final.Blocks[0].Text, "I wrote the brief") {
		t.Fatalf("the thread does not end with the reply: %+v", final)
	}
	var reason string
	_ = rt.Store().db.QueryRow(`SELECT reason FROM runs WHERE id=?`, id).Scan(&reason)
	if reason != "turn_limit" {
		t.Errorf("run reason %q", reason)
	}
}

func TestARunKeepsWhatItWasSent(t *testing.T) {
	s := newStandIn(t, textReply("One."), textReply("Two."))
	rt := newRuntime(t, s)
	if _, err := rt.Store().SetStanding(t.Context(), "A standing rule.", "", "owner"); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, text := range []string{"one", "two"} {
		id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: text, ClientID: "c1"})
		if err != nil {
			t.Fatal(err)
		}
		follow(t, rt, id, nil)
		ids = append(ids, id)
	}
	system, tools, err := rt.Store().RunInputs(t.Context(), ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if system != systemText(t, s.bodies[0]) {
		t.Fatalf("the kept prompt is not the one sent")
	}
	for _, want := range []string{"standing_update", "task_start", "ask_owner"} {
		if !slices.Contains(tools, want) {
			t.Errorf("tool %s not recorded: %v", want, tools)
		}
	}
	// The same prompt twice is stored once.
	var n int
	_ = rt.Store().db.QueryRow(`SELECT COUNT(*) FROM prompts`).Scan(&n)
	if n != 1 {
		t.Errorf("%d prompts stored for one distinct text", n)
	}
	if _, _, err := rt.Store().RunInputs(t.Context(), "run_nope"); err == nil {
		t.Errorf("a missing run had inputs")
	}
}
