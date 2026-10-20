package account

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/basalisk/internal/assurance"
	"github.com/hollis-labs/basalisk/internal/requestorigin"
)

func TestConfiguredAssuranceHostUsesPersistedAuthority(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if handler, err := s.ConfigureAssuranceHost(context.Background(), nil, nil); handler != nil || err != nil {
		t.Fatal("default activated assurance")
	}
	var authority string
	if err = s.db.QueryRow(`SELECT installation_id FROM activity_identity WHERE singleton=1`).Scan(&authority); err != nil {
		t.Fatal(err)
	}
	cfg := assurance.HostConfig{Authority: "wrong", Origin: requestorigin.CanonicalOrigin, RPID: requestorigin.CanonicalHost, Audiences: []string{"tether"}, CurrentTrust: assurance.CurrentTrustConfig{Origin: "https://issuer.test", Issuer: "issuer", ReaderTokenFile: "/not-installed/reader"}}
	cfg.Authentication.Issuer = "issuer"
	cfg.Authentication.JWKSURL = "https://issuer.test/.well-known/jwks.json"
	cfg.Authentication.Callers = []assurance.WorkloadPin{{Subject: "tachyon", Kind: "device", Audiences: []string{"tether"}}}
	cfg.Tachyon.Origin = "https://tachyon.test"
	cfg.Tachyon.Exchange = assurance.ExchangeConfig{Origin: "https://issuer.test", Issuer: "issuer", ParentTokenFile: "/not-installed/parent", Subject: "basalisk", Kind: "device"}
	if _, err = s.ConfigureAssuranceHost(context.Background(), &cfg, nil); err == nil || s.assurance != nil {
		t.Fatal("wrong authority installed")
	}
	cfg.Authority = authority
	h, err := s.ConfigureAssuranceHost(context.Background(), &cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/assurance/v1/status", strings.NewReader(`{}`)))
	if w.Code != 401 || s.assurance == nil || s.assuranceTachyon == nil {
		t.Fatal("configured status boundary not installed")
	}
	// Configuration does not grant browser access or bypass owner sessions.
	for _, path := range []string{"/api/assurance/deliver", "/api/assurance/outcome"} {
		r := httptest.NewRequest("POST", path, strings.NewReader(`{"completion_id":"not-a-credential"}`))
		r.Header.Set("X-Basalisk-Request", "1")
		r.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal("unauthenticated assurance browser route accepted")
		}
	}
}

func TestCanonicalProxyCookiesAndOrigin(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := requestorigin.Handler(true, s.Handler())
	call := func(path, origin, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", requestorigin.CanonicalOrigin+path, strings.NewReader(body))
		// Simulate the actual plaintext loopback hop from Caddy.
		r.TLS = nil
		r.RemoteAddr = "127.0.0.1:3210"
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Basalisk-Request", "1")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	body, _ := json.Marshal(setupBody())
	if w := call("/api/setup", "https://evil.example", string(body)); w.Code != 403 {
		t.Fatal("foreign origin accepted")
	}
	w := call("/api/setup", requestorigin.CanonicalOrigin, string(body))
	if w.Code != 200 {
		t.Fatalf("canonical setup: %d %s", w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Domain != "" {
		t.Fatal("proxy cookie is not secure and host-only")
	}
	if w := call("/api/login", "http://"+requestorigin.CanonicalHost, `{}`); w.Code != 403 {
		t.Fatal("downgraded origin accepted")
	}
}

func TestStableHumanIdentityAcrossSessionsAndRestart(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	c := &client{t: t, handler: s.Handler()}
	c.call("GET", "/api/identity", nil, 401)
	c.call("POST", "/api/setup", setupBody(), 200)
	first := c.call("GET", "/api/identity", nil, 200).Body.String()
	var identity map[string]string
	if e = json.Unmarshal([]byte(first), &identity); e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(identity["human_subject"], "usr_") || !strings.HasPrefix(identity["authority"], "bas_") || identity["assurance"] != "password-session" {
		t.Fatal("unstable identity mapping")
	}
	old := digest(c.cookie.Value)
	current, e := s.LookupSession(context.Background(), old)
	if e != nil || current.Subject != identity["human_subject"] {
		t.Fatal("handoff and browser identity differ", e)
	}
	c.call("POST", "/api/assurance/prepare", map[string]any{}, 503)
	c.call("POST", "/api/logout", map[string]any{}, 200)
	if _, e = s.LookupSession(context.Background(), old); e == nil {
		t.Fatal("logged out handoff accepted")
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	c.handler = s.Handler()
	c.call("POST", "/api/login", map[string]any{"username": "owner", "password": "long test passphrase"}, 200)
	if got := c.call("GET", "/api/identity", nil, 200).Body.String(); got != first {
		t.Fatal("identity changed after login/restart")
	}
	if _, e = s.LookupSession(context.Background(), identity["human_subject"]); e == nil {
		t.Fatal("subject used as bearer")
	}
}
