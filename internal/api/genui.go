package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	hostgenui "github.com/hollis-labs/basalisk/internal/genui"
	libauth "github.com/hollis-labs/libs/security/auth"
	contract "github.com/hollis-labs/substrate/genui"
	"github.com/hollis-labs/substrate/genui/gateway"
)

// EnableGenUI binds the same database and preference writer used by the ordinary
// account API. Missing identity or ports refuses construction; no listener opens.
func (a *API) EnableGenUI(users hostgenui.Users, producers []hostgenui.ProducerEnrollment) (*hostgenui.Host, error) {
	h, e := hostgenui.New(a.svc.preferences, users, producers)
	if e != nil {
		return nil, e
	}
	a.genui = h
	return h, nil
}
func (a *API) genuiRoutes() []route {
	if a.genui == nil {
		return nil
	}
	h, e := gateway.NewHTTP(a.genui.Service, a.genui, hostgenui.ScopePrefix)
	if e != nil {
		return nil
	}
	handlers := h.Handlers()
	return []route{
		{"POST", "/api/genui/surfaces", hostgenui.ScopePrefix + ".show", handlers["POST /api/genui/surfaces"].ServeHTTP},
		{"POST", "/api/genui/actions", hostgenui.ScopePrefix + ".act", handlers["POST /api/genui/actions"].ServeHTTP},
		{"GET", "/api/genui/actions/{actionID}", hostgenui.ScopePrefix + ".read", handlers["GET /api/genui/actions/{actionID}"].ServeHTTP},
		{"GET", "/api/genui/surfaces", hostgenui.ScopePrefix + ".read", a.genuiList},
		{"GET", "/api/genui/surfaces/{id}", hostgenui.ScopePrefix + ".read", a.genuiGet},
		{"POST", "/api/genui/confirm", hostgenui.ScopePrefix + ".confirm", a.genuiConfirm},
		{"POST", "/api/genui/producer/surfaces", hostgenui.ScopePrefix + ".show", a.genuiProducer},
	}
}
func genuiFail(w http.ResponseWriter, e error) {
	status := 503
	switch {
	case errors.Is(e, gateway.ErrForbidden):
		status = 403
	case errors.Is(e, gateway.ErrNotFound):
		status = 404
	case errors.Is(e, gateway.ErrInvalid):
		status = 400
	}
	fail(w, status, "GenUI request could not be completed.")
}
func (a *API) genuiScope(w http.ResponseWriter, r *http.Request, op string) (gateway.Scope, bool) {
	p, ok := libauth.FromContext(r.Context())
	if !ok {
		fail(w, 401, "Verified identity required.")
		return gateway.Scope{}, false
	}
	s, e := a.genui.Authorize(r.Context(), p, op)
	if e != nil {
		genuiFail(w, e)
		return s, false
	}
	return s, true
}
func (a *API) genuiList(w http.ResponseWriter, r *http.Request) {
	s, ok := a.genuiScope(w, r, "read")
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		genuiFail(w, gateway.ErrInvalid)
		return
	}
	v, e := a.genui.List(r.Context(), s)
	if e != nil {
		genuiFail(w, e)
		return
	}
	reply(w, 200, map[string]any{"surfaces": v})
}
func (a *API) genuiGet(w http.ResponseWriter, r *http.Request) {
	s, ok := a.genuiScope(w, r, "read")
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		genuiFail(w, gateway.ErrInvalid)
		return
	}
	v, e := a.genui.Get(r.Context(), s, r.PathValue("id"))
	if e != nil {
		genuiFail(w, e)
		return
	}
	reply(w, 200, v)
}
func genuiBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" {
		return nil, gateway.ErrInvalid
	}
	b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, contract.MaxActionBytes))
	if e != nil {
		return nil, gateway.ErrInvalid
	}
	return b, nil
}
func boundedPrincipal(r *http.Request) (context.Context, context.CancelFunc) {
	p, _ := libauth.FromContext(r.Context())
	if !p.ExpiresAt.IsZero() {
		return context.WithDeadline(r.Context(), p.ExpiresAt)
	}
	return context.WithCancel(r.Context())
}
func (a *API) genuiConfirm(w http.ResponseWriter, r *http.Request) {
	s, ok := a.genuiScope(w, r, "confirm")
	if !ok {
		return
	}
	b, e := genuiBody(w, r)
	if e != nil {
		genuiFail(w, e)
		return
	}
	ctx, cancel := boundedPrincipal(r)
	defer cancel()
	proof, e := a.genui.Confirm(ctx, s, b)
	if e != nil {
		genuiFail(w, e)
		return
	}
	reply(w, 200, map[string]string{"confirmation": proof})
}
func (a *API) genuiProducer(w http.ResponseWriter, r *http.Request) {
	p, ok := libauth.FromContext(r.Context())
	if !ok || !p.Verified || (!p.ExpiresAt.IsZero() && !time.Now().Before(p.ExpiresAt)) {
		fail(w, 401, "Verified identity required.")
		return
	}
	b, e := genuiBody(w, r)
	if e != nil {
		genuiFail(w, e)
		return
	}
	ctx, cancel := boundedPrincipal(r)
	defer cancel()
	v, e := a.genui.ShowProducer(ctx, p, b)
	if e != nil {
		genuiFail(w, e)
		return
	}
	reply(w, 200, v)
}
