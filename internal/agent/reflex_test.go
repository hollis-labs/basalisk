package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/ui/ui-go/chatstream"
)

func reflexFile(t *testing.T, body string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "agent-reflexes.json")
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

// lastUserText is the text of the last message of a request to the model.
func lastUserText(t *testing.T, body map[string]any) string {
	t.Helper()
	msgs, _ := body["messages"].([]any)
	last, _ := msgs[len(msgs)-1].(map[string]any)
	var b strings.Builder
	blocks, _ := last["content"].([]any)
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		text, _ := block["text"].(string)
		b.WriteString(text + "\n")
	}
	return b.String()
}

func reflexNames(evs []chatstream.Event) []string {
	var out []string
	for _, e := range evs {
		if e.Verb == chatstream.VerbActivity && e.Kind == "basalisk.reflex" {
			var v struct{ Name string }
			_ = json.Unmarshal(e.Value, &v)
			out = append(out, v.Name)
		}
	}
	return out
}

func TestAReminderReflexIsToldToTheModelAndThenKeepsQuietForItsCooldown(t *testing.T) {
	s := newStandIn(t, textReply("Deployed."), textReply("Again."))
	rt := newRuntime(t, s)
	rt.SetReflexes(NewReflexes(reflexFile(t, `{"reflexes":[{"name":"deploys","trigger":{"kind":"user_regex_window","window":1,"pattern":"(?i)\\bdeploy\\b"},
		"action_kind":"inject_reminder","action":{"body":"Deploys go through Cerberus."},"cooldown_seconds":600}]}`), rt.Store()))
	if p := rt.Reflexes().Problem(); p != "" {
		t.Fatal(p)
	}
	id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "please deploy it", ClientID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	evs := follow(t, rt, id, nil)
	if got := reflexNames(evs); len(got) != 1 || got[0] != "deploys" {
		t.Fatalf("fired: %v", got)
	}
	if sent := lastUserText(t, s.bodies[0]); !strings.Contains(sent, "please deploy it\n<reminder>\nDeploys go through Cerberus.\n</reminder>") {
		t.Fatalf("the model was not told: %s", sent)
	}
	// What the panel shows as the owner's message is what they typed.
	thread, _ := rt.Store().MainThread(t.Context(), "u1")
	msgs, _ := rt.Store().Messages(t.Context(), thread)
	if !strings.Contains(string(msgs[0].Meta), `"text":"please deploy it"`) || !strings.Contains(string(msgs[0].Meta), `"reflexes":["deploys"]`) {
		t.Errorf("meta: %s", msgs[0].Meta)
	}
	// Within its cooldown it does not fire again.
	id, _ = rt.Start(t.Context(), StartInput{Owner: "u1", Text: "deploy the other one too", ClientID: "c1"})
	if got := reflexNames(follow(t, rt, id, nil)); len(got) != 0 {
		t.Fatalf("fired inside its cooldown: %v", got)
	}
	if strings.Count(lastUserText(t, s.bodies[1]), "reminder") != 0 {
		t.Errorf("a reminder was sent inside the cooldown")
	}
	// A message it does not match never fired it.
	if n, _ := rt.Store().reflexFired(t.Context(), "deploys"); n != 1 {
		t.Errorf("fired count = %d", n)
	}
}

func TestAHaltReflexAnswersWithoutTheModel(t *testing.T) {
	s := newStandIn(t)
	rt := newRuntime(t, s)
	rt.SetReflexes(NewReflexes(reflexFile(t, `{"reflexes":[{"name":"no-prod","trigger":{"kind":"AND","clauses":[
		{"kind":"user_regex_window","window":1,"pattern":"(?i)drop table"},{"kind":"attr","key":"focused.app","value":"terminal"}]},
		"action_kind":"halt_session","action":{"message":"Not from here. Do that one yourself."}}]}`), rt.Store()))
	ctxTerminal := json.RawMessage(`{"focused":{"winId":"w1","app":"terminal","title":"zsh"},"windows":[]}`)
	id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "run DROP TABLE users", ClientID: "c1", Context: ctxTerminal})
	if err != nil {
		t.Fatal(err)
	}
	evs := follow(t, rt, id, nil)
	if textOf(evs) != "Not from here. Do that one yourself." || len(s.bodies) != 0 || last(evs).Verb != chatstream.VerbRunFinish {
		t.Fatalf("said %q, model calls %d, ended %+v", textOf(evs), len(s.bodies), last(evs))
	}
	var rule string
	_ = rt.Store().db.QueryRow(`SELECT route FROM runs WHERE id=?`, id).Scan(&rule)
	if rule != "reflex:no-prod" {
		t.Errorf("run's route = %q", rule)
	}
}

func TestTheLongThreadReflexFiresOnceTheLastRequestWasLarge(t *testing.T) {
	big := strings.Replace(textReply("Done."), `"input_tokens":10`, `"input_tokens":160000`, 1)
	s := newStandIn(t, textReply("Short."), big, textReply("Noted."))
	rt := newRuntime(t, s)
	rt.cfg.CompactAt = -1 // the reflex is for a thread that compaction did not bring down
	rt.SetReflexes(NewReflexes("", rt.Store()))
	for i, text := range []string{"one", "two", "three"} {
		id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: text, ClientID: "c1"})
		if err != nil {
			t.Fatal(err)
		}
		got := reflexNames(follow(t, rt, id, nil))
		if want := i == 2; (len(got) == 1 && got[0] == "long-thread") != want {
			t.Fatalf("message %d: fired %v", i+1, got)
		}
	}
	if sent := lastUserText(t, s.bodies[2]); !strings.Contains(sent, "/new starts a fresh conversation") {
		t.Fatalf("the model was not told: %s", sent)
	}
}

func TestTheOwnersReflexesReplaceByNameAndABrokenFileIsSaidNotUsed(t *testing.T) {
	rt := newRuntime(t, newStandIn(t))
	names := func(rs *ReflexSet) string {
		list, _ := rs.Candidates(t.Context(), "", "")
		var out []string
		for _, r := range list {
			out = append(out, r.Name)
		}
		return strings.Join(out, ",")
	}
	if got := names(NewReflexes("", rt.Store())); got != "long-thread" {
		t.Fatalf("shipped: %s", got)
	}
	off := NewReflexes(reflexFile(t, `{"reflexes":[{"name":"long-thread","disabled":true,"trigger":{"kind":"prefix_pressure"},"action_kind":"inject_reminder","action":{"body":"x"}},
		{"name":"mine","trigger":{"kind":"attr","key":"selection","value":"yes"},"action_kind":"inject_reminder","action":{"body":"x"}}]}`), rt.Store())
	if got := names(off); got != "mine" || off.Problem() != "" {
		t.Fatalf("replaced: %s (%s)", got, off.Problem())
	}
	// The listing has both, the replaced one as switched off.
	list, file := off.List(t.Context())
	if len(list) != 2 || !strings.HasSuffix(file, "agent-reflexes.json") {
		t.Fatalf("list: %+v (%s)", list, file)
	}
	for _, r := range list {
		if r.Enabled != (r.Name == "mine") {
			t.Errorf("list: %+v", r)
		}
	}
	shipped, _ := NewReflexes("", rt.Store()).List(t.Context())
	if len(shipped) != 1 || shipped[0].About == "" || shipped[0].CooldownSeconds != 3600 || shipped[0].Action != "inject_reminder" {
		t.Errorf("shipped listing: %+v", shipped)
	}
	if rules, _ := NewRouter("").Rules(); len(rules) < 5 || rules[0].ID != "help" {
		t.Errorf("rules listing: %+v", rules)
	}
	for _, bad := range []string{
		`{"reflexes":[{"name":"x","trigger":{"kind":"user_regex_window","pattern":"("},"action_kind":"inject_reminder","action":{"body":"x"}}]}`,
		`{"reflexes":[{"name":"x","trigger":{"kind":"no_such_kind"},"action_kind":"inject_reminder","action":{"body":"x"}}]}`,
		`{"reflexes":[{"name":"x","trigger":{"kind":"attr","key":"a","value":"b"},"action_kind":"send_message","action":{"body":"x"}}]}`,
		`{"reflexes":[{"name":"x","trigger":{"kind":"attr","key":"a","value":"b"},"action_kind":"halt_session","action":{"body":"x"}}]}`,
		`not json`,
	} {
		rs := NewReflexes(reflexFile(t, bad), rt.Store())
		if got := names(rs); got != "long-thread" || !strings.Contains(rs.Problem(), "agent-reflexes.json") {
			t.Errorf("%s\n  in use: %s; problem: %q", bad, got, rs.Problem())
		}
	}
}
