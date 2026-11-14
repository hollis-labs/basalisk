package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/libs/agent/core/reflexes"
)

// Reflexes are standing rules that look at the conversation each time a message arrives,
// before the model is called (libs agent/core/reflexes: a trigger over the recent messages,
// and an action when it fires). Two actions are carried out here:
//
//	inject_reminder  {"body": "..."}     the model is told this, with the owner's message
//	halt_session     {"message": "..."}  the message is answered with this; no model is called
//
// The ones that ship are in reflexes.json. The owner's own go in a file (ReflexSet.file),
// in the same format; one with the name of a shipped reflex replaces it.

//go:embed reflexes.json
var defaultReflexes []byte

// reflexDef is a reflex as the files write it.
type reflexDef struct {
	Name        string          `json:"name"`
	About       string          `json:"about"`
	TriggerKind string          `json:"trigger_kind"`
	Trigger     json.RawMessage `json:"trigger"`
	ActionKind  string          `json:"action_kind"`
	Action      json.RawMessage `json:"action"`
	// CooldownSeconds is how long after firing it stays quiet. 0: it may fire on every message.
	CooldownSeconds int64 `json:"cooldown_seconds"`
	Priority        int64 `json:"priority"`
	Disabled        bool  `json:"disabled"`
}

// Fired is a reflex that fired on a message, and what it asks for.
type Fired struct {
	Name string
	// Reminder is told to the model with the owner's message.
	Reminder string
	// Halt answers the message; no model is called.
	Halt string
}

// ReflexSet holds the reflexes and evaluates them.
type ReflexSet struct {
	file  string
	store *Store

	mu      sync.Mutex
	about   map[string]string // name -> what the file says it is for
	defs    []reflexes.Reflex
	stamp   string
	problem string
	engine  *reflexes.Engine
}

// NewReflexes loads the reflexes that ship and the owner's file when there is one. The
// store remembers when each last fired (for its cooldown).
func NewReflexes(file string, store *Store) *ReflexSet {
	rs := &ReflexSet{file: file, store: store}
	engine, err := reflexes.New(rs, rs, reflexes.WithTrace(rs))
	if err != nil {
		slog.Error("agent: the reflex engine did not start", "err", err)
		return nil
	}
	rs.engine = engine
	rs.mu.Lock()
	rs.load()
	rs.mu.Unlock()
	return rs
}

// Problem says why the owner's reflex file is not in use ("" when it is, or there is none).
func (rs *ReflexSet) Problem() string {
	if rs == nil {
		return ""
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.problem
}

func parseReflexes(raw []byte, about map[string]string) ([]reflexes.Reflex, error) {
	var file struct {
		Reflexes []reflexDef `json:"reflexes"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, err
	}
	// A state with enough in it that every node of a trigger is really evaluated: a
	// pattern that does not compile, or a node of an unknown kind, is found now.
	probe := reflexes.State{PrefixTokens: 1}
	for i := 0; i < 64; i++ {
		m := reflexes.MessageSignal{Role: "user", Content: "x"}
		probe.Messages = append(probe.Messages, m)
		probe.UserMessages = append(probe.UserMessages, m)
	}
	var out []reflexes.Reflex
	for i, d := range file.Reflexes {
		if d.Name == "" {
			return nil, fmt.Errorf("reflex %d has no name", i+1)
		}
		if d.ActionKind != reflexes.ActionInjectReminder && d.ActionKind != reflexes.ActionHaltSession {
			return nil, fmt.Errorf("reflex %q: action_kind must be inject_reminder or halt_session", d.Name)
		}
		var action map[string]any
		if err := json.Unmarshal(d.Action, &action); err != nil {
			return nil, fmt.Errorf("reflex %q: action: %w", d.Name, err)
		}
		key := "body"
		if d.ActionKind == reflexes.ActionHaltSession {
			key = "message"
		}
		if s, _ := action[key].(string); strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("reflex %q: action needs %q", d.Name, key)
		}
		if d.TriggerKind == "" {
			d.TriggerKind = "predicate"
		}
		if _, err := reflexes.EvaluateTrigger(d.TriggerKind, string(d.Trigger), probe); err != nil {
			return nil, fmt.Errorf("reflex %q: trigger: %w", d.Name, err)
		}
		about[d.Name] = d.About
		status := "active"
		if d.Disabled {
			status = "disabled"
		}
		cooldown := max(d.CooldownSeconds, 0)
		out = append(out, reflexes.Reflex{ID: d.Name, Name: d.Name, TriggerKind: d.TriggerKind, TriggerSpec: string(d.Trigger),
			ActionKind: d.ActionKind, ActionSpec: string(d.Action), Status: status, Priority: d.Priority,
			CreatedAt: fmt.Sprintf("%06d", i), RecurrenceOverrideSeconds: &cooldown})
	}
	return out, nil
}

// load reads the definitions again if the owner's file changed. With the lock held.
func (rs *ReflexSet) load() {
	stamp := "none"
	var own []byte
	if rs.file != "" {
		if st, err := os.Stat(rs.file); err == nil {
			stamp = st.ModTime().Format(time.RFC3339Nano) + "/" + strconv.FormatInt(st.Size(), 10)
			if stamp == rs.stamp {
				return
			}
			own, _ = os.ReadFile(rs.file)
		}
	}
	if stamp == rs.stamp && rs.defs != nil {
		return
	}
	rs.stamp = stamp
	about := map[string]string{}
	defs, err := parseReflexes(defaultReflexes, about)
	if err != nil {
		slog.Error("agent: the built-in reflexes do not parse", "err", err)
		defs = []reflexes.Reflex{}
	}
	problem := ""
	if len(own) > 0 {
		mineAbout := map[string]string{}
		mine, err := parseReflexes(own, mineAbout)
		if err != nil {
			problem = fmt.Sprintf("%s is not in use: %v", rs.file, err)
			slog.Warn("agent: the owner's reflexes are not in use", "file", rs.file, "err", err)
		} else {
			named := map[string]bool{}
			for _, m := range mine {
				named[m.Name] = true
				about[m.Name] = mineAbout[m.Name]
			}
			kept := mine
			for _, d := range defs {
				if !named[d.Name] {
					kept = append(kept, d)
				}
			}
			defs = kept
		}
	}
	rs.defs, rs.problem, rs.about = defs, problem, about
}

// Candidates is the engine's Source: the active reflexes, with when each last fired.
func (rs *ReflexSet) Candidates(ctx context.Context, _, _ string) ([]reflexes.Reflex, error) {
	rs.mu.Lock()
	rs.load()
	defs := append([]reflexes.Reflex(nil), rs.defs...)
	rs.mu.Unlock()
	out := make([]reflexes.Reflex, 0, len(defs))
	for _, d := range defs {
		if d.Status != "active" {
			continue
		}
		d.FiredCount, d.LastFiredAt = rs.store.reflexFired(ctx, d.ID)
		out = append(out, d)
	}
	return out, nil
}

// ActionKinds is the engine's KindCatalog. Every reminder that fires is given; of several
// halts, the first. Neither kind has a cooldown of its own: each reflex states its own.
func (rs *ReflexSet) ActionKinds(context.Context) ([]reflexes.ActionKind, error) {
	none := int64(0)
	return []reflexes.ActionKind{
		{Name: reflexes.ActionInjectReminder, Category: "system_message", CombiningAlgorithm: "all_applicable", DefaultRecurrenceSeconds: &none},
		{Name: reflexes.ActionHaltSession, Category: "control", CombiningAlgorithm: "first_applicable", DefaultRecurrenceSeconds: &none},
	}, nil
}

// LogEvent and BumpAgentReflexFired are the engine's TraceStore.
func (rs *ReflexSet) LogEvent(_ context.Context, sessionID, eventType, _, detail, _ string) {
	slog.Info("agent: reflex fired", "reflex", detail, "action", eventType, "thread", sessionID)
}

func (rs *ReflexSet) BumpAgentReflexFired(ctx context.Context, id string, now time.Time) error {
	return rs.store.bumpReflex(ctx, id, now)
}

// Evaluate puts the conversation to the reflexes and returns the ones that fired.
func (rs *ReflexSet) Evaluate(ctx context.Context, state reflexes.State) []Fired {
	if rs == nil || rs.engine == nil {
		return nil
	}
	res, err := rs.engine.Run(ctx, reflexes.RunInput{AgentID: state.AgentID, State: &state})
	if err != nil {
		slog.Warn("agent: reflexes not evaluated", "err", err)
		return nil
	}
	var out []Fired
	for _, a := range res.Applied.Actions {
		f := Fired{Name: a.ReflexName}
		switch a.ActionKind {
		case reflexes.ActionInjectReminder:
			f.Reminder, _ = a.Spec["body"].(string)
		case reflexes.ActionHaltSession:
			f.Halt, _ = a.Spec["message"].(string)
		}
		if f.Reminder != "" || f.Halt != "" {
			out = append(out, f)
		}
	}
	return out
}

// reflexState is the conversation as the reflexes see it: the new message first, then
// what came before, newest first.
func reflexState(thread, text string, deskContext json.RawMessage, prior []Message, prefixTokens int) reflexes.State {
	st := reflexes.State{SessionID: thread, AgentID: "basalisk", PrefixTokens: prefixTokens, Attrs: map[string]string{"selection": "no"}}
	now := reflexes.MessageSignal{Role: "user", Content: text}
	st.Messages = append(st.Messages, now)
	st.UserMessages = append(st.UserMessages, now)
	for i := len(prior) - 1; i >= 0 && len(st.Messages) < 60; i-- {
		m := prior[i]
		sig := reflexes.MessageSignal{MessageID: m.ID, Role: m.Role, CreatedAt: m.CreatedAt}
		var said struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(m.Meta, &said)
		var words []string
		for _, b := range m.Blocks {
			switch b.Type {
			case "text":
				words = append(words, b.Text)
			case "tool_use":
				sig.ToolCalls++
				sig.ToolNames = append(sig.ToolNames, b.Name)
			}
		}
		switch {
		case m.Role == "user" && said.Text != "":
			// What the owner typed, without the desktop block that leads it.
			sig.Content = said.Text
			st.UserMessages = append(st.UserMessages, sig)
		case m.Role == "user" && len(words) == 0:
			continue // tool results only
		default:
			sig.Content = strings.Join(words, "\n")
		}
		st.Messages = append(st.Messages, sig)
	}
	var c struct {
		Focused *struct {
			App       string `json:"app"`
			Selection string `json:"selection"`
		} `json:"focused"`
	}
	if json.Unmarshal(deskContext, &c) == nil && c.Focused != nil {
		st.Attrs["focused.app"] = c.Focused.App
		if strings.TrimSpace(c.Focused.Selection) != "" {
			st.Attrs["selection"] = "yes"
		}
	}
	return st
}

// ReflexInfo is one reflex, for the interface to list.
type ReflexInfo struct {
	Name            string `json:"name"`
	About           string `json:"about"`
	Action          string `json:"action"` // inject_reminder or halt_session
	Enabled         bool   `json:"enabled"`
	CooldownSeconds int64  `json:"cooldown_seconds"`
	FiredCount      int64  `json:"fired_count"`
	LastFiredAt     string `json:"last_fired_at"`
}

// List are the reflexes in use, with when each last fired, and the owner's file.
func (rs *ReflexSet) List(ctx context.Context) (list []ReflexInfo, file string) {
	list = []ReflexInfo{}
	if rs == nil {
		return list, ""
	}
	rs.mu.Lock()
	rs.load()
	defs, about := append([]reflexes.Reflex(nil), rs.defs...), rs.about
	rs.mu.Unlock()
	for _, d := range defs {
		info := ReflexInfo{Name: d.Name, About: about[d.Name], Action: d.ActionKind, Enabled: d.Status == "active"}
		if d.RecurrenceOverrideSeconds != nil {
			info.CooldownSeconds = *d.RecurrenceOverrideSeconds
		}
		info.FiredCount, info.LastFiredAt = rs.store.reflexFired(ctx, d.ID)
		list = append(list, info)
	}
	return list, rs.file
}
