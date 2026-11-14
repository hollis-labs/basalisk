package agent

import (
	"context"
	"log/slog"
	"regexp"
	"time"

	"github.com/hollis-labs/libs/agent/core/reflexes"
)

// Triggers: reflexes that fire without the owner saying anything, and start a task agent
// (action dispatch_to_agent). Two kinds of trigger do that:
//
//	event     {"name": "<event>"}       when the named thing happens
//	interval  {"every_n_ticks": N}      every N minutes while Basalisk is running
//
// The events:
//
//	desktop_ready          the desktop page has come up (once per page load)
//	window_opened:<app>    a window of that app was opened (window_opened:browser)
//	task_ended             a task agent ended (not one a trigger started: no loops)
//
// plus anything the page reports to POST /api/agent/events. A predicate reflex may also
// dispatch, in which case it is the owner's message that sets the task going.
//
// A trigger never fires more than once a minute, whatever its cooldown says, and a task
// it starts is a task like any other: it asks permission for anything that changes data,
// and counts toward the limit on running tasks. None ship: these are the owner's to write.

// minDispatchCooldown is the least time, in seconds, between two firings of a reflex that
// starts an agent.
const minDispatchCooldown = 60

var eventName = regexp.MustCompile(`^[a-z][a-z0-9_.:-]{0,63}$`)

// ValidEvent says whether a name can be an event's.
func ValidEvent(name string) bool { return eventName.MatchString(name) }

// Event tells the reflexes that something happened, and starts the tasks of those that
// fire on it. It returns the tasks started.
func (rt *Runtime) Event(ctx context.Context, owner, name string) []Task {
	if !ValidEvent(name) {
		return nil
	}
	return rt.dispatch(ctx, owner, reflexes.State{
		Events: []reflexes.EventSignal{{EventType: name, Category: "desktop", CreatedAt: now()}},
	}, "event "+name)
}

// dispatch evaluates the reflexes that start agents against a state, and starts them.
func (rt *Runtime) dispatch(ctx context.Context, owner string, state reflexes.State, why string) []Task {
	rs := rt.Reflexes()
	if rs == nil || !rt.cfg.Ready() {
		return nil
	}
	if thread, err := rt.store.MainThread(ctx, owner); err == nil {
		state.SessionID = thread
	}
	state.AgentID = "basalisk"
	var started []Task
	for _, f := range rs.Evaluate(ctx, state, reflexes.ActionDispatchToAgent) {
		task, _, err := rt.StartTask(ctx, TaskInput{Owner: owner, Title: f.Title, Brief: f.Prompt, Origin: "trigger:" + f.Name})
		if err != nil {
			slog.Warn("agent: a trigger's task did not start", "reflex", f.Name, "why", why, "err", err)
			continue
		}
		slog.Info("agent: a trigger started a task", "reflex", f.Name, "why", why, "task", task.ID)
		started = append(started, task)
	}
	return started
}

// RunTriggers counts the minutes while ctx lives, and at each one puts the interval
// reflexes to it, for every owner whose desktop page has been seen since Basalisk started
// (a task with no desktop to use is of little use, so nothing fires with nobody there).
func (rt *Runtime) RunTriggers(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = time.Minute
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	tick := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		tick++
		rt.mu.Lock()
		owners := make([]string, 0, len(rt.pages))
		for owner := range rt.pages {
			owners = append(owners, owner)
		}
		rt.mu.Unlock()
		for _, owner := range owners {
			rt.dispatch(ctx, owner, reflexes.State{TickN: tick}, "interval")
		}
	}
}
