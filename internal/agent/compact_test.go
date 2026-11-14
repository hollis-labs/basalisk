package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/libs/ui/ui-go/chatstream"
)

func textBlocks(text string) []llmtypes.ContentBlock {
	return []llmtypes.ContentBlock{{Type: "text", Text: text}}
}

// bigReply is a reply whose request counted as large.
func bigReply(text string) string {
	return strings.Replace(textReply(text), `"input_tokens":10`, `"input_tokens":130000`, 1)
}

func compactStates(evs []chatstream.Event) []string {
	var out []string
	for _, e := range evs {
		if e.Verb == chatstream.VerbActivity && e.Kind == "basalisk.compact" {
			var v struct{ State string }
			_ = json.Unmarshal(e.Value, &v)
			out = append(out, v.State)
		}
	}
	return out
}

func say(t *testing.T, rt *Runtime, text string) []chatstream.Event {
	t.Helper()
	id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: text, ClientID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	return follow(t, rt, id, nil)
}

func TestALongThreadIsSummarisedForTheModelAndNothingIsDeleted(t *testing.T) {
	long := strings.Repeat("The quick brown fox. ", 3000) // about 63,000 characters a message
	s := newStandIn(t,
		textReply("First answer."),
		bigReply("Second answer."),
		// The third message: the thread is summarised first, then answered.
		textReply("SUMMARY: the owner asked about foxes twice; nothing was changed."),
		bigReply("Third answer."),
		// The fourth: summarised again, the earlier summary with what came since.
		textReply("SUMMARY 2: foxes three times."),
		textReply("Fourth answer."),
	)
	rt := newRuntime(t, s)
	say(t, rt, "one "+long)
	if got := compactStates(say(t, rt, "two "+long)); len(got) != 0 {
		t.Fatalf("compacted before the thread was long: %v", got)
	}
	evs := say(t, rt, "three "+long)
	if got := compactStates(evs); strings.Join(got, ",") != "start,done" {
		t.Fatalf("compaction: %v", got)
	}
	if textOf(evs) != "Third answer." {
		t.Fatalf("said %q", textOf(evs))
	}
	// The request to summarise: its own instructions, no tools, the older messages as text.
	sumReq := s.bodies[2]
	if sys, _ := json.Marshal(sumReq["system"]); !strings.Contains(string(sys), "running summary of a long conversation") {
		t.Errorf("summary request system: %.200s", sys)
	}
	if sumReq["tools"] != nil {
		t.Errorf("the summary request carried tools")
	}
	if text := lastUserText(t, sumReq); !strings.Contains(text, "Owner: one The quick") || !strings.Contains(text, "Agent: First answer.") {
		t.Errorf("what was summarised: %.300s", text)
	}
	// The answer's request: the summary, then only the recent messages.
	ans, _ := json.Marshal(s.bodies[3]["messages"])
	if !strings.Contains(string(ans), "SUMMARY: the owner asked about foxes twice") || strings.Contains(string(ans), "one The quick") || !strings.Contains(string(ans), "three The quick") {
		t.Errorf("the answer's request does not have the summary in place of the old messages")
	}
	if msgs, _ := s.bodies[3]["messages"].([]any); len(msgs) == 0 || msgs[0].(map[string]any)["role"] != "user" {
		t.Errorf("the request does not start with the owner")
	}
	// Nothing is deleted: the thread still has every message.
	thread, _ := rt.Store().MainThread(t.Context(), "u1")
	stored, _ := rt.Store().Messages(t.Context(), thread)
	if len(stored) != 6 {
		t.Fatalf("stored messages = %d", len(stored))
	}
	summary, upto := rt.Store().summary(t.Context(), thread)
	if !strings.HasPrefix(summary, "SUMMARY:") || upto < 2 || upto >= 5 {
		t.Fatalf("summary upto %d: %.60s", upto, summary)
	}
	// Again, later: the earlier summary is summarised with what came since.
	evs = say(t, rt, "four "+long)
	if got := compactStates(evs); strings.Join(got, ",") != "start,done" {
		t.Fatalf("second compaction: %v", got)
	}
	if text := lastUserText(t, s.bodies[4]); !strings.Contains(text, "Summary of the conversation before this part:\nSUMMARY: the owner asked") {
		t.Errorf("the second summary did not build on the first: %.200s", text)
	}
	if summary, upto2 := rt.Store().summary(t.Context(), thread); !strings.HasPrefix(summary, "SUMMARY 2") || upto2 <= upto {
		t.Errorf("second summary upto %d (was %d): %.40s", upto2, upto, summary)
	}
}

func TestAFailedSummaryLeavesTheThreadAsItWas(t *testing.T) {
	long := strings.Repeat("Lorem ipsum dolor. ", 3500)
	s := newStandIn(t, textReply("One."), bigReply("Two."), "status:401", textReply("Three anyway."))
	rt := newRuntime(t, s)
	say(t, rt, "one "+long)
	say(t, rt, "two "+long)
	evs := say(t, rt, "three "+long)
	if got := compactStates(evs); strings.Join(got, ",") != "start,failed" {
		t.Fatalf("compaction: %v", got)
	}
	if textOf(evs) != "Three anyway." {
		t.Fatalf("the message was not answered: %q (%+v)", textOf(evs), last(evs))
	}
	thread, _ := rt.Store().MainThread(t.Context(), "u1")
	if summary, upto := rt.Store().summary(t.Context(), thread); summary != "" || upto != 0 {
		t.Errorf("a summary was kept: %d %q", upto, summary)
	}
	if sent, _ := json.Marshal(s.bodies[3]["messages"]); !strings.Contains(string(sent), "one Lorem") {
		t.Errorf("the whole thread was not sent after the failure")
	}
}

func TestWhereAThreadIsCut(t *testing.T) {
	owner := func(seq int, n int) Message {
		meta, _ := json.Marshal(map[string]string{"text": "x"})
		return Message{Seq: seq, Role: "user", Meta: meta, Blocks: textBlocks(strings.Repeat("a", n))}
	}
	agent := func(seq int, n int) Message {
		return Message{Seq: seq, Role: "assistant", Meta: json.RawMessage("{}"), Blocks: textBlocks(strings.Repeat("b", n))}
	}
	results := func(seq int) Message {
		return Message{Seq: seq, Role: "user", Meta: json.RawMessage("{}"), Blocks: textBlocks("r")}
	}
	thread := []Message{owner(1, 100), agent(2, 100), owner(3, 100), agent(4, 100), results(5), agent(6, 100), owner(7, 100), agent(8, 100), owner(9, 100)}
	for _, c := range []struct {
		upto, keep, want int
	}{
		{0, 150, 9},   // room for the last turn only
		{0, 350, 7},   // the last two
		{0, 10000, 3}, // everything fits, but something must be summarised: all but the first turn is kept
		{2, 10000, 7}, // the first turn is already summarised: the next goes
		{6, 10000, 9},
		{8, 10000, 0}, // one turn left: nothing to summarise
	} {
		if got := compactCut(thread, c.upto, c.keep); got != c.want {
			t.Errorf("upto %d keep %d: cut at %d, want %d", c.upto, c.keep, got, c.want)
		}
	}
	// The cut is always at one of the owner's messages, so no call is parted from its result.
	h := historyFor(thread, "S", 6)
	if len(h) != 3 || h[0].Role != "user" || !strings.HasPrefix(h[0].ContentBlocks[0].Text, "<summary>\nS") || len(h[0].ContentBlocks) != 2 {
		t.Errorf("history after a summary: %+v", h)
	}
}
