package api

import (
	"net/http"
	"strconv"
	"sync/atomic"

	"github.com/hollis-labs/basalisk/internal/assist"
)

// The assistant's record and its sorting of the owner's mail (internal/assist).

// EnableAssist turns the routes on. watch may be nil (no gateway: nothing to sort).
func (a *API) EnableAssist(log *assist.Log, watch *assist.Watcher) {
	a.assist, a.assistWatch = log, watch
}

// assistOwner is the owner whose page last asked: who a background run works for.
var assistOwner atomic.Value

// AssistOwner is the signed-in owner the desktop page last spoke as, or "".
func (a *API) AssistOwner() string {
	s, _ := assistOwner.Load().(string)
	return s
}

func (a *API) assistRoutes() []route {
	if a.assist == nil {
		return nil
	}
	r, w := ScopeRead, ScopeWrite
	rts := []route{{"GET", "/api/assist/activity", r, a.assistActivity}}
	if a.assistWatch != nil {
		rts = append(rts,
			route{"GET", "/api/assist/needs", r, a.assistNeeds},
			route{"POST", "/api/assist/mail/{id}", w, a.assistMail},
			route{"GET", "/api/assist/rules", r, a.assistRules},
			route{"DELETE", "/api/assist/rules/{id}", w, a.assistDropRule},
		)
	}
	return rts
}

func (a *API) assistActivity(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	list, err := a.assist.Recent(r.Context(), before, 80)
	if err != nil {
		fail(w, 500, "The activity log could not be read.")
		return
	}
	reply(w, 200, map[string]any{"activity": list})
}

// assistNeeds is what waits on the owner and what is held for them.
func (a *API) assistNeeds(w http.ResponseWriter, r *http.Request) {
	owner, err := a.ownerID(r)
	if err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	assistOwner.Store(owner)
	needs, err1 := a.assistWatch.Open(r.Context(), assist.ClassNeeds)
	held, err2 := a.assistWatch.Open(r.Context(), assist.ClassDigest)
	with, err3 := a.assistWatch.Open(r.Context(), assist.ClassBas)
	if err1 != nil || err2 != nil || err3 != nil {
		fail(w, 500, "The sorted mail could not be read.")
		return
	}
	reply(w, 200, map[string]any{"needs": needs, "held": held, "with_assistant": with})
}

// assistMail is the owner saying what to do with a sorted message: not now, done, or where
// its kind belongs from now on (which becomes a rule).
func (a *API) assistMail(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	var in struct {
		State string `json:"state"`
		// Rule, when set, sorts later mail like this one: class, and what to match on.
		Rule *struct {
			Class string `json:"class"`
			From  string `json:"from"`
			Kind  string `json:"kind"`
		} `json:"rule"`
	}
	if !decode(w, r, &in) {
		return
	}
	switch in.State {
	case "done", "dismissed", "open":
	default:
		fail(w, 400, "A message is done, dismissed or open.")
		return
	}
	class := ""
	out := map[string]any{}
	if in.Rule != nil {
		rule, err := a.assistWatch.AddRule(r.Context(), assist.Rule{From: in.Rule.From, Kind: in.Rule.Kind, Class: in.Rule.Class, Note: "from a card"})
		if err != nil {
			fail(w, 422, err.Error())
			return
		}
		out["rule"] = rule
		if in.State == "open" {
			class = in.Rule.Class
		}
	}
	if err := a.assistWatch.SetState(r.Context(), r.PathValue("id"), in.State, class); err != nil {
		fail(w, 500, "It could not be changed.")
		return
	}
	reply(w, 200, out)
}

func (a *API) assistRules(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	rules, err := a.assistWatch.Rules(r.Context())
	if err != nil {
		fail(w, 500, "The rules could not be read.")
		return
	}
	reply(w, 200, map[string]any{"rules": rules})
}

func (a *API) assistDropRule(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := a.assistWatch.DropRule(r.Context(), id); err != nil {
		fail(w, 500, "The rule could not be removed.")
		return
	}
	w.WriteHeader(204)
}
