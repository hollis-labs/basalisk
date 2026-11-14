package api

import (
	"net/http"
	"strconv"

	"github.com/hollis-labs/basalisk/internal/assist"
)

// The activity log (internal/assist): what the assistant did in the owner's name.

// EnableAssist turns the activity route on.
func (a *API) EnableAssist(log *assist.Log) { a.assist = log }

func (a *API) assistRoutes() []route {
	if a.assist == nil {
		return nil
	}
	return []route{{"GET", "/api/assist/activity", ScopeRead, a.assistActivity}}
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
