package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
)

func TestFocusHistory(t *testing.T) {
	seq := 0
	var msgs []Message
	turn := func(slot, said string, pad int) {
		seq++
		meta, _ := json.Marshal(map[string]any{"text": said, "slot": slot})
		msgs = append(msgs, Message{Seq: seq, Role: "user", Meta: meta, Blocks: []llmtypes.ContentBlock{{Type: "text", Text: said}}})
		seq++
		msgs = append(msgs, Message{Seq: seq, Role: "assistant", Blocks: []llmtypes.ContentBlock{{Type: "text", Text: "re: " + said + strings.Repeat(".", pad)}, {Type: "tool_use", ID: "c" + said, Name: "x"}}})
		seq++
		msgs = append(msgs, Message{Seq: seq, Role: "user", Blocks: []llmtypes.ContentBlock{{Type: "tool_result", ToolUseID: "c" + said, Content: "ok"}}})
	}
	// A short thread is sent whole.
	turn("planning", "p1", 10)
	turn("writing", "w1", 10)
	if got := focusHistory(msgs, 0, "writing"); len(got) != len(msgs) {
		t.Fatalf("a short thread is not focused: %d of %d", len(got), len(msgs))
	}
	// A long one keeps the current context and the last three turns.
	for _, s := range []string{"p2", "p3"} {
		turn("planning", s, 20000)
	}
	turn("writing", "w2", 20000)
	turn("engineering", "e1", 100)
	turn("planning", "p4", 100)
	turn("writing", "w3", 100)
	turn("writing", "w4", 100)
	got := focusHistory(msgs, 0, "writing")
	var said []string
	elsewhere := ""
	for _, m := range got {
		if ownerTurn(m) {
			var meta struct{ Text string }
			_ = json.Unmarshal(m.Meta, &meta)
			said = append(said, meta.Text)
			if strings.HasPrefix(m.Blocks[0].Text, "<elsewhere>") {
				elsewhere += meta.Text + ":" + m.Blocks[0].Text + "|"
			}
		}
	}
	if strings.Join(said, ",") != "w1,w2,p4,w3,w4" {
		t.Fatalf("kept turns: %v", said)
	}
	if !strings.Contains(elsewhere, "w1:") || !strings.Contains(elsewhere, "1 in planning") || !strings.Contains(elsewhere, "w2:") || !strings.Contains(elsewhere, "2 in planning") || !strings.Contains(elsewhere, "p4:") || !strings.Contains(elsewhere, "1 in engineering") {
		t.Fatalf("what was left out is said in front of the next kept turn: %s", elsewhere)
	}
	// No call is parted from its result.
	uses, results := 0, 0
	for _, m := range got {
		for _, b := range m.Blocks {
			if b.Type == "tool_use" {
				uses++
			}
			if b.Type == "tool_result" {
				results++
			}
		}
	}
	if uses != results || uses != 5 {
		t.Fatalf("calls %d, results %d", uses, results)
	}
	// The stored messages are not changed.
	if strings.HasPrefix(msgs[3].Blocks[0].Text, "<elsewhere>") {
		t.Fatal("the stored thread was written to")
	}
	// What the summary covers is left out as before.
	if got := focusHistory(msgs, 6, ""); got[0].Seq != 7 {
		t.Fatalf("starts after the summary: %d", got[0].Seq)
	}
}
