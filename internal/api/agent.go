package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/hollis-labs/libs/agent/core/transport/httpstream"
	"github.com/hollis-labs/libs/ui/ui-go/streamhub"

	"github.com/hollis-labs/basalisk/internal/agent"
)

// The agent (internal/agent, docs/agent.md): the owner's messages in, the run's event
// stream out, and the two things a run can wait on the page for: the result of a tool the
// page runs, and the answer to a permission question.

// EnableAgent turns the agent routes on. Without it GET /api/agent answers "not available".
func (a *API) EnableAgent(rt *agent.Runtime) { a.agent = rt }

func (a *API) agentRoutes() []route {
	r, w := ScopeRead, ScopeWrite
	rts := []route{{"GET", "/api/agent", r, a.agentState}}
	if a.agent == nil {
		return rts
	}
	return append(rts,
		route{"GET", "/api/agent/thread", r, a.agentThread},
		route{"DELETE", "/api/agent/thread", w, a.agentNewThread},
		route{"POST", "/api/agent/turns", w, a.agentTurn},
		route{"GET", "/api/agent/runs/{id}/events", r, a.agentEvents},
		route{"POST", "/api/agent/runs/{id}/cancel", w, a.agentCancel},
		route{"POST", "/api/agent/runs/{id}/tool-results", w, a.agentToolResult},
		route{"POST", "/api/agent/runs/{id}/tool-alive", w, a.agentToolAlive},
		route{"POST", "/api/agent/runs/{id}/approvals", w, a.agentApproval},
		route{"GET", "/api/agent/tools", r, a.agentTools},
		route{"GET", "/api/agent/grants", r, a.agentGrants},
		route{"DELETE", "/api/agent/grants/{tool}", w, a.agentRevoke},
	)
}

func (a *API) agentState(w http.ResponseWriter, _ *http.Request) {
	if a.agent == nil {
		reply(w, 200, map[string]any{"available": false, "ready": false, "model": ""})
		return
	}
	cfg := a.agent.Config()
	// ready: a model can be called. available without ready: the panel says what to set.
	reply(w, 200, map[string]any{"available": true, "ready": cfg.Ready(), "model": cfg.Model, "rules_problem": a.agent.Router().Problem(), "reflexes_problem": a.agent.Reflexes().Problem()})
}

func (a *API) agentThread(w http.ResponseWriter, r *http.Request) {
	owner, err := a.ownerID(r)
	if err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	thread, err := a.agent.Store().MainThread(r.Context(), owner)
	if err != nil {
		fail(w, 500, "The conversation could not be read.")
		return
	}
	msgs, err := a.agent.Store().Messages(r.Context(), thread)
	if err != nil {
		fail(w, 500, "The conversation could not be read.")
		return
	}
	reply(w, 200, map[string]any{"thread_id": thread, "messages": msgs, "active_run": a.agent.ActiveRun(thread)})
}

// agentNewThread puts the running thread away; the next message starts a fresh one.
func (a *API) agentNewThread(w http.ResponseWriter, r *http.Request) {
	owner, err := a.ownerID(r)
	if err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	if thread, err := a.agent.Store().MainThread(r.Context(), owner); err == nil && a.agent.ActiveRun(thread) != "" {
		fail(w, 409, "The agent is still working. Stop it first.")
		return
	}
	if err := a.agent.Store().ArchiveMain(r.Context(), owner); err != nil {
		fail(w, 500, "The conversation could not be put away.")
		return
	}
	w.WriteHeader(204)
}

func (a *API) agentTurn(w http.ResponseWriter, r *http.Request) {
	owner, err := a.ownerID(r)
	if err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	var in struct {
		Text     string           `json:"text"`
		ClientID string           `json:"client_id"`
		Context  json.RawMessage  `json:"context"`
		Tools    []agent.ToolSpec `json:"tools"`
		Hints    agent.Hints      `json:"hints"`
	}
	if !decodeLimit(w, r, &in, 512<<10) {
		return
	}
	id, err := a.agent.Start(r.Context(), agent.StartInput{Owner: owner, Text: in.Text, ClientID: in.ClientID, Context: in.Context, Tools: in.Tools, Hints: in.Hints})
	switch {
	case errors.Is(err, agent.ErrNotReady):
		fail(w, 503, "No model is configured for the agent.")
	case errors.Is(err, agent.ErrBusy):
		fail(w, 409, "The agent is still working on the last message.")
	case err != nil:
		fail(w, 400, err.Error())
	default:
		reply(w, 200, map[string]string{"run_id": id})
	}
}

// agentRun checks that the run in the path is the caller's.
func (a *API) agentRun(w http.ResponseWriter, r *http.Request) (owner, id string, ok bool) {
	owner, err := a.ownerID(r)
	if err != nil {
		fail(w, 401, "Please sign in.")
		return "", "", false
	}
	id = r.PathValue("id")
	if a.agent.Store().RunOwner(r.Context(), id) != owner {
		fail(w, 404, "No such run.")
		return "", "", false
	}
	return owner, id, true
}

// agentEvents streams a run as chatstream/v1 server-sent events. A reader that was cut off
// resumes with Last-Event-ID; a finished run can be replayed for a few minutes.
func (a *API) agentEvents(w http.ResponseWriter, r *http.Request) {
	_, id, ok := a.agentRun(w, r)
	if !ok {
		return
	}
	after, err := httpstream.ParseCursor(r)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	sub, err := a.agent.Hub().Subscribe(r.Context(), id, streamhub.SubscribeOptions{After: streamhub.Seq(after)})
	if err != nil {
		// The run ended long enough ago that its events are gone: the thread has the result.
		fail(w, 410, "That run's events are no longer kept.")
		return
	}
	_ = httpstream.Write(w, r, sub, httpstream.State{RunID: id, After: after}, false)
}

func (a *API) agentCancel(w http.ResponseWriter, r *http.Request) {
	owner, id, ok := a.agentRun(w, r)
	if !ok {
		return
	}
	// A run that already ended has nothing to stop: that is not an error.
	_ = a.agent.Cancel(owner, id)
	w.WriteHeader(204)
}

func (a *API) agentToolResult(w http.ResponseWriter, r *http.Request) {
	owner, id, ok := a.agentRun(w, r)
	if !ok {
		return
	}
	var in struct {
		CallID  string `json:"call_id"`
		Content string `json:"content"`
		IsError bool   `json:"is_error"`
	}
	if !decodeLimit(w, r, &in, 2<<20) {
		return
	}
	if err := a.agent.ToolResult(owner, id, in.CallID, in.Content, in.IsError); err != nil {
		fail(w, 409, err.Error())
		return
	}
	w.WriteHeader(204)
}

// agentToolAlive is the page saying it is still running a call. 409 tells it the run has
// stopped waiting, so it must not go on to change anything.
func (a *API) agentToolAlive(w http.ResponseWriter, r *http.Request) {
	owner, id, ok := a.agentRun(w, r)
	if !ok {
		return
	}
	var in struct {
		CallID string `json:"call_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := a.agent.ToolAlive(owner, id, in.CallID); err != nil {
		fail(w, 409, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (a *API) agentApproval(w http.ResponseWriter, r *http.Request) {
	owner, id, ok := a.agentRun(w, r)
	if !ok {
		return
	}
	var in struct {
		ApprovalID string `json:"approval_id"`
		Decision   string `json:"decision"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := a.agent.Approve(owner, id, in.ApprovalID, in.Decision); err != nil {
		fail(w, 409, err.Error())
		return
	}
	w.WriteHeader(204)
}

// agentTools lists the tools the server runs itself (the page knows its own).
func (a *API) agentTools(w http.ResponseWriter, r *http.Request) {
	reply(w, 200, map[string]any{"tools": a.agent.ServerTools(r.Context())})
}

func (a *API) agentGrants(w http.ResponseWriter, r *http.Request) {
	owner, err := a.ownerID(r)
	if err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	grants, err := a.agent.Store().Grants(r.Context(), owner)
	if err != nil {
		fail(w, 500, "The permissions could not be read.")
		return
	}
	reply(w, 200, map[string]any{"tools": grants})
}

func (a *API) agentRevoke(w http.ResponseWriter, r *http.Request) {
	owner, err := a.ownerID(r)
	if err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	if err := a.agent.Store().Revoke(r.Context(), owner, r.PathValue("tool")); err != nil {
		fail(w, 500, "The permission could not be removed.")
		return
	}
	w.WriteHeader(204)
}
