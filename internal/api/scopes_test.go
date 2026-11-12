package api

import (
	"net/http"
	"strings"
	"testing"

	libauth "github.com/hollis-labs/libs/security/auth"
)

// Every route the owner API serves has a scope in the table the guard enforces, reads need
// basalisk.read and changes need basalisk.write. The mux and the table are built from one
// route list, so this fails if either side stops using it.
func TestEveryOwnerRouteHasItsScope(t *testing.T) {
	s, _ := newStore(t)
	routes := s.api.routes()
	if len(routes) == 0 {
		t.Fatal("no routes")
	}
	table := libauth.NewScopeTable()
	s.api.RegisterScopes(table)
	for _, rt := range routes {
		concrete := strings.ReplaceAll(rt.Pattern, "{id}", "x")
		got, known := table.ForRoute(rt.Method, concrete)
		want := ScopeWrite
		if rt.Method == http.MethodGet {
			want = ScopeRead
		}
		if !known || got != want {
			t.Errorf("%s %s: scope=%q known=%v, want %q", rt.Method, concrete, got, known, want)
		}
		if !strings.HasPrefix(rt.Pattern, "/api/") {
			t.Errorf("%s %s is outside /api/, where the guard does not look", rt.Method, rt.Pattern)
		}
	}
	// A route nobody declared is unmapped, which the guard refuses in enforce mode.
	for _, undeclared := range [][2]string{{"GET", "/api/assurance/status"}, {"GET", "/api/identity-legacy"}, {"POST", "/api/account"}, {"DELETE", "/api/preferences"}} {
		if _, known := table.ForRoute(undeclared[0], undeclared[1]); known {
			t.Errorf("%s %s is mapped but not declared", undeclared[0], undeclared[1])
		}
	}
}

// The owner API serves exactly the paths the frontend contract lists.
func TestOwnerRouteInventory(t *testing.T) {
	s, _ := newStore(t)
	have := map[string]bool{}
	for _, rt := range s.api.routes() {
		have[rt.Method+" "+rt.Pattern] = true
	}
	for _, want := range []string{
		"GET /api/account", "PUT /api/preferences",
		"GET /api/integrations", "POST /api/integrations", "PUT /api/integrations/{id}/credential", "DELETE /api/integrations/{id}",
		"GET /api/activity", "GET /api/activity/sessions", "GET /api/activity/sessions/{id}/events", "GET /api/activity/sessions/{id}/artifacts", "POST /api/activity/commands",
		"GET /api/devices", "POST /api/devices/grants", "DELETE /api/devices/grants/{id}", "DELETE /api/devices/{id}",
		"GET /api/fragment-connections", "PUT /api/fragment-connections/{id}", "DELETE /api/fragment-connections/{id}",
		"GET /api/fragment-submissions", "GET /api/fragment-submissions/{id}", "DELETE /api/fragment-submissions/{id}", "POST /api/fragment-submissions/{id}/retry",
		"GET /api/capture-readiness", "GET /api/observations",
	} {
		if !have[want] {
			t.Errorf("missing route %s", want)
		}
		delete(have, want)
	}
	for extra := range have {
		t.Errorf("unlisted route %s", extra)
	}
}

// The companion routes sit at the paths the macOS app speaks and are open to the guard, because
// they authenticate device tokens themselves.
func TestCompanionMountsAreOpenAtTheFixedPaths(t *testing.T) {
	s, _ := newStore(t)
	mounts := s.api.Mounts()
	got := map[string]bool{}
	for _, m := range mounts {
		if !m.Open || m.Handler == nil {
			t.Errorf("mount %s must be open and have a handler", m.Pattern)
		}
		got[m.Pattern] = true
	}
	if !got["/auth/"] || !got["/companion/"] || len(mounts) != 2 {
		t.Fatalf("mounts = %v", got)
	}
	// Without a device credential every companion path answers 401 JSON, never the SPA or a 404.
	h := s.CompanionHandler()
	for _, p := range []string{"/auth/pair/exchange", "/auth/renew", "/companion/observations"} {
		w := nativeCall(t, h, "", "POST", p, map[string]any{}, 401)
		if !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
			t.Errorf("%s: %q", p, w.Header().Get("Content-Type"))
		}
	}
}
