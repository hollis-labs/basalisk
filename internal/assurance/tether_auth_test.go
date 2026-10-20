package assurance

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	auth "github.com/hollis-labs/libs/security/auth"
)

type currentTrustFunc func(context.Context, auth.Principal) error

func (f currentTrustFunc) CheckCurrentWorkload(ctx context.Context, p auth.Principal) error {
	return f(ctx, p)
}

func signedWorkload(t *testing.T, key ed25519.PrivateKey, header, claims any) string {
	t.Helper()
	h, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	c, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	body := enc.EncodeToString(h) + "." + enc.EncodeToString(c)
	return body + "." + enc.EncodeToString(ed25519.Sign(key, []byte(body)))
}

func TestTetherStatusRealJWTAndCurrentTrust(t *testing.T) {
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var requests, checks atomic.Int32
	var failKeys, revoked, expiredDuringCheck, redirect, rotated atomic.Bool
	var clock atomic.Int64
	clock.Store(time.Now().Unix())
	started := clock.Load()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Cache-Control") != "no-cache, no-store" {
			t.Error("JWKS request permitted an intermediary stale cache")
		}
		if r.URL.Path != "/.well-known/jwks.json" {
			t.Error("untrusted JWKS path")
		}
		if failKeys.Load() {
			w.WriteHeader(503)
			return
		}
		if redirect.Load() {
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
			return
		}
		kid := "key1"
		if rotated.Load() {
			kid = "key2"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kid": kid, "kty": "OKP", "crv": "Ed25519", "use": "sig", "x": base64.RawURLEncoding.EncodeToString(pub)}}})
	}))
	defer server.Close()
	cfg := TetherStatusConfig{
		Issuer: "tether-test", JWKSURL: server.URL + "/.well-known/jwks.json", Client: server.Client(),
		Callers: []WorkloadPin{{Subject: "msg://device/local/tachyon", Kind: "device", Audiences: []string{"tether"}}},
		Now:     func() time.Time { return time.Unix(clock.Load(), 0) },
		Current: currentTrustFunc(func(_ context.Context, p auth.Principal) error {
			checks.Add(1)
			if !p.Verified || p.ID != p.Parent || p.TokenID != "token1" || p.Audience != StatusTokenAudience || len(p.Scopes) != 1 || p.Scopes[0] != StatusTokenScope {
				t.Error("current lookup did not receive authenticated claims")
			}
			if revoked.Load() {
				return ErrDenied
			}
			if expiredDuringCheck.Load() {
				clock.Store(started + 60)
			}
			return nil
		}),
	}
	a, err := NewTetherStatusAuth(cfg)
	if err != nil {
		t.Fatal(err)
	}
	header := map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": "key1"}
	base := workloadClaims{Issuer: cfg.Issuer, Subject: cfg.Callers[0].Subject, Audience: StatusTokenAudience, Scope: StatusTokenScope, Expiry: started + 60, Issued: started, ID: "token1", Kind: "device", Parent: cfg.Callers[0].Subject}
	request := func(token string) (StatusCaller, error) {
		r := httptest.NewRequest("POST", "https://basalisk.nanite.cloud/assurance/v1/status", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		return a.AuthenticateStatus(r.Context(), r)
	}
	good := signedWorkload(t, private, header, base)
	caller, err := request(good)
	if err != nil || caller.Subject != base.Subject || len(caller.Audiences) != 1 || caller.Audiences[0] != "tether" {
		t.Fatal(caller, err)
	}
	// Returned allowlists cannot mutate the server policy.
	caller.Audiences[0] = "other"
	caller, err = request(good)
	if err != nil || caller.Audiences[0] != "tether" {
		t.Fatal(caller, err)
	}
	for name, change := range map[string]func(*workloadClaims){
		"issuer":       func(c *workloadClaims) { c.Issuer = "other" },
		"audience":     func(c *workloadClaims) { c.Audience = "tachyon-enrollment" },
		"scope prefix": func(c *workloadClaims) { c.Scope = "basalisk.assurance_read" },
		"wildcard":     func(c *workloadClaims) { c.Scope = "basalisk-status.*" },
		"extra scope":  func(c *workloadClaims) { c.Scope += " basalisk-status.admin" },
		"subject":      func(c *workloadClaims) { c.Subject = "other" },
		"parent":       func(c *workloadClaims) { c.Parent = "operator" },
		"operator":     func(c *workloadClaims) { c.Kind = "operator" },
		"session":      func(c *workloadClaims) { c.Session = "session" },
		"expired":      func(c *workloadClaims) { c.Expiry = started },
		"future":       func(c *workloadClaims) { c.Issued = started + 1 },
		"nbf":          func(c *workloadClaims) { c.Before = started + 1 },
		"long lived":   func(c *workloadClaims) { c.Expiry = started + 901 },
		"no id":        func(c *workloadClaims) { c.ID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			c := base
			change(&c)
			before := checks.Load()
			if _, err := request(signedWorkload(t, private, header, c)); err == nil {
				t.Fatal("accepted")
			}
			if checks.Load() != before {
				t.Fatal("unverified claims reached current trust")
			}
		})
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"iss":"tether-test","iss":"tether-test"}`),
		json.RawMessage(`{"aud":["basalisk-status"]}`),
	} {
		if _, err := request(signedWorkload(t, private, header, raw)); err == nil {
			t.Fatal("ambiguous claims accepted")
		}
	}
	_, wrongKey, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := request(signedWorkload(t, wrongKey, header, base)); err == nil {
		t.Fatal("bad signature accepted")
	}
	for _, bad := range []map[string]string{
		{"alg": "none", "typ": "JWT", "kid": "key1"},
		{"alg": "EdDSA", "typ": "JWT", "kid": "unknown"},
		{"alg": "EdDSA", "typ": "JWT", "kid": "key1", "jku": server.URL},
	} {
		if _, err := request(signedWorkload(t, private, bad, base)); err == nil {
			t.Fatal("untrusted header accepted")
		}
	}
	for _, flag := range []*atomic.Bool{&revoked, &failKeys, &redirect, &rotated} {
		flag.Store(true)
		if _, err := request(good); err == nil {
			t.Fatal("cached-good trust used")
		}
		flag.Store(false)
	}
	expiredDuringCheck.Store(true)
	if _, err := request(good); err == nil {
		t.Fatal("expired during current lookup accepted")
	}
	expiredDuringCheck.Store(false)
	clock.Store(started)
	before := requests.Load()
	var group sync.WaitGroup
	for range 12 {
		group.Go(func() {
			if _, err := request(good); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if requests.Load()-before != 12 {
		t.Fatal("concurrent lookup reused signing-key snapshot")
	}
	r := httptest.NewRequest("POST", "/assurance/v1/status", nil)
	r.AddCookie(&http.Cookie{Name: "basalisk_session", Value: "not-workload-auth", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	if _, err := a.AuthenticateStatus(r.Context(), r); !errors.Is(err, ErrDenied) {
		t.Fatal("cookie accepted")
	}
	r.Header.Add("Authorization", "Bearer "+good)
	r.Header.Add("Authorization", "Bearer "+good)
	if _, err := a.AuthenticateStatus(r.Context(), r); err == nil {
		t.Fatal("multiple bearers accepted")
	}
	cfg.Current = nil
	if _, err := NewTetherStatusAuth(cfg); err == nil {
		t.Fatal("current trust optional")
	}
}
