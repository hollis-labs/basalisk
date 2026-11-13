package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/hollis-labs/basalisk/internal/macshell"
)

// EnableBrowser turns the browser routes on: the agent's way to see and drive the desktop's
// Browser windows. Without it GET /api/browser reports the browser as disabled.
func (a *API) EnableBrowser(b macshell.Browser) { a.browser = b }

func (a *API) browserRoutes() []route {
	rts := []route{{"GET", "/api/browser", ScopeRead, a.browserList}}
	if a.browser == nil {
		return rts
	}
	// Driving a page (navigating it, running script in it) is using it: write scope.
	w := ScopeWrite
	return append(rts,
		route{"GET", "/api/browser/{id}/snapshot", w, a.browserSnapshot},
		route{"POST", "/api/browser/{id}/{op}", w, a.browserCommand},
	)
}

func (a *API) browserList(w http.ResponseWriter, _ *http.Request) {
	if a.browser == nil {
		reply(w, 200, map[string]any{"enabled": false, "views": []macshell.BrowserView{}})
		return
	}
	views, err := a.browser.List()
	if err != nil {
		fail(w, 500, "Could not list the browser views.")
		return
	}
	reply(w, 200, map[string]any{"enabled": true, "views": views})
}

func (a *API) browserSnapshot(w http.ResponseWriter, r *http.Request) {
	png, err := a.browser.Snapshot(r.PathValue("id"))
	if err != nil {
		fail(w, 404, "No such browser view, or the snapshot failed.")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(png) //nolint:gosec // a PNG the native layer rendered, served as image/png
}

// The operations an agent may run on a view. "open" and "frame" stay with the page: the
// window owns where the view is.
var browserOps = map[string]bool{
	"navigate": true, "back": true, "forward": true, "reload": true, "stop": true,
	"eval": true, "state": true, "focus": true, "zoom": true,
}

// browserCommand runs POST /api/browser/{id}/{op}; the body carries the op's fields
// ({"url"} for navigate, {"js"} for eval, {"zoom"} for zoom) and may be empty.
func (a *API) browserCommand(w http.ResponseWriter, r *http.Request) {
	op := r.PathValue("op")
	if !browserOps[op] {
		fail(w, 404, "Unknown browser operation.")
		return
	}
	in := map[string]any{}
	if r.ContentLength != 0 {
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		if d.Decode(&in) != nil {
			fail(w, 400, "Invalid request.")
			return
		}
	}
	in["op"], in["id"] = op, r.PathValue("id")
	out, err := a.browser.Command(in)
	if err != nil {
		status := 400
		if strings.Contains(err.Error(), "no such view") {
			status = 404
		}
		fail(w, status, err.Error())
		return
	}
	reply(w, 200, map[string]any{"result": out})
}
