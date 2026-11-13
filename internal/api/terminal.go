package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/coder/websocket"

	"github.com/hollis-labs/basalisk/internal/terminal"
)

// EnableTerminal turns the terminal routes on. Without it GET /api/terminal reports the
// terminal as disabled and nothing else is served: a shell is the most powerful thing this
// API can hand out, so a server deployment does not get one by default.
func (a *API) EnableTerminal(m *terminal.Manager) { a.term = m }

func (a *API) terminalRoutes() []route {
	rts := []route{{"GET", "/api/terminal", ScopeRead, a.terminalState}}
	if a.term == nil {
		return rts
	}
	// Every session route needs the write scope, the attach included: reading a shell is
	// using it.
	w := ScopeWrite
	return append(rts,
		route{"POST", "/api/terminal/sessions", w, a.terminalCreate},
		route{"PUT", "/api/terminal/sessions/{id}", w, a.terminalRename},
		route{"DELETE", "/api/terminal/sessions/{id}", w, a.terminalClose},
		route{"GET", "/api/terminal/sessions/{id}/ws", w, a.terminalAttach},
	)
}

func (a *API) terminalState(w http.ResponseWriter, _ *http.Request) {
	if a.term == nil {
		reply(w, 200, map[string]any{"enabled": false, "sessions": []terminal.Info{}})
		return
	}
	reply(w, 200, map[string]any{"enabled": true, "sessions": a.term.List(), "max_sessions": terminal.MaxSessions})
}

func (a *API) terminalCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Cwd   string `json:"cwd"`
		Title string `json:"title"`
	}
	if !decode(w, r, &in) {
		return
	}
	info, err := a.term.Create(terminal.Options{Cwd: in.Cwd, Title: in.Title})
	switch {
	case errors.Is(err, terminal.ErrTooMany):
		fail(w, 429, err.Error())
	case err != nil:
		fail(w, 400, err.Error())
	default:
		reply(w, 201, info)
	}
}

func (a *API) terminalRename(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title string `json:"title"`
	}
	if !decode(w, r, &in) {
		return
	}
	info, err := a.term.Rename(r.PathValue("id"), in.Title)
	switch {
	case errors.Is(err, terminal.ErrNotFound):
		fail(w, 404, "No such terminal session.")
	case err != nil:
		fail(w, 400, err.Error())
	default:
		reply(w, 200, info)
	}
}

func (a *API) terminalClose(w http.ResponseWriter, r *http.Request) {
	if err := a.term.Close(r.PathValue("id")); err != nil {
		fail(w, 404, "No such terminal session.")
		return
	}
	w.WriteHeader(204)
}

// The terminal socket. Binary frames are raw bytes in both directions; text frames are JSON
// control messages. The same wire protocol as Tachyon's chat terminal.
type terminalControl struct {
	Type string `json:"type"` // resize
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

type terminalEvent struct {
	Type  string `json:"type"` // state | exit
	State string `json:"state,omitempty"`
	Code  *int   `json:"code,omitempty"`
}

// sameOrigin refuses a socket opened by another site. The identity middleware checks Origin
// only on unsafe methods, and the upgrade is a GET, so the handler must do it.
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // not a browser
	}
	u, err := url.Parse(origin)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil && u.Host == r.Host
}

func (a *API) terminalAttach(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		fail(w, 403, "Cross-origin terminal connections are refused.")
		return
	}
	id := r.PathValue("id")
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = c.CloseNow() }()
	c.SetReadLimit(1 << 20)

	// The serve loop closes every session when the server stops (terminal.Manager.CloseAll),
	// which ends the frame loop below, so shutdown does not wait on an open terminal.
	ctx := r.Context()

	// The browser sends its size first, so the shell starts at the right size.
	cols, rows := uint16(100), uint16(30)
	var pending []byte
	rctx, rcancel := context.WithTimeout(ctx, 3*time.Second)
	typ, data, err := c.Read(rctx)
	rcancel()
	if err == nil && typ == websocket.MessageText {
		var ctl terminalControl
		if json.Unmarshal(data, &ctl) == nil && ctl.Type == "resize" {
			cols, rows = ctl.Cols, ctl.Rows
		}
	} else if err == nil {
		pending = data
	}

	att, err := a.term.Attach(id, cols, rows)
	if err != nil {
		_ = c.Close(websocket.StatusPolicyViolation, err.Error())
		return
	}
	defer att.Detach()
	att.Resize(cols, rows)

	send := func(ev terminalEvent) {
		b, _ := json.Marshal(ev)
		_ = c.Write(ctx, websocket.MessageText, b)
	}
	if len(att.Replay) > 0 && c.Write(ctx, websocket.MessageBinary, att.Replay) != nil {
		return
	}
	if len(pending) > 0 {
		_ = att.Input(pending)
	}
	send(terminalEvent{Type: "state", State: "open"})

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				if att.Input(data) != nil {
					return
				}
				continue
			}
			var ctl terminalControl
			if json.Unmarshal(data, &ctl) == nil && ctl.Type == "resize" {
				att.Resize(ctl.Cols, ctl.Rows)
			}
		}
	}()

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case f, ok := <-att.Frames:
			if !ok {
				if exited, code := att.Exited(); exited {
					send(terminalEvent{Type: "exit", Code: &code})
					_ = c.Close(websocket.StatusNormalClosure, "session ended")
				} else {
					_ = c.Close(websocket.StatusTryAgainLater, "too slow; reconnect")
				}
				return
			}
			if c.Write(ctx, websocket.MessageBinary, f) != nil {
				return
			}
		case <-ping.C:
			pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		case <-readDone:
			return
		}
	}
}
