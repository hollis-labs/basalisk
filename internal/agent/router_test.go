package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/ui/ui-go/chatstream"
)

var testHints = Hints{
	Apps:     []Named{{ID: "notes", Title: "Notes"}, {ID: "terminal", Title: "Terminal"}, {ID: "browser", Title: "Browser"}},
	Commands: []Named{{ID: "layout.toggle", Title: "Tile windows"}, {ID: "notes.new", Title: "New note"}, {ID: "a.same", Title: "Twice"}, {ID: "b.same", Title: "Twice"}},
}

func TestTheRulesThatShip(t *testing.T) {
	r := NewRouter("")
	cases := []struct {
		text  string
		rule  string // "" : the model answers
		tool  string
		input string
		say   string
	}{
		{"/open notes", "open-app", "desktop_open", `{"app":"notes"}`, "Opened Notes."},
		{"/open Terminal", "open-app", "desktop_open", `{"app":"terminal"}`, "Opened Terminal."},
		{"/terminal", "open-app-named", "desktop_open", `{"app":"terminal"}`, "Opened Terminal."},
		{"/Notes", "", "", "", ""}, // a slash command is lower case
		{"/notes", "open-app-named", "desktop_open", `{"app":"notes"}`, "Opened Notes."},
		{"/notes about tiling", "", "", "", ""},
		{"open notes", "open-app-said", "desktop_open", `{"app":"notes"}`, "Opened Notes."},
		{"Open the Browser.", "open-app-said", "desktop_open", `{"app":"browser"}`, "Opened Browser."},
		{"launch a terminal", "open-app-said", "desktop_open", `{"app":"terminal"}`, "Opened Terminal."},
		{"Tile windows", "command-said", "desktop_run_command", `{"command":"layout.toggle"}`, "Done: Tile windows."},
		{"/do new note", "command", "desktop_run_command", `{"command":"notes.new"}`, "Done: New note."},
		{"/do layout.toggle", "command", "desktop_run_command", `{"command":"layout.toggle"}`, "Done: Tile windows."},
		{"/run ls -la ~/Notes", "run", "terminal_run", `{"command":"ls -la ~/Notes"}`, "Ran it in the terminal."},
		// Not for the rules: the model answers.
		{"open notes.md", "", "", "", ""},
		{"open the note about tiling windows", "", "", "", ""},
		{"what is open?", "", "", "", ""},
		{"/open", "", "", "", ""},
		{"/open nothing-like-this", "", "", "", ""},
		{"/run", "", "", "", ""},
		{"/Volumes/disk/notes what is in here", "", "", "", ""},
		{"/etc/hosts", "", "", "", ""},
		{"twice", "", "", "", ""}, // two commands share the title: not guessed between
		{"tile windows and then open notes", "", "", "", ""},
	}
	for _, c := range cases {
		got := r.Route(t.Context(), c.text, nil, testHints)
		if c.rule == "" {
			if got != nil {
				t.Errorf("%q: routed to %s, want the model", c.text, got.Rule)
			}
			continue
		}
		if got == nil {
			t.Errorf("%q: not routed, want %s", c.text, c.rule)
			continue
		}
		input, _ := json.Marshal(got.Input)
		if got.Rule != c.rule || got.Tool != c.tool || string(input) != c.input || got.Say != c.say {
			t.Errorf("%q: got rule %s tool %s input %s say %q", c.text, got.Rule, got.Tool, input, got.Say)
		}
	}
	help := r.Route(t.Context(), "/help", nil, testHints)
	if help == nil || help.Tool != "" || !strings.Contains(help.Say, "/open <app>") || !strings.Contains(help.Say, "/new") {
		t.Errorf("help: %+v", help)
	}
}

func TestTheOwnersRulesComeFirstAndABrokenFileIsSaidNotUsed(t *testing.T) {
	file := filepath.Join(t.TempDir(), "agent-rules.json")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"version":"mine-1","strategy":"first_match","rules":[
		{"id":"my-open","when":[{"field":"slash","op":"eq","value":"open"},{"field":"app","op":"exists"}],"target":"tool:desktop_focus_app","params":{"app":"{app}","_say":"Mine."},"auto":true},
		{"id":"standup","when":[{"field":"slash","op":"eq","value":"standup"}],"target":"say","params":{"_say":"Notes, then tasks."},"auto":true}]}`)
	r := NewRouter(file)
	if got := r.Route(t.Context(), "/open notes", nil, testHints); got == nil || got.Rule != "my-open" || got.Say != "Mine." {
		t.Errorf("the owner's rule did not win: %+v", got)
	}
	if got := r.Route(t.Context(), "/standup", nil, testHints); got == nil || got.Tool != "" || got.Say != "Notes, then tasks." {
		t.Errorf("say rule: %+v", got)
	}
	if got := r.Route(t.Context(), "open notes", nil, testHints); got == nil || got.Rule != "open-app-said" {
		t.Errorf("the shipped rules are still there: %+v", got)
	}
	if p := r.Problem(); p != "" {
		t.Errorf("problem: %s", p)
	}
	// A file that does not parse is left out and said so; the shipped rules go on working.
	write(`{"version":"mine-2","strategy":"first_match","rules":[{"id":"x","when":[{"field":"slash","op":"nearly","value":"y"}],"target":"say"}]} `)
	if got := r.Route(t.Context(), "/open notes", nil, testHints); got == nil || got.Rule != "open-app" {
		t.Errorf("after a broken file: %+v", got)
	}
	if p := r.Problem(); !strings.Contains(p, "agent-rules.json") {
		t.Errorf("problem not reported: %q", p)
	}
}

var openTool = ToolSpec{Name: "desktop_open", Description: "Open an app.", Level: LevelAct, InputSchema: map[string]any{"type": "object"}}
var commandTool = ToolSpec{Name: "desktop_run_command", Description: "Run a command.", InputSchema: map[string]any{"type": "object"}} // asks

// answerCalls answers every call the page is asked to run, and every permission question.
func answerCalls(t *testing.T, rt *Runtime, id, result string, isError bool, decision string) func(chatstream.Event) {
	return func(e chatstream.Event) {
		if e.Verb == chatstream.VerbApprovalRequest {
			if err := rt.Approve("u1", id, e.ApprovalID, decision); err != nil {
				t.Error(err)
			}
		}
		if e.Verb == chatstream.VerbPartEnd && len(e.Ext) > 0 {
			var call string
			_ = json.Unmarshal(e.Ext["basalisk.call"], &call)
			if err := rt.ToolResult("u1", id, call, result, isError); err != nil {
				t.Error(err)
			}
		}
	}
}

func TestARoutedMessageRunsItsToolWithoutTheModel(t *testing.T) {
	s := newStandIn(t, textReply("And then the model."))
	rt := newRuntime(t, s)
	rt.SetRouter(NewRouter(""))
	id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "open notes", ClientID: "c1", Tools: []ToolSpec{openTool, lookTool}, Hints: testHints})
	if err != nil {
		t.Fatal(err)
	}
	var routed string
	evs := follow(t, rt, id, func(e chatstream.Event) {
		if e.Verb == chatstream.VerbActivity && e.Kind == "basalisk.route" {
			routed = string(e.Value)
		}
		if e.Verb == chatstream.VerbPartEnd && len(e.Ext) > 0 && string(e.Final) != `{"app":"notes"}` {
			t.Errorf("call input: %s", e.Final)
		}
		answerCalls(t, rt, id, "Opened notes.", false, "once")(e)
	})
	if last(evs).Verb != chatstream.VerbRunFinish || textOf(evs) != "Opened Notes." {
		t.Fatalf("ended %+v, said %q", last(evs), textOf(evs))
	}
	if !strings.Contains(routed, `"rule":"open-app-said"`) {
		t.Errorf("route event: %s", routed)
	}
	if len(s.bodies) != 0 {
		t.Fatalf("the model was called %d times", len(s.bodies))
	}
	// The thread has it as an ordinary call, so the model knows later what was done.
	thread, _ := rt.Store().MainThread(t.Context(), "u1")
	msgs, _ := rt.Store().Messages(t.Context(), thread)
	if len(msgs) != 4 || msgs[1].Blocks[0].Type != "tool_use" || msgs[2].Blocks[0].Type != "tool_result" || msgs[3].Blocks[0].Text != "Opened Notes." {
		t.Fatalf("thread: %+v", msgs)
	}
	var rule string
	_ = rt.Store().db.QueryRow(`SELECT route FROM runs WHERE id=?`, id).Scan(&rule)
	if rule != "open-app-said" {
		t.Errorf("run's route = %q", rule)
	}
	// The next message goes to the model, with the routed call in what it is sent.
	id, err = rt.Start(t.Context(), StartInput{Owner: "u1", Text: "and what did you just do?", ClientID: "c1", Tools: []ToolSpec{openTool, lookTool}, Hints: testHints})
	if err != nil {
		t.Fatal(err)
	}
	follow(t, rt, id, nil)
	if len(s.bodies) != 1 {
		t.Fatalf("model calls = %d", len(s.bodies))
	}
	if sent, _ := json.Marshal(s.bodies[0]["messages"]); !strings.Contains(string(sent), `"name":"desktop_open"`) || !strings.Contains(string(sent), "Opened Notes.") {
		t.Errorf("the model was not sent the routed call: %s", sent)
	}
}

func TestARoutedCallThatFailsGoesToTheModel(t *testing.T) {
	s := newStandIn(t, textReply("That app would not open; here is why."))
	rt := newRuntime(t, s)
	rt.SetRouter(NewRouter(""))
	id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "/open notes", ClientID: "c1", Tools: []ToolSpec{openTool}, Hints: testHints})
	if err != nil {
		t.Fatal(err)
	}
	evs := follow(t, rt, id, answerCalls(t, rt, id, "The window could not be made.", true, "once"))
	if last(evs).Verb != chatstream.VerbRunFinish || textOf(evs) != "That app would not open; here is why." {
		t.Fatalf("ended %+v, said %q", last(evs), textOf(evs))
	}
	if len(s.bodies) != 1 {
		t.Fatalf("model calls = %d", len(s.bodies))
	}
	if sent, _ := json.Marshal(s.bodies[0]["messages"]); !strings.Contains(string(sent), "The window could not be made.") {
		t.Errorf("the model was not told what failed: %s", sent)
	}
}

func TestARoutedCallAsksLikeAnyOtherAndANoEndsIt(t *testing.T) {
	s := newStandIn(t)
	rt := newRuntime(t, s)
	rt.SetRouter(NewRouter(""))
	id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "tile windows", ClientID: "c1", Tools: []ToolSpec{commandTool}, Hints: testHints})
	if err != nil {
		t.Fatal(err)
	}
	asked, ran := false, false
	evs := follow(t, rt, id, func(e chatstream.Event) {
		if e.Verb == chatstream.VerbApprovalRequest {
			asked = true
		}
		if e.Verb == chatstream.VerbPartEnd && len(e.Ext) > 0 {
			ran = true
		}
		answerCalls(t, rt, id, "x", false, "deny")(e)
	})
	if !asked || ran || textOf(evs) != "Not done." || len(s.bodies) != 0 {
		t.Fatalf("asked=%v ran=%v said %q model calls %d", asked, ran, textOf(evs), len(s.bodies))
	}
}

func TestARuleWhoseToolIsNotOfferedIsNoRoute(t *testing.T) {
	s := newStandIn(t, textReply("I cannot open apps here."))
	rt := newRuntime(t, s)
	rt.SetRouter(NewRouter(""))
	id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "open notes", ClientID: "c1", Tools: []ToolSpec{lookTool}, Hints: testHints})
	if err != nil {
		t.Fatal(err)
	}
	evs := follow(t, rt, id, nil)
	if textOf(evs) != "I cannot open apps here." || len(s.bodies) != 1 {
		t.Fatalf("said %q, model calls %d", textOf(evs), len(s.bodies))
	}
}
