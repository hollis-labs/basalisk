package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/ui/ui-go/chatstream"
)

// waitTask waits for a task to leave the running state.
func waitTask(t *testing.T, rt *Runtime, owner, id string) Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if task, ok := rt.Store().Task(t.Context(), owner, id); ok && task.State != TaskRunning {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s did not end", id)
	return Task{}
}

func TestTheAgentHandsWorkToATaskAgentAndIsToldHowItEnded(t *testing.T) {
	s := newStandIn(t,
		// The running thread: the model starts a task.
		toolReply("toolu_1", "task_start", `{"title":"Read the docs","brief":"Read the three docs and say what they cover."}`),
		// The task agent's own run comes next (it starts while the first run is still going).
		textReply("They cover the shell, the agent and the files."),
		textReply("I have started it."),
		// The next message to the running thread: it is told how the task ended.
		textReply("The task found: shell, agent, files."),
	)
	rt := newRuntime(t, s)
	id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "look into the docs for me", ClientID: "c1", Tools: []ToolSpec{lookTool}})
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		TaskID string `json:"task_id"`
		RunID  string `json:"run_id"`
		State  string `json:"state"`
	}
	kinds := map[string]string{}
	evs := follow(t, rt, id, func(e chatstream.Event) {
		if e.Verb == chatstream.VerbPartStart {
			kinds[e.PartID] = e.Kind
		}
		if e.Verb == chatstream.VerbPartDelta && kinds[e.PartID] == "tool_result" {
			_ = json.Unmarshal([]byte(e.Text), &started)
		}
		if e.Verb == chatstream.VerbApprovalRequest {
			t.Error("starting a task asked for permission")
		}
	})
	if started.TaskID == "" || started.State != TaskRunning || last(evs).Verb != chatstream.VerbRunFinish {
		t.Fatalf("task_start: %+v, ended %+v", started, last(evs))
	}
	task := waitTask(t, rt, "u1", started.TaskID)
	if task.State != TaskDone || task.Result != "They cover the shell, the agent and the files." || task.Title != "Read the docs" || task.Origin != "agent" || task.Reported {
		t.Fatalf("task: %+v", task)
	}
	// The task agent was sent the brief and the task prompt, the page's tools, and no task tools.
	var taskBody map[string]any
	for _, b := range s.bodies {
		if sys, _ := json.Marshal(b["system"]); strings.Contains(string(sys), "working as a task agent") {
			taskBody = b
		}
	}
	if taskBody == nil {
		t.Fatal("no request was made as a task agent")
	}
	if msgs, _ := json.Marshal(taskBody["messages"]); !strings.Contains(string(msgs), "Read the three docs") || strings.Contains(string(msgs), "look into the docs for me") {
		t.Errorf("the task agent's messages: %s", msgs)
	}
	if tools, _ := json.Marshal(taskBody["tools"]); !strings.Contains(string(tools), `"desktop_windows"`) || strings.Contains(string(tools), "task_start") {
		t.Errorf("the task agent's tools: %s", tools)
	}
	// The running thread is told at its next message, once.
	id, _ = rt.Start(t.Context(), StartInput{Owner: "u1", Text: "anything back?", ClientID: "c1", Tools: []ToolSpec{lookTool}})
	follow(t, rt, id, nil)
	sent := lastUserText(t, s.bodies[len(s.bodies)-1])
	if !strings.Contains(sent, "<tasks>") || !strings.Contains(sent, `"Read the docs"`) || !strings.Contains(sent, "They cover the shell") || !strings.HasSuffix(strings.TrimSpace(sent), "anything back?") {
		t.Fatalf("the running thread was not told: %s", sent)
	}
	if again := rt.Store().unreported(t.Context(), "u1"); len(again) != 0 {
		t.Errorf("told twice: %+v", again)
	}
	// It is listed, and its thread can be read.
	if tasks, _ := rt.Store().Tasks(t.Context(), "u1", 10); len(tasks) != 1 || !tasks[0].Reported {
		t.Errorf("tasks: %+v", tasks)
	}
	if msgs, _ := rt.Store().Messages(t.Context(), started.TaskID); len(msgs) != 2 {
		t.Errorf("the task's thread: %d messages", len(msgs))
	}
	if _, ok := rt.Store().Task(t.Context(), "someone-else", started.TaskID); ok {
		t.Error("another owner can see the task")
	}
}

func TestATaskIsStartedByCommandRunsBesideTheRunningThreadAndTakesAFollowUp(t *testing.T) {
	s := newStandIn(t, "hang", textReply("Hello."), textReply("And the follow-up is done."))
	rt := newRuntime(t, s)
	rt.SetRouter(NewRouter(""))
	// "/task ..." is routed: no model for the running thread, and the task starts.
	id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "/task count the files in my notes folder", ClientID: "c1", Tools: []ToolSpec{lookTool}})
	if err != nil {
		t.Fatal(err)
	}
	evs := follow(t, rt, id, nil)
	if !strings.HasPrefix(textOf(evs), "Started a task agent") {
		t.Fatalf("said %q", textOf(evs))
	}
	tasks, _ := rt.Store().Tasks(t.Context(), "u1", 10)
	if len(tasks) != 1 || tasks[0].State != TaskRunning || tasks[0].Origin != "owner" || tasks[0].Title != "count the files in my notes folder" {
		t.Fatalf("tasks: %+v", tasks)
	}
	taskRun := rt.ActiveRun(tasks[0].ID)
	if taskRun == "" {
		t.Fatal("the task has no run")
	}
	// While the task hangs, the running thread still answers.
	id, err = rt.Start(t.Context(), StartInput{Owner: "u1", Text: "hello", ClientID: "c1", Tools: []ToolSpec{lookTool}})
	if err != nil {
		t.Fatal(err)
	}
	if got := textOf(follow(t, rt, id, nil)); got != "Hello." {
		t.Fatalf("the running thread said %q", got)
	}
	// Stopping the task's run ends the task as stopped.
	if err := rt.Cancel("u1", taskRun); err != nil {
		t.Fatal(err)
	}
	if task := waitTask(t, rt, "u1", tasks[0].ID); task.State != TaskStopped {
		t.Fatalf("after stop: %+v", task)
	}
	// A follow-up goes to the task's own thread and sets it going again.
	id, err = rt.Start(t.Context(), StartInput{Owner: "u1", Text: "carry on", ClientID: "c1", Tools: []ToolSpec{lookTool}, Thread: tasks[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	follow(t, rt, id, nil)
	if task := waitTask(t, rt, "u1", tasks[0].ID); task.State != TaskDone || task.Result != "And the follow-up is done." {
		t.Fatalf("after the follow-up: %+v", task)
	}
	if _, err := rt.Start(t.Context(), StartInput{Owner: "u2", Text: "mine now", Thread: tasks[0].ID}); err == nil {
		t.Error("another owner wrote to the task")
	}
}

func TestOnlySoManyTasksRunAtOnce(t *testing.T) {
	replies := make([]string, MaxRunningTasks)
	for i := range replies {
		replies[i] = "hang"
	}
	rt := newRuntime(t, newStandIn(t, replies...))
	for i := 0; i < MaxRunningTasks; i++ {
		if _, _, err := rt.StartTask(t.Context(), TaskInput{Owner: "u1", Brief: "wait", Origin: "owner"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := rt.StartTask(t.Context(), TaskInput{Owner: "u1", Brief: "one more", Origin: "owner"}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("a fifth task: %v", err)
	}
}
