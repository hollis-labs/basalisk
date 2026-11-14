package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// A Planner item can be sent to Torque: one Torque task is made from it and its id is kept
// in the item's `task`, so a second send finds the task and makes none. Nothing else of
// Torque's is changed; the status is read back for the detail.

// toolCaller is the gateway call Torque needs (GatewayCaster's link has it).
type toolCaller interface {
	Call(ctx context.Context, tool string, args map[string]any) ([]byte, error)
}

func (s *Store) torque() toolCaller {
	switch c := s.caster.(type) {
	case GatewayCaster:
		return c.Link
	case toolCaller:
		return c
	}
	return nil
}

// Sendable kinds: work that an agent can pick up.
var sendable = map[string]bool{"task": true, "feature": true, "exploration": true}

// ErrNoTorque: the gateway does not reach Torque here.
var ErrNoTorque = errors.New("Torque is not reachable here (no gateway)")

// SendToTorque makes the item's Torque task, or returns the one it already has
// (created false). The item is read again under the store's lock, so two sends cannot make
// two tasks.
func (s *Store) SendToTorque(ctx context.Context, ws, id string) (task string, created bool, err error) {
	t := s.torque()
	if s.caster == nil || t == nil {
		return "", false, ErrNoTorque
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, err := s.space(ctx, ws)
	if err != nil {
		return "", false, err
	}
	it, ok, err := s.fresh(ctx, ns, ItemName(id))
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, ErrNotFound
	}
	d, err := ParseDoc(it.Content)
	if err != nil {
		return "", false, fmt.Errorf("the item cannot be read: %w", err)
	}
	if have := strings.TrimSpace(d.Str("task")); have != "" {
		return have, false, nil
	}
	if !sendable[d.Str("kind")] {
		return "", false, fmt.Errorf("a %s is planned here; tasks, features and explorations go to Torque", d.Str("kind"))
	}
	title := strings.TrimSpace(d.Str("title"))
	if title == "" {
		title = id
	}
	w, _ := s.workspace(ctx, ws)
	body := strings.TrimSpace(ParseBody(d.Body).String())
	desc := fmt.Sprintf("%s\n\nFrom the Planner: %s (%s), caster://%s/%s.", body, w.Title, ws, ns, ItemName(id))
	raw, err := t.Call(ctx, "torque_task_create", map[string]any{
		"title":       clip(title, 200),
		"description": strings.TrimSpace(desc),
		"tags":        `["from-planner"]`,
		"source_type": "user",
		"priority":    "3",
	})
	if err != nil {
		return "", false, fmt.Errorf("Torque did not take the task: %w", err)
	}
	var made struct {
		Data struct {
			ID string `json:"ID"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &made) != nil || made.Data.ID == "" {
		return "", false, errors.New("Torque answered, but not with a task")
	}
	if _, err := s.changeLocked(ctx, ns, id, func(d *Doc) error { return d.Set("task", made.Data.ID) }); err != nil {
		return made.Data.ID, true, fmt.Errorf("task %s was made in Torque, but the item could not record it: %w", made.Data.ID, err)
	}
	return made.Data.ID, true, nil
}

// TaskStatus is a Torque task as the detail shows it.
type TaskStatus struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// TorqueStatus reads a task's title and status from Torque.
func (s *Store) TorqueStatus(ctx context.Context, task string) (TaskStatus, error) {
	t := s.torque()
	if t == nil {
		return TaskStatus{}, ErrNoTorque
	}
	raw, err := t.Call(ctx, "torque_task_get", map[string]any{"id": task, "format": "typed", "comments": "false"})
	if err != nil {
		return TaskStatus{}, err
	}
	var out struct {
		Data TaskStatus `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Data.ID == "" {
		return TaskStatus{}, errors.New("Torque answered, but not with the task")
	}
	return out.Data, nil
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
