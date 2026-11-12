package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/hollis-labs/basalisk/internal/activity"
)

func activityReply(w http.ResponseWriter, value any, err error) {
	switch {
	case err == nil:
		reply(w, 200, value)
	case errors.Is(err, activity.ErrInvalid):
		fail(w, 400, "Enter a session title and valid context. A request ID is required.")
	case errors.Is(err, activity.ErrConflict):
		fail(w, 409, "This session changed, or the request conflicts. Refresh before trying again.")
	case errors.Is(err, activity.ErrNotFound):
		fail(w, 404, "Activity session not found.")
	default:
		fail(w, 500, "Could not read or save activity. You can safely retry the same request.")
	}
}

func beforeCursor(w http.ResponseWriter, r *http.Request) (int64, bool) {
	value := r.URL.Query().Get("before")
	if value == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		fail(w, 400, "Invalid history cursor.")
		return 0, false
	}
	return n, true
}

func (a *API) activityState(w http.ResponseWriter, r *http.Request) {
	owner, _ := a.ownerID(r)
	state, err := a.svc.activity.State(r.Context(), owner)
	activityReply(w, state, err)
}

func (a *API) activitySessions(w http.ResponseWriter, r *http.Request) {
	before, ok := beforeCursor(w, r)
	if !ok {
		return
	}
	owner, _ := a.ownerID(r)
	page, err := a.svc.activity.Sessions(r.Context(), owner, before)
	activityReply(w, page, err)
}

func (a *API) activityEvents(w http.ResponseWriter, r *http.Request) {
	before, ok := beforeCursor(w, r)
	if !ok {
		return
	}
	owner, _ := a.ownerID(r)
	page, err := a.svc.activity.Events(r.Context(), owner, r.PathValue("id"), before)
	activityReply(w, page, err)
}

func (a *API) activityCommand(w http.ResponseWriter, r *http.Request) {
	var command activity.Command
	if !decode(w, r, &command) {
		return
	}
	owner, _ := a.ownerID(r)
	result, err := a.svc.activity.Apply(r.Context(), owner, command)
	activityReply(w, result, err)
}
