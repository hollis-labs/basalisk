package agent

import (
	"strings"
	"testing"
	"time"
)

func tasksOf(t *testing.T, rt *Runtime, owner string) []Task {
	t.Helper()
	list, err := rt.Store().Tasks(t.Context(), owner, 50)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestAnEventTriggerStartsATaskAgentAndNotMoreThanOnceAMinute(t *testing.T) {
	s := newStandIn(t, textReply("Checked the page."))
	rt := newRuntime(t, s)
	rt.SetReflexes(NewReflexes(reflexFile(t, `{"reflexes":[
		{"name":"greet-browser","about":"When a browser window opens.","trigger_kind":"event","trigger":{"name":"window_opened:browser"},
		 "action_kind":"dispatch_to_agent","action":{"prompt":"Say what page the new browser window shows.","title":"New browser window"},"cooldown_seconds":0}]}`), rt.Store()))
	if p := rt.Reflexes().Problem(); p != "" {
		t.Fatal(p)
	}
	rt.SeePage("u1", "c1", []ToolSpec{lookTool}, nil)
	if got := rt.Event(t.Context(), "u1", "window_opened:notes"); len(got) != 0 {
		t.Fatalf("another event started %d tasks", len(got))
	}
	started := rt.Event(t.Context(), "u1", "window_opened:browser")
	if len(started) != 1 || started[0].Title != "New browser window" || started[0].Origin != "trigger:greet-browser" {
		t.Fatalf("started: %+v", started)
	}
	task := waitTask(t, rt, "u1", started[0].ID)
	if task.State != TaskDone || task.Result != "Checked the page." {
		t.Fatalf("task: %+v", task)
	}
	// The task agent was given the prompt and the page's tools.
	if sent := lastUserText(t, s.bodies[0]); !strings.Contains(sent, "Say what page the new browser window shows.") {
		t.Errorf("the task's brief: %s", sent)
	}
	// Asked for no cooldown, it still keeps a minute.
	if again := rt.Event(t.Context(), "u1", "window_opened:browser"); len(again) != 0 {
		t.Fatalf("fired again at once: %+v", again)
	}
	if rt.Event(t.Context(), "u1", "Not An Event") != nil || ValidEvent("window opened") || !ValidEvent("window_opened:browser") {
		t.Error("event names")
	}
	list, _ := rt.Reflexes().List(t.Context())
	if len(list) != 2 || list[0].Trigger != "event" || list[0].Action != "dispatch_to_agent" || list[0].CooldownSeconds != 60 || list[0].FiredCount != 1 {
		t.Errorf("listing: %+v", list)
	}
}

func TestAnIntervalTriggerFiresAsTheMinutesPassOnceAPageHasBeenSeen(t *testing.T) {
	s := newStandIn(t, textReply("Nothing new."))
	rt := newRuntime(t, s)
	rt.SetReflexes(NewReflexes(reflexFile(t, `{"reflexes":[
		{"name":"hourly-look","trigger_kind":"interval","trigger":{"every_n_ticks":3},
		 "action_kind":"dispatch_to_agent","action":{"prompt":"Look for anything new."},"cooldown_seconds":3600}]}`), rt.Store()))
	go rt.RunTriggers(t.Context(), 15*time.Millisecond)
	// Nobody there: nothing fires however many ticks pass.
	time.Sleep(120 * time.Millisecond)
	if n := len(tasksOf(t, rt, "u1")); n != 0 {
		t.Fatalf("fired with no page seen: %d", n)
	}
	rt.SeePage("u1", "c1", nil, nil)
	deadline := time.Now().Add(3 * time.Second)
	for len(tasksOf(t, rt, "u1")) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	list := tasksOf(t, rt, "u1")
	if len(list) != 1 || list[0].Origin != "trigger:hourly-look" || list[0].Title != "hourly-look" {
		t.Fatalf("tasks: %+v", list)
	}
	waitTask(t, rt, "u1", list[0].ID)
	// Its cooldown holds through later ticks.
	time.Sleep(150 * time.Millisecond)
	if n := len(tasksOf(t, rt, "u1")); n != 1 {
		t.Fatalf("fired again inside its cooldown: %d tasks", n)
	}
}

func TestATaskEndingIsAnEventButATriggersOwnTaskDoesNotSetItOffAgain(t *testing.T) {
	s := newStandIn(t, textReply("The first is done."), textReply("Noted that it ended."))
	rt := newRuntime(t, s)
	rt.SetReflexes(NewReflexes(reflexFile(t, `{"reflexes":[
		{"name":"after-task","trigger_kind":"event","trigger":{"name":"task_ended"},
		 "action_kind":"dispatch_to_agent","action":{"prompt":"A task ended. Note it."}}]}`), rt.Store()))
	first, _, err := rt.StartTask(t.Context(), TaskInput{Owner: "u1", Brief: "do the first thing", Origin: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, rt, "u1", first.ID)
	deadline := time.Now().Add(3 * time.Second)
	for len(tasksOf(t, rt, "u1")) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	list := tasksOf(t, rt, "u1")
	if len(list) != 2 {
		t.Fatalf("the trigger did not start a task: %+v", list)
	}
	for _, task := range list {
		waitTask(t, rt, "u1", task.ID)
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(tasksOf(t, rt, "u1")); n != 2 {
		t.Fatalf("the trigger's own task set it off again: %d tasks", n)
	}
}

func TestAReflexOnAMessageCanStartATaskAndTheMessageIsStillAnswered(t *testing.T) {
	s := newStandIn(t, textReply("On it."), textReply("Standup notes drafted."))
	rt := newRuntime(t, s)
	rt.SetReflexes(NewReflexes(reflexFile(t, `{"reflexes":[
		{"name":"standup","trigger":{"kind":"user_regex_window","window":1,"pattern":"(?i)\\bstandup\\b"},
		 "action_kind":"dispatch_to_agent","action":{"prompt":"Draft standup notes from yesterday's closed tasks.","title":"Standup notes"}}]}`), rt.Store()))
	id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "standup is in ten minutes", ClientID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	evs := follow(t, rt, id, nil)
	if got := reflexNames(evs); len(got) != 1 || got[0] != "standup" {
		t.Fatalf("fired: %v", got)
	}
	list := tasksOf(t, rt, "u1")
	if len(list) != 1 || list[0].Title != "Standup notes" || list[0].Origin != "trigger:standup" || list[0].ParentID == "" {
		t.Fatalf("tasks: %+v", list)
	}
	waitTask(t, rt, "u1", list[0].ID)
	if len(s.bodies) != 2 {
		t.Fatalf("model calls = %d (the message and the task)", len(s.bodies))
	}
}

func TestADispatchReflexNeedsItsPrompt(t *testing.T) {
	rt := newRuntime(t, newStandIn(t))
	rs := NewReflexes(reflexFile(t, `{"reflexes":[{"name":"x","trigger_kind":"event","trigger":{"name":"desktop_ready"},"action_kind":"dispatch_to_agent","action":{"title":"no prompt"}}]}`), rt.Store())
	if !strings.Contains(rs.Problem(), `action needs "prompt"`) {
		t.Fatalf("problem: %q", rs.Problem())
	}
}
