// Package agent is Basalisk's AI core: one agent, beside the desktop, that the owner talks
// to in a running thread. It runs in the server on the portfolio's shared libraries (the
// run loop and turn reader from libs/agent/core, the model contracts from libs/llm/core,
// the chatstream/v1 event stream and its hub from libs/ui/ui-go) and adds what only
// Basalisk has: a model client for the LLM gateway, a store, and a bridge that lets the
// model use tools the desktop page runs (the windows, the apps).
//
// See docs/agent.md.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/libs/agent/core/runloop"
	"github.com/hollis-labs/libs/agent/core/turn"
	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/libs/ui/ui-go/chatstream"
	"github.com/hollis-labs/libs/ui/ui-go/chatstream/hubbind"
	"github.com/hollis-labs/libs/ui/ui-go/streamhub"
)

// Config is where the model is and how far a run may go.
type Config struct {
	// GatewayURL is the Anthropic-compatible base: requests go to <GatewayURL>/v1/messages.
	GatewayURL string
	// KeyFile holds the bearer key. Read at each request; never logged.
	KeyFile string
	// Model is the model or gateway alias.
	Model     string
	MaxTokens int
	// MaxSteps bounds the model invocations in one run (each tool round is one).
	MaxSteps int
}

// Ready says whether a model can be called at all.
func (c Config) Ready() bool { return c.GatewayURL != "" && c.KeyFile != "" && c.Model != "" }

func (c Config) withDefaults() Config {
	if c.MaxTokens <= 0 {
		c.MaxTokens = 8192
	}
	if c.MaxSteps <= 0 {
		c.MaxSteps = 24
	}
	return c
}

// Tool levels: how much a tool may do without asking.
const (
	LevelRead  = "read"  // looks, changes nothing
	LevelAct   = "act"   // changes what is on screen, and is undone as easily (open a window)
	LevelWrite = "write" // changes data or reaches outside Basalisk: asks first
)

// ToolSpec is a tool the model may call. The desktop page declares its tools with each
// message and runs them itself; the server relays the call and waits for the answer.
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
	Level       string         `json:"level"`
}

var toolName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// StartInput is one message from the owner.
type StartInput struct {
	Owner string
	Text  string
	// ClientID names the page that sent the message: the one that runs the desktop tools.
	ClientID string
	// Context is what the owner was looking at (the focused window, the selection, the open
	// windows), as the page describes it. Kept with the message and shown to the model.
	Context json.RawMessage
	Tools   []ToolSpec
}

// Errors a caller can tell apart.
var (
	ErrNotReady = errors.New("no model is configured")
	ErrBusy     = errors.New("the agent is still working on the last message")
	ErrNoRun    = errors.New("no such run")
)

// Runtime runs the agent.
type Runtime struct {
	cfg      Config
	store    *Store
	provider llmcontracts.Provider
	hub      *streamhub.Hub
	// base outlives a request: a run keeps going when the page that started it goes away.
	base context.Context

	mu      sync.Mutex
	runs    map[string]*run            // running, by id
	busy    map[string]string          // thread -> its running run
	session map[string]map[string]bool // owner -> tools allowed until the app restarts
}

// New builds the runtime. ctx ends every run when it is cancelled (the server stopping).
func New(ctx context.Context, cfg Config, store *Store, provider llmcontracts.Provider) *Runtime {
	hub := streamhub.New(streamhub.NewMemoryLog(),
		streamhub.WithTerminal(hubbind.Terminal),
		// A finished run can be replayed for a while: a page that reloads mid-answer catches up.
		streamhub.WithRetainAfterClose(10*time.Minute))
	return &Runtime{cfg: cfg.withDefaults(), store: store, provider: provider, hub: hub, base: ctx,
		runs: map[string]*run{}, busy: map[string]string{}, session: map[string]map[string]bool{}}
}

// Config is the configuration in use (the key file's path, never its content).
func (rt *Runtime) Config() Config { return rt.cfg }

// Store is the conversation store.
func (rt *Runtime) Store() *Store { return rt.store }

// Hub is the event hub: one stream per run, named by the run id.
func (rt *Runtime) Hub() *streamhub.Hub { return rt.hub }

// ActiveRun is the run working on the owner's thread, or "".
func (rt *Runtime) ActiveRun(thread string) string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.busy[thread]
}

type toolAnswer struct {
	content string
	isError bool
}

type run struct {
	rt                *Runtime
	id, owner, thread string
	client            string
	tools             map[string]ToolSpec
	defs              []llmtypes.ToolDefinition
	cancel            context.CancelCauseFunc

	mu        sync.Mutex
	waiting   map[string]chan toolAnswer // tool call id -> its answer from the page
	approvals map[string]chan string     // approval id -> "once", "session", "always" or "deny"

	history []llmtypes.ChatMessage
	in, out int // tokens, summed over the run's steps
	msgN    int
	partN   int
}

var errCancelled = errors.New("cancelled")

// Start takes a message and begins answering it. The answer is the run's event stream.
func (rt *Runtime) Start(ctx context.Context, in StartInput) (string, error) {
	if !rt.cfg.Ready() {
		return "", ErrNotReady
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return "", errors.New("the message is empty")
	}
	tools := map[string]ToolSpec{}
	var defs []llmtypes.ToolDefinition
	for _, t := range in.Tools {
		if !toolName.MatchString(t.Name) || tools[t.Name].Name != "" {
			return "", fmt.Errorf("tool name %q is not usable", t.Name)
		}
		if t.Level != LevelRead && t.Level != LevelAct {
			t.Level = LevelWrite // anything not declared harmless asks first
		}
		tools[t.Name] = t
		defs = append(defs, llmtypes.ToolDefinition{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	thread, err := rt.store.MainThread(ctx, in.Owner)
	if err != nil {
		return "", err
	}
	r := &run{rt: rt, id: newID("run"), owner: in.Owner, thread: thread, client: in.ClientID, tools: tools, defs: defs,
		waiting: map[string]chan toolAnswer{}, approvals: map[string]chan string{}}

	rt.mu.Lock()
	if rt.busy[thread] != "" {
		rt.mu.Unlock()
		return "", ErrBusy
	}
	rt.busy[thread] = r.id
	rt.runs[r.id] = r
	rt.mu.Unlock()
	release := func() {
		rt.mu.Lock()
		delete(rt.busy, thread)
		delete(rt.runs, r.id)
		rt.mu.Unlock()
	}

	prior, err := rt.store.Messages(ctx, thread)
	if err == nil {
		err = rt.store.startRun(ctx, r.id, thread, in.Owner, rt.cfg.Model)
	}
	if err == nil {
		err = rt.hub.Open(ctx, r.id)
	}
	if err != nil {
		release()
		return "", err
	}
	for _, m := range prior {
		r.history = append(r.history, llmtypes.ChatMessage{Role: m.Role, ContentBlocks: m.Blocks})
	}
	r.history = repair(r.history)

	// What the owner was looking at goes in front of what they said, in the same message:
	// the thread then remembers where each thing was said, and the system prompt stays the
	// same from turn to turn.
	blocks := []llmtypes.ContentBlock{}
	if where := describeContext(in.Context); where != "" {
		blocks = append(blocks, llmtypes.ContentBlock{Type: "text", Text: where})
	}
	blocks = append(blocks, llmtypes.ContentBlock{Type: "text", Text: text})
	meta, _ := json.Marshal(map[string]any{"text": text, "context": in.Context})
	if _, err := rt.store.Append(ctx, thread, r.id, "user", blocks, meta); err != nil {
		rt.store.finishRun(ctx, r.id, "error", err.Error(), 0, 0)
		release()
		return "", err
	}
	r.history = append(r.history, llmtypes.ChatMessage{Role: "user", ContentBlocks: blocks})

	runCtx, cancel := context.WithCancelCause(rt.base)
	r.cancel = cancel
	go func() {
		defer release()
		defer cancel(nil)
		r.execute(WithSession(runCtx, thread))
	}()
	return r.id, nil
}

// repair makes a stored history sendable: a run that died between a tool call and its
// result left a call unanswered, which the model API refuses.
func repair(h []llmtypes.ChatMessage) []llmtypes.ChatMessage {
	out := make([]llmtypes.ChatMessage, 0, len(h))
	for i, m := range h {
		out = append(out, m)
		if m.Role != "assistant" {
			continue
		}
		answered := map[string]bool{}
		if i+1 < len(h) && h[i+1].Role == "user" {
			for _, b := range h[i+1].ContentBlocks {
				if b.Type == "tool_result" {
					answered[b.ToolUseID] = true
				}
			}
		}
		var missing []llmtypes.ContentBlock
		for _, b := range m.ContentBlocks {
			if b.Type == "tool_use" && !answered[b.ID] {
				missing = append(missing, llmtypes.ContentBlock{Type: "tool_result", ToolUseID: b.ID, Content: "This did not run: the conversation was interrupted.", IsError: true})
			}
		}
		if len(missing) == 0 {
			continue
		}
		if len(answered) > 0 {
			// Some were answered: add the rest to that same message when it is copied next.
			h[i+1].ContentBlocks = append(missing, h[i+1].ContentBlocks...)
			continue
		}
		out = append(out, llmtypes.ChatMessage{Role: "user", ContentBlocks: missing})
	}
	return out
}

// Cancel stops a run. The stream ends with run.abort.
func (rt *Runtime) Cancel(owner, id string) error {
	r := rt.find(owner, id)
	if r == nil {
		return ErrNoRun
	}
	r.cancel(errCancelled)
	return nil
}

func (rt *Runtime) find(owner, id string) *run {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if r := rt.runs[id]; r != nil && r.owner == owner {
		return r
	}
	return nil
}

// ToolResult is the page's answer to a tool call it was asked to run.
func (rt *Runtime) ToolResult(owner, id, callID, content string, isError bool) error {
	r := rt.find(owner, id)
	if r == nil {
		return ErrNoRun
	}
	r.mu.Lock()
	ch := r.waiting[callID]
	delete(r.waiting, callID)
	r.mu.Unlock()
	if ch == nil {
		return errors.New("that tool call is not waiting for an answer")
	}
	ch <- toolAnswer{content: content, isError: isError}
	return nil
}

// Approve answers a question the run asked: decision is "once", "session", "always" or "deny".
func (rt *Runtime) Approve(owner, id, approvalID, decision string) error {
	switch decision {
	case "once", "session", "always", "deny":
	default:
		return errors.New("the decision must be once, session, always or deny")
	}
	r := rt.find(owner, id)
	if r == nil {
		return ErrNoRun
	}
	r.mu.Lock()
	ch := r.approvals[approvalID]
	delete(r.approvals, approvalID)
	r.mu.Unlock()
	if ch == nil {
		return errors.New("that question is no longer waiting")
	}
	ch <- decision
	return nil
}

// ---- one run ----

func (r *run) emit(ev chatstream.Event) {
	ev.V = chatstream.SchemaVersion
	ev.RunID = r.id
	ev.Time = time.Now().UTC()
	// Publishing does not depend on the run's context: the terminal event of a cancelled run
	// still has to reach the page.
	if _, err := hubbind.Publish(context.WithoutCancel(r.rt.base), r.rt.hub, r.id, ev); err != nil {
		slog.Warn("agent: event not published", "run", r.id, "verb", string(ev.Verb), "err", err)
	}
}

func meta(kv ...any) map[string]json.RawMessage {
	m := map[string]json.RawMessage{}
	for i := 0; i+1 < len(kv); i += 2 {
		raw, _ := json.Marshal(kv[i+1])
		m[kv[i].(string)] = raw
	}
	return m
}

func (r *run) usage(scope chatstream.UsageScope) *chatstream.Usage {
	u, _ := chatstream.UsageFromExclusiveInput(scope, r.in, 0, 0, r.out)
	return &u
}

type step struct {
	res turn.TurnResult
	err error
}

func (r *run) execute(ctx context.Context) {
	rt := r.rt
	r.emit(chatstream.Event{Verb: chatstream.VerbRunStart, Provider: "gateway", Model: rt.cfg.Model})

	finish := chatstream.FinishStop
	var failure error
	steps, tries := 0, 0

	_, loopErr := runloop.Execute(ctx, runloop.Ports[step, []llmtypes.ToolUseBlock]{
		// Request: one model invocation, streamed to the page as it arrives.
		Request: func(ctx context.Context) (step, runloop.Directive) {
			if ctx.Err() != nil {
				return step{}, runloop.Terminate
			}
			if steps >= rt.cfg.MaxSteps {
				finish = chatstream.FinishTurnLimit
				return step{}, runloop.Finish
			}
			steps++
			r.msgN++
			msgID := fmt.Sprintf("m%d", r.msgN)
			r.emit(chatstream.Event{Verb: chatstream.VerbMessageStart, MessageID: msgID, Role: "assistant"})
			textPart := ""
			closeText := func() {
				if textPart != "" {
					r.emit(chatstream.Event{Verb: chatstream.VerbPartEnd, PartID: textPart})
					textPart = ""
				}
			}
			res, err := turn.ExecuteTurn(ctx, turn.TurnRequest{
				IdleTimeout:     90 * time.Second,
				RequireTerminal: true,
				Stream: func(ctx context.Context) (<-chan llmtypes.StreamEvent, error) {
					return rt.provider.StreamChat(ctx, llmtypes.ChatRequest{
						Model: rt.cfg.Model, MaxTokens: rt.cfg.MaxTokens,
						SystemPrompt: systemPrompt(), Messages: r.history, Tools: r.defs,
					})
				},
				Sink: turn.TurnSink{
					OnDelta: func(s string) {
						if textPart == "" {
							r.partN++
							textPart = fmt.Sprintf("p%d", r.partN)
							r.emit(chatstream.Event{Verb: chatstream.VerbPartStart, PartID: textPart, Kind: string(chatstream.PartText)})
						}
						r.emit(chatstream.Event{Verb: chatstream.VerbPartDelta, PartID: textPart, Text: s})
					},
					OnToolUse: func(llmtypes.ToolUseBlock) { closeText() },
				},
			})
			closeText()
			r.emit(chatstream.Event{Verb: chatstream.VerbMessageEnd, MessageID: msgID})
			if res.Usage != nil {
				r.in += res.Usage.InputTokens + res.Usage.CacheReadTokens + res.Usage.CacheCreationTokens
				r.out += res.Usage.OutputTokens
				r.emit(chatstream.Event{Verb: chatstream.VerbUsage, Usage: r.usage(chatstream.UsageCumulative)})
			}
			if err != nil && ctx.Err() != nil {
				// Stopped mid-answer: what was said so far stays in the thread.
				if res.Text != "" {
					r.keep(ctx, "assistant", []llmtypes.ContentBlock{{Type: "text", Text: res.Text}})
				}
				return step{}, runloop.Terminate
			}
			// A request the gateway refused for a passing reason is tried again, as long as
			// nothing of it reached the page.
			var ge *GatewayError
			if err != nil && errors.As(err, &ge) && ge.Retryable() && tries < 2 {
				tries++
				steps--
				select {
				case <-time.After(time.Duration(tries) * 2 * time.Second):
					return step{}, runloop.Retry
				case <-ctx.Done():
					return step{}, runloop.Terminate
				}
			}
			tries = 0
			return step{res: res, err: err}, runloop.Proceed
		},
		// Consume: keep what the model said; decide whether there are tools to run.
		Consume: func(ctx context.Context, s step) ([]llmtypes.ToolUseBlock, runloop.Directive) {
			if s.err != nil {
				failure = s.err
				// What was said before the failure is kept, so the thread shows it.
				if s.res.Text != "" {
					r.keep(ctx, "assistant", []llmtypes.ContentBlock{{Type: "text", Text: s.res.Text}})
				}
				return nil, runloop.Terminate
			}
			blocks := []llmtypes.ContentBlock{}
			if s.res.Text != "" {
				blocks = append(blocks, llmtypes.ContentBlock{Type: "text", Text: s.res.Text})
			}
			for _, tu := range s.res.ToolUseBlocks {
				input := tu.Input
				if input == nil {
					input = map[string]any{}
				}
				blocks = append(blocks, llmtypes.ContentBlock{Type: "tool_use", ID: tu.ID, Name: tu.Name, Input: &input})
			}
			if len(blocks) > 0 {
				r.keep(ctx, "assistant", blocks)
			}
			if len(s.res.ToolUseBlocks) == 0 {
				switch s.res.StopReason {
				case llmtypes.StopReasonMaxTokens:
					finish = chatstream.FinishLength
				case llmtypes.StopReasonRefusal:
					finish = chatstream.FinishRefusal
				}
				return nil, runloop.Finish
			}
			return s.res.ToolUseBlocks, runloop.Proceed
		},
		// Settle: run the tools, one after another, and hand the model their results.
		Settle: func(ctx context.Context, calls []llmtypes.ToolUseBlock) runloop.Directive {
			results := make([]llmtypes.ContentBlock, 0, len(calls))
			for _, call := range calls {
				content, isError := r.tool(ctx, call)
				results = append(results, llmtypes.ContentBlock{Type: "tool_result", ToolUseID: call.ID, Content: content, IsError: isError})
			}
			// Kept even when cancelled mid-way, so every call in the thread has its answer.
			r.keep(context.WithoutCancel(ctx), "user", results)
			if ctx.Err() != nil {
				return runloop.Terminate
			}
			return runloop.Proceed
		},
		Finalize: func(context.Context) runloop.Directive { return runloop.Finish },
	})

	bg := context.WithoutCancel(ctx)
	switch {
	case errors.Is(context.Cause(ctx), errCancelled):
		rt.store.finishRun(bg, r.id, "cancelled", "", r.in, r.out)
		r.emit(chatstream.Event{Verb: chatstream.VerbRunAbort, Reason: string(chatstream.FinishCancelled)})
	case ctx.Err() != nil:
		rt.store.finishRun(bg, r.id, "error", "Basalisk stopped while this was running.", r.in, r.out)
		r.emit(chatstream.Event{Verb: chatstream.VerbRunAbort, Reason: string(chatstream.FinishCancelled)})
	case failure != nil || loopErr != nil:
		err := failure
		if err == nil {
			err = loopErr
		}
		slog.Warn("agent: run failed", "run", r.id, "err", err)
		rt.store.finishRun(bg, r.id, "error", err.Error(), r.in, r.out)
		code, retryable := "upstream_error", false
		var ge *GatewayError
		if errors.As(err, &ge) {
			retryable = ge.Retryable()
		}
		if errors.Is(err, turn.ErrTurnTruncated) {
			code = "upstream_truncated"
		}
		r.emit(chatstream.Event{Verb: chatstream.VerbRunError, Code: code, Retryable: retryable, Message: plain(err)})
	default:
		rt.store.finishRun(bg, r.id, "finished", string(finish), r.in, r.out)
		r.emit(chatstream.Event{Verb: chatstream.VerbRunFinish, Reason: string(finish), Usage: r.usage(chatstream.UsageFinal)})
	}
}

// plain is the error as the owner should read it: without the turn package's wrapping.
func plain(err error) string {
	var te *turn.TurnError
	if errors.As(err, &te) && te.Err != nil {
		err = te.Err
	}
	if errors.Is(err, turn.ErrTurnStalled) {
		return "The model stopped answering."
	}
	if errors.Is(err, turn.ErrTurnTruncated) {
		return "The model's answer was cut off."
	}
	return err.Error()
}

func (r *run) keep(ctx context.Context, role string, blocks []llmtypes.ContentBlock) {
	r.history = append(r.history, llmtypes.ChatMessage{Role: role, ContentBlocks: blocks})
	if _, err := r.rt.store.Append(context.WithoutCancel(ctx), r.thread, r.id, role, blocks, nil); err != nil {
		slog.Warn("agent: message not saved", "run", r.id, "err", err)
	}
}

// How long the page has to answer a tool call, and the owner a question.
const (
	toolTimeout     = 90 * time.Second
	approvalTimeout = 10 * time.Minute
	maxToolResult   = 60_000 // characters handed back to the model from one call
)

// tool runs one call: it shows the call, asks first when the tool's level requires it,
// relays it to the page, and shows the result.
func (r *run) tool(ctx context.Context, call llmtypes.ToolUseBlock) (content string, isError bool) {
	r.partN++
	callPart := fmt.Sprintf("p%d", r.partN)
	spec, known := r.tools[call.Name]
	input, _ := json.Marshal(call.Input)
	if call.Input == nil {
		input = []byte("{}")
	}
	r.emit(chatstream.Event{Verb: chatstream.VerbPartStart, PartID: callPart, Kind: string(chatstream.PartToolCall),
		Meta: meta(chatstream.MetaName, call.Name, chatstream.MetaCallID, call.ID, "level", spec.Level)})

	defer func() {
		if len(content) > maxToolResult {
			content = content[:maxToolResult] + "\n[cut: the result was longer than this]"
		}
		content = strings.ToValidUTF8(content, "")
		r.partN++
		id := fmt.Sprintf("p%d", r.partN)
		r.emit(chatstream.Event{Verb: chatstream.VerbPartStart, PartID: id, Kind: string(chatstream.PartToolResult),
			Meta: meta(chatstream.MetaCallID, callPart, chatstream.MetaIsError, isError)})
		r.emit(chatstream.Event{Verb: chatstream.VerbPartDelta, PartID: id, Text: content})
		r.emit(chatstream.Event{Verb: chatstream.VerbPartEnd, PartID: id})
	}()

	if !known {
		r.emit(chatstream.Event{Verb: chatstream.VerbPartEnd, PartID: callPart, Final: input})
		return fmt.Sprintf("There is no tool named %q.", call.Name), true
	}
	if spec.Level == LevelWrite && !r.allowed(ctx, call.Name) {
		decision := r.ask(ctx, call, input)
		switch decision {
		case "session":
			r.rt.mu.Lock()
			if r.rt.session[r.owner] == nil {
				r.rt.session[r.owner] = map[string]bool{}
			}
			r.rt.session[r.owner][call.Name] = true
			r.rt.mu.Unlock()
		case "always":
			if err := r.rt.store.grant(context.WithoutCancel(ctx), r.owner, call.Name); err != nil {
				slog.Warn("agent: permission not saved", "tool", call.Name, "err", err)
			}
		case "once":
		default:
			r.emit(chatstream.Event{Verb: chatstream.VerbPartEnd, PartID: callPart, Final: input})
			if decision == "timeout" {
				return "Nobody answered the permission question, so this did not run.", true
			}
			if decision == "cancelled" {
				return "This did not run: the owner stopped the run.", true
			}
			return "The owner declined this. Do not try it again; ask what they would like instead.", true
		}
	}

	// The call's final event carries who is to run it: the page that sent the message.
	wait := make(chan toolAnswer, 1)
	r.mu.Lock()
	r.waiting[call.ID] = wait
	r.mu.Unlock()
	r.emit(chatstream.Event{Verb: chatstream.VerbPartEnd, PartID: callPart, Final: input,
		Ext: meta("basalisk.exec", "client", "basalisk.client", r.client, "basalisk.call", call.ID)})
	timer := time.NewTimer(toolTimeout)
	defer timer.Stop()
	select {
	case a := <-wait:
		return a.content, a.isError
	case <-timer.C:
		r.forget(call.ID)
		return "The desktop did not answer in time. Its window may be closed.", true
	case <-ctx.Done():
		r.forget(call.ID)
		return "This did not run: the owner stopped the run.", true
	}
}

func (r *run) forget(callID string) {
	r.mu.Lock()
	delete(r.waiting, callID)
	r.mu.Unlock()
}

func (r *run) allowed(ctx context.Context, tool string) bool {
	r.rt.mu.Lock()
	ok := r.rt.session[r.owner][tool]
	r.rt.mu.Unlock()
	return ok || r.rt.store.granted(ctx, r.owner, tool)
}

// ask puts the permission question to the owner and waits for the answer.
func (r *run) ask(ctx context.Context, call llmtypes.ToolUseBlock, input []byte) string {
	id := newID("apr")
	ch := make(chan string, 1)
	r.mu.Lock()
	r.approvals[id] = ch
	r.mu.Unlock()
	descriptor, _ := json.Marshal(map[string]any{"tool": call.Name, "input": json.RawMessage(input), "description": r.tools[call.Name].Description})
	expires := time.Now().Add(approvalTimeout).UTC()
	r.emit(chatstream.Event{Verb: chatstream.VerbApprovalRequest, ApprovalID: id, CallID: call.ID,
		Reason: "This changes something.", Descriptor: descriptor, Mode: chatstream.ApprovalInBand, ExpiresAt: &expires})
	decision := ""
	timer := time.NewTimer(approvalTimeout)
	defer timer.Stop()
	select {
	case decision = <-ch:
	case <-timer.C:
		decision = "timeout"
	case <-ctx.Done():
		decision = "cancelled"
	}
	r.mu.Lock()
	delete(r.approvals, id)
	r.mu.Unlock()
	value, _ := json.Marshal(map[string]string{"approval_id": id, "decision": decision})
	r.emit(chatstream.Event{Verb: chatstream.VerbActivity, Kind: "basalisk.approval", Value: value})
	return decision
}
