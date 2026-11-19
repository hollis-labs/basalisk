package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/hollis-labs/substrate/apierr"

	"github.com/hollis-labs/basalisk/internal/gatecrasher"
)

// Approvals from Gatecrasher (internal/gatecrasher): the cards that wait on the owner, and
// their decisions. No route here takes a secret: a request_secret card's value is posted by
// the page straight to the card's intake, and decide refuses any field it does not know.

// EnableGate turns the approval routes on.
func (a *API) EnableGate(in *gatecrasher.Inbox) { a.gate = in }

func (a *API) gateRoutes() []route {
	if a.gate == nil {
		return nil
	}
	r, w := ScopeRead, ScopeWrite
	return []route{
		{"GET", "/api/gate/cards", r, a.gateCards},
		{"POST", "/api/gate/cards/{id}/decide", w, a.gateDecide},
		{"POST", "/api/gate/cards/{id}/refresh", w, a.gateRefresh},
	}
}

// gateCards lists the cards. With ?v=<version> it waits (up to 25s) until they change.
func (a *API) gateCards(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	if v := r.URL.Query().Get("v"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			a.gate.Wait(r.Context(), n, 25*time.Second)
		}
	}
	cards, version, online := a.gate.List()
	waiting := 0
	for _, c := range cards {
		if c.Waiting() {
			waiting++
		}
	}
	reply(w, 200, map[string]any{"cards": cards, "version": version, "online": online, "waiting": waiting})
}

func (a *API) gateDecide(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	var d gatecrasher.Decision
	if !decode(w, r, &d) {
		return
	}
	if err := d.Check(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	card, err := a.gate.Decide(r.Context(), r.PathValue("id"), d)
	var de *gatecrasher.DecideError
	switch {
	case errors.As(err, &de):
		code := de.Code
		if code == "" {
			code = errorCode(de.Status)
		}
		apierr.Write(w, de.Status, code, de.Message, nil)
	case err != nil:
		fail(w, 502, "Gatecrasher could not be reached.")
	default:
		reply(w, 200, card)
	}
}

func (a *API) gateRefresh(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	card, err := a.gate.Refresh(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, 404, "That approval is gone.")
		return
	}
	reply(w, 200, card)
}
