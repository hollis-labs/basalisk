package account

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/basalisk/internal/activity"
	"github.com/hollis-labs/libs/security/pairing"
)

func nativeCall(t *testing.T, h http.Handler, token, method, path string, body any, want int) *httptest.ResponseRecorder {
	t.Helper()
	var b bytes.Buffer
	if body != nil {
		if e := json.NewEncoder(&b).Encode(body); e != nil {
			t.Fatal(e)
		}
	}
	r := httptest.NewRequest(method, "http://localhost"+path, &b)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(pairing.ProtocolHeader, pairing.Version)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, want, w.Body.String())
	}
	return w
}
func pairDevice(t *testing.T, c *client, h http.Handler, scopes []string) pairing.Exchange {
	t.Helper()
	g := responseAs[pairing.Grant](t, c.call("POST", "/api/devices/grants", map[string]string{"label": "Test Mac"}, 201))
	w := nativeCall(t, h, "", "POST", pairing.ExchangePath, pairing.ExchangeRequest{Code: g.Code, Audience: "basalisk", Scopes: scopes}, 200)
	return responseAs[pairing.Exchange](t, w)
}
func TestCompanionPairingBoundaries(t *testing.T) {
	s, c := newStore(t)
	h := s.CompanionHandler()
	c.call("POST", "/api/devices/grants", map[string]string{"label": "Mac"}, 401)
	c.call("POST", "/api/setup", setupBody(), 200)
	// Owner cookies do not authenticate the device API.
	r := httptest.NewRequest("GET", "http://localhost/companion/context", nil)
	r.AddCookie(c.cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("owner cookie admitted on device API")
	}
	r = httptest.NewRequest("POST", "http://localhost/auth/pair/exchange", strings.NewReader(`{}`))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("missing protocol admitted")
	}
	x := pairDevice(t, c, h, []string{scopeContext})
	nativeCall(t, h, x.Token, "GET", "/companion/context", nil, 200)
	nativeCall(t, h, x.Token, "POST", "/companion/observations", map[string]any{}, 403)
	nativeCall(t, s.Handler(), x.Token, "GET", "/api/account", nil, 401)
	listed := c.call("GET", "/api/devices", nil, 200).Body.String()
	if strings.Contains(listed, x.Token) || strings.Contains(listed, "token_hash") {
		t.Fatal("device list exposed credentials")
	}
	c.call("DELETE", "/api/devices/"+x.Device.ID, nil, 200)
	nativeCall(t, h, x.Token, "GET", "/companion/context", nil, 401)
	nativeCall(t, h, x.Token, "POST", pairing.RenewPath, map[string]any{}, 401)
}

// Separate limiter witness avoids assuming test setup consumed no exchange.
func TestPairingRateLimit(t *testing.T) {
	s, _ := newStore(t)
	h := s.CompanionHandler()
	for range 10 {
		nativeCall(t, h, "", "POST", pairing.ExchangePath, pairing.ExchangeRequest{Code: "invalid", Audience: "basalisk"}, 401)
	}
	nativeCall(t, h, "", "POST", pairing.ExchangePath, pairing.ExchangeRequest{Code: "invalid", Audience: "basalisk"}, 429)
}

func TestObservationAttributionRetryPauseAndRetention(t *testing.T) {
	s, c := newStore(t)
	h := s.CompanionHandler()
	c.call("POST", "/api/setup", setupBody(), 200)
	session := command(c, activity.Command{RequestID: "companion-start-001", Action: "start", Title: "Focus test", Project: "Original"}, 200)
	x := pairDevice(t, c, h, nil)
	now := time.Now().UTC()
	event := observation{Sequence: 1, OccurredAt: now.Format(timestampFormat), Kind: "app.activated", BundleID: "test.editor", AppName: "Editor", SessionID: session.ID, ContextID: session.Context.ID, ContextAsOf: now.Add(-time.Millisecond).Format(timestampFormat)}
	batch := observationBatch{Generation: "test-generation-001", Events: []observation{event}}
	for range 2 {
		nativeCall(t, h, x.Token, "POST", "/companion/observations", batch, 200)
	}
	items := responseAs[[]map[string]string](t, c.call("GET", "/api/observations", nil, 200))
	if len(items) != 1 || items[0]["contextId"] != session.Context.ID || items[0]["source"] != "macos.workspace" {
		t.Fatal("retry or attribution failed", items)
	}
	conflict := batch
	conflict.Events = []observation{event}
	conflict.Events[0].AppName = "Different"
	nativeCall(t, h, x.Token, "POST", "/companion/observations", conflict, 409)
	paused := command(c, activity.Command{RequestID: "companion-pause-001", Action: "pause", SessionID: session.ID, Revision: 1}, 200)
	event.Sequence = 2
	event.OccurredAt = time.Now().UTC().Format(timestampFormat)
	batch.Events = []observation{event}
	receipt := responseAs[map[string]int](t, nativeCall(t, h, x.Token, "POST", "/companion/observations", batch, 200))
	if receipt["ignored"] != 1 {
		t.Fatal("paused observation admitted")
	}
	current := command(c, activity.Command{RequestID: "companion-context01", Action: "context", SessionID: session.ID, Revision: paused.Revision, Project: "Next"}, 200)
	current = command(c, activity.Command{RequestID: "companion-resume01", Action: "resume", SessionID: session.ID, Revision: current.Revision}, 200)
	event.Sequence = 3
	event.OccurredAt = time.Now().UTC().Format(timestampFormat)
	batch.Events = []observation{event}
	receipt = responseAs[map[string]int](t, nativeCall(t, h, x.Token, "POST", "/companion/observations", batch, 200))
	if receipt["ignored"] != 1 {
		t.Fatal("stale context attributed to new project")
	}
	event.Sequence = 4
	event.ContextID = current.Context.ID
	event.ContextAsOf = time.Now().Add(-time.Millisecond).UTC().Format(timestampFormat)
	event.OccurredAt = time.Now().UTC().Format(timestampFormat)
	batch.Events = []observation{event}
	nativeCall(t, h, x.Token, "POST", "/companion/observations", batch, 200)
	if _, e := s.db.Exec("UPDATE companion_observations SET occurred_at=?", time.Now().Add(-6*time.Minute).UTC().Format(timestampFormat)); e != nil {
		t.Fatal(e)
	}
	items = responseAs[[]map[string]string](t, c.call("GET", "/api/observations", nil, 200))
	if len(items) != 0 {
		t.Fatal("retention view exposed expired history")
	}
	c.call("DELETE", "/api/devices/"+x.Device.ID, nil, 200)
	nativeCall(t, h, x.Token, "POST", "/companion/observations", batch, 401)
}
