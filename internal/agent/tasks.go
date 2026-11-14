package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Task agents: beside the one running thread, an agent can be given one piece of work to
// do by itself. It gets a thread of its own (kind 'task'), the same tools, and runs at the
// same time as the running thread and as other tasks. It asks permission like any run.
// When it ends, how it ended and its last message are kept on the task, and the running
// thread is told at its next message.
//
// A task is started by the main agent (the tool task_start), by the owner ("/task ..."
// through the router), or by a trigger (see triggers.go).

// MaxRunningTasks is how many of an owner's tasks may run at once.
const MaxRunningTasks = 4

// page is what the desktop page last sent with a message: who runs page tools, and which.
type page struct {
	client  string
	tools   []ToolSpec
	context json.RawMessage
}

type runKey struct{}

// runOf is the run a server tool is being called from.
func runOf(ctx context.Context) *run {
	r, _ := ctx.Value(runKey{}).(*run)
	return r
}

// TaskInput is one piece of work for a task agent.
type TaskInput struct {
	Owner string
	// Title is what the task is called in lists; made from the brief when empty.
	Title string
	// Brief is the whole of what the agent is told: it does not see the running thread.
	Brief string
	// Origin says who started it: "agent", "owner" or "trigger:<name>".
	Origin string
	// Parent is the thread it was started from, if any.
	Parent string
}

// StartTask gives an agent a task. It uses the tools of the owner's desktop page as last
// seen; with no page seen since Basalisk started, the task has the server's tools only.
func (rt *Runtime) StartTask(ctx context.Context, in TaskInput) (Task, string, error) {
	brief := strings.TrimSpace(in.Brief)
	if brief == "" {
		return Task{}, "", errors.New("the task has no brief")
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		words := strings.Fields(brief)
		if len(words) > 9 {
			words = append(words[:9], "…")
		}
		title = strings.Join(words, " ")
	}
	title = clip(title, 90)
	tasks, err := rt.store.Tasks(ctx, in.Owner, 200)
	if err != nil {
		return Task{}, "", err
	}
	running := 0
	for _, t := range tasks {
		if t.State == TaskRunning {
			running++
		}
	}
	if running >= MaxRunningTasks {
		return Task{}, "", fmt.Errorf("%d tasks are already running; wait for one to end, or stop one", running)
	}
	id, err := rt.store.newTask(ctx, in.Owner, in.Parent, title, in.Origin)
	if err != nil {
		return Task{}, "", err
	}
	rt.mu.Lock()
	pg := rt.pages[in.Owner]
	rt.mu.Unlock()
	runID, err := rt.Start(ctx, StartInput{Owner: in.Owner, Text: brief, ClientID: pg.client, Context: pg.context, Tools: pg.tools, Thread: id})
	if err != nil {
		rt.store.setTask(ctx, id, TaskFailed, "It could not be started: "+err.Error())
		rt.store.markReported(ctx, id)
		return Task{}, "", err
	}
	task, _ := rt.store.Task(ctx, in.Owner, id)
	return task, runID, nil
}

// taskTools are the main agent's tools for tasks. A task agent does not get them: a task
// does not start tasks.
func (rt *Runtime) taskTools() []ServerTool {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	return []ServerTool{
		{
			Spec: ToolSpec{
				Name:  "task_start",
				Level: LevelAct,
				Description: "Hand a piece of work to a task agent that works by itself, in the background, at the same time as this conversation and as other tasks. " +
					"For work of many steps that the owner does not need to watch (reading several pages or files and reporting, a long check). Do small things yourself. " +
					"The task agent has the same tools and asks permission the same way, but it does not see this conversation: put everything it needs in the brief. " +
					"It returns at once; the owner can open the task's window to watch it, and you are told how it ended at the owner's next message (or ask task_status).",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title": str("A few words to call it by."),
						"brief": str("The whole task, as you would hand it to a colleague who knows nothing of this conversation: the goal, what is known, where to look, what to report back."),
					},
					"required":             []string{"brief"},
					"additionalProperties": false,
				},
			},
			Run: func(ctx context.Context, input map[string]any) (string, error) {
				r := runOf(ctx)
				if r == nil {
					return "", errors.New("no run to start a task from")
				}
				title, _ := input["title"].(string)
				brief, _ := input["brief"].(string)
				origin := "agent"
				if r.route != nil {
					origin = "owner" // typed as a command, not decided by the model
				}
				// Not the run's context: the task goes on when this run is over.
				task, runID, err := rt.StartTask(context.WithoutCancel(ctx), TaskInput{Owner: r.owner, Title: title, Brief: brief, Origin: origin, Parent: r.thread})
				if err != nil {
					return "", err
				}
				out, _ := json.Marshal(map[string]string{"task_id": task.ID, "title": task.Title, "state": task.State, "run_id": runID})
				return string(out), nil
			},
		},
		{
			Spec: ToolSpec{
				Name:        "task_status",
				Level:       LevelRead,
				Description: "How the task agents stand: each task's title, state (running, done, failed, stopped) and, when it has ended, its report. Leave out task_id for all recent tasks.",
				InputSchema: map[string]any{
					"type":                 "object",
					"properties":           map[string]any{"task_id": str("One task's id, from task_start.")},
					"additionalProperties": false,
				},
			},
			Run: func(ctx context.Context, input map[string]any) (string, error) {
				r := runOf(ctx)
				if r == nil {
					return "", errors.New("no run")
				}
				var list []Task
				if id, _ := input["task_id"].(string); id != "" {
					t, ok := rt.store.Task(ctx, r.owner, id)
					if !ok {
						return "", fmt.Errorf("there is no task %s", id)
					}
					list = []Task{t}
				} else {
					var err error
					if list, err = rt.store.Tasks(ctx, r.owner, 12); err != nil {
						return "", err
					}
				}
				type row struct {
					ID     string `json:"task_id"`
					Title  string `json:"title"`
					State  string `json:"state"`
					Report string `json:"report,omitempty"`
				}
				out := []row{}
				for _, t := range list {
					out = append(out, row{ID: t.ID, Title: t.Title, State: t.State, Report: clip(t.Result, 6000)})
					if t.State != TaskRunning {
						rt.store.markReported(ctx, t.ID) // it has been told now
					}
				}
				raw, _ := json.Marshal(map[string]any{"tasks": out})
				return string(raw), nil
			},
		},
	}
}

// tasksBlock tells the running thread which tasks have ended since it was last told.
func tasksBlock(ended []Task) string {
	if len(ended) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<tasks>\nTask agents that have ended since the last message:\n")
	for _, t := range ended {
		fmt.Fprintf(&b, "- %q (%s): %s.\n<report>\n%s\n</report>\n", t.Title, t.ID, t.State, clip(strings.TrimSpace(t.Result), 4000))
	}
	b.WriteString("</tasks>")
	return b.String()
}

// taskPrompt is what a task agent is told on top of the usual.
const taskPrompt = `

You are working as a task agent: you were handed one task, in the first message, and you work on it by yourself. The owner is not watching as you go and may be doing something else on the same desktop, so prefer tools that read and look over ones that move their windows about. Do not ask questions unless you cannot go on at all. When you are done, your last message is your report: what you did, what you found, and anything you could not do. It is read later, on its own, so make it stand alone.`

// SeePage notes what an owner's desktop page can do: who runs page tools, and which. A
// message does this too; an event does it so that a task started before any message has
// the desktop's tools.
func (rt *Runtime) SeePage(owner, client string, tools []ToolSpec, deskContext json.RawMessage) {
	if client == "" {
		return
	}
	rt.mu.Lock()
	rt.pages[owner] = page{client: client, tools: tools, context: deskContext}
	rt.mu.Unlock()
}
