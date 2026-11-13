package api

import (
	"net/http"
	"os"
	"strconv"

	"github.com/hollis-labs/basalisk/internal/macshell"
)

// EnableShell connects the native desktop shell. Without it GET /api/shell reports the
// shell as unavailable (a browser, or a server deployment) and nothing else is served.
func (a *API) EnableShell(c macshell.Controller) { a.shell = c }

func (a *API) shellRoutes() []route {
	rts := []route{{"GET", "/api/shell", ScopeRead, a.shellStatus}}
	if a.shell == nil {
		return rts
	}
	r, w := ScopeRead, ScopeWrite
	// Running script in the page is a development aid (it is how an agent drives the real
	// window); it is served only when the app was started with BASALISK_SHELL_DEBUG=1.
	if os.Getenv("BASALISK_SHELL_DEBUG") == "1" {
		rts = append(rts, route{"POST", "/api/shell/eval", w, a.shellEval})
	}
	return append(rts,
		route{"GET", "/api/shell/snapshot", r, a.shellSnapshot},
		route{"GET", "/api/shell/events", r, a.shellEvents},
		route{"GET", "/api/shell/windows", r, a.shellWindows},
		route{"POST", "/api/shell/kiosk", w, a.shellKiosk},
		route{"POST", "/api/shell/menu-bar", w, a.shellMenuBar},
		route{"POST", "/api/shell/swallow", w, a.shellSwallow},
		route{"POST", "/api/shell/fix-minimized", w, a.shellFixMinimized},
		route{"POST", "/api/shell/windows/unminimize", w, a.shellUnminimize},
		route{"POST", "/api/shell/permissions", w, a.shellPermission},
	)
}

func (a *API) shellStatus(w http.ResponseWriter, _ *http.Request) {
	if a.shell == nil {
		reply(w, 200, macshell.Status{MenuBarVisible: true, Combos: []string{}})
		return
	}
	reply(w, 200, a.shell.Status())
}

func (a *API) shellEvents(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	reply(w, 200, map[string]any{"events": a.shell.Events(after)})
}

func (a *API) shellWindows(w http.ResponseWriter, _ *http.Request) {
	wins, err := a.shell.Windows()
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	reply(w, 200, map[string]any{"windows": wins})
}

// shellDone answers a change with the new status, or the reason it was refused.
func (a *API) shellDone(w http.ResponseWriter, err error) {
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	reply(w, 200, a.shell.Status())
}

func (a *API) shellKiosk(w http.ResponseWriter, r *http.Request) {
	var in struct {
		On             bool `json:"on"`
		BlockSwitching bool `json:"block_switching"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.shellDone(w, a.shell.SetKiosk(in.On, in.BlockSwitching))
}

func (a *API) shellMenuBar(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Visible bool `json:"visible"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.shellDone(w, a.shell.SetMenuBar(in.Visible))
}

func (a *API) shellSwallow(w http.ResponseWriter, r *http.Request) {
	var in struct {
		On     bool     `json:"on"`
		Combos []string `json:"combos"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.shellDone(w, a.shell.SetSwallow(in.On, in.Combos))
}

func (a *API) shellFixMinimized(w http.ResponseWriter, r *http.Request) {
	var in struct {
		On bool `json:"on"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.shellDone(w, a.shell.SetFixMinimized(in.On))
}

func (a *API) shellUnminimize(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PID   int `json:"pid"`
		Index int `json:"index"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.shellDone(w, a.shell.Unminimize(in.PID, in.Index))
}

func (a *API) shellPermission(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind string `json:"kind"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.shellDone(w, a.shell.RequestPermission(in.Kind))
}

func (a *API) shellSnapshot(w http.ResponseWriter, _ *http.Request) {
	png, err := a.shell.Snapshot()
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(png)
}

func (a *API) shellEval(w http.ResponseWriter, r *http.Request) {
	var in struct {
		JS string `json:"js"`
	}
	if !decodeLimit(w, r, &in, 1<<20) {
		return
	}
	out, err := a.shell.Eval(in.JS)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}
