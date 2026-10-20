package account

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type client struct {
	t       *testing.T
	handler http.Handler
	cookie  *http.Cookie
}

func (c *client) call(method, path string, body any, want int) *httptest.ResponseRecorder {
	c.t.Helper()
	var b bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&b).Encode(body); err != nil {
			c.t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, "http://localhost"+path, &b)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Basalisk-Request", "1")
	r.Header.Set("Origin", "http://localhost")
	if c.cookie != nil {
		r.AddCookie(c.cookie)
	}
	w := httptest.NewRecorder()
	c.handler.ServeHTTP(w, r)
	if w.Code != want {
		c.t.Fatalf("%s %s: status=%d want=%d: %s", method, path, w.Code, want, w.Body.String())
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == sessionCookie {
			c.cookie = cookie
		}
	}
	return w
}
func setupBody() map[string]any {
	return map[string]any{"username": "owner", "displayName": "Owner", "password": "long test passphrase"}
}
func newStore(t *testing.T) (*Store, *client) {
	t.Helper()
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s, &client{t: t, handler: s.Handler()}
}
func TestOwnerLifecycle(t *testing.T) {
	s, c := newStore(t)
	c.call("GET", "/api/account", nil, 401)
	c.call("GET", "/api/integrations", nil, 401)
	c.call("POST", "/api/setup", setupBody(), 200)
	oldCookie := *c.cookie
	if !c.cookie.HttpOnly || c.cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe session cookie")
	}
	c.call("POST", "/api/setup", setupBody(), 409)
	var p Profile
	w := c.call("PUT", "/api/account", map[string]any{"displayName": "New name", "email": "me@example.test", "revision": 1}, 200)
	if e := json.Unmarshal(w.Body.Bytes(), &p); e != nil {
		t.Fatal(e)
	}
	if p.DisplayName != "New name" || p.Revision != 2 {
		t.Fatal("profile was not updated")
	}
	c.call("PUT", "/api/account", map[string]any{"displayName": "Stale", "email": "", "revision": 1}, 409)
	c.call("PUT", "/api/preferences", map[string]any{"theme": "light", "timezone": "America/Chicago", "revision": 2}, 200)
	c.call("PUT", "/api/preferences", map[string]any{"theme": "light", "timezone": "Invalid/Zone", "revision": 3}, 400)
	c.call("PUT", "/api/password", map[string]any{"currentPassword": "wrong", "newPassword": "new long passphrase"}, 400)
	c.call("PUT", "/api/password", map[string]any{"currentPassword": "long test passphrase", "newPassword": "new long passphrase"}, 200)
	c.cookie = &oldCookie
	c.call("GET", "/api/account", nil, 401)
	c.call("POST", "/api/login", map[string]any{"username": "owner", "password": "long test passphrase"}, 401)
	c.call("POST", "/api/login", map[string]any{"username": "owner", "password": "new long passphrase"}, 200)
	var hash string
	if e := s.db.QueryRow("SELECT password_hash FROM owner").Scan(&hash); e != nil {
		t.Fatal(e)
	}
	if hash == "new long passphrase" || !strings.HasPrefix(hash, "$2") {
		t.Fatal("password was not hashed")
	}
	c.call("POST", "/api/logout", map[string]any{}, 200)
	c.call("GET", "/api/account", nil, 401)
}
func TestCrossSiteWritesAndExpiredSessions(t *testing.T) {
	s, c := newStore(t)
	for _, origin := range []string{"https://evil.test", "http://localhost.evil.test"} {
		b, _ := json.Marshal(setupBody())
		r := httptest.NewRequest("POST", "http://localhost/api/setup", bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Basalisk-Request", "1")
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		c.handler.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("cross-origin setup allowed: %d", w.Code)
		}
	}
	r := httptest.NewRequest("POST", "http://localhost/api/setup", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	c.handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("missing CSRF header allowed")
	}
	c.call("POST", "/api/setup", setupBody(), 200)
	if _, e := s.db.Exec("UPDATE sessions SET expires=?", time.Now().Add(-time.Hour).Unix()); e != nil {
		t.Fatal(e)
	}
	c.call("GET", "/api/account", nil, 401)
}
func TestIntegrationSecretsNeverReturned(t *testing.T) {
	s, c := newStore(t)
	c.call("POST", "/api/setup", setupBody(), 200)
	key := "test-secret-do-not-return"
	w := c.call("POST", "/api/integrations", map[string]any{"name": "Example", "provider": "Example API", "baseUrl": "https://api.example.test", "credential": key}, 201)
	if strings.Contains(w.Body.String(), key) || strings.Contains(w.Body.String(), "file://") {
		t.Fatal("creation leaked credential")
	}
	var i Integration
	if e := json.Unmarshal(w.Body.Bytes(), &i); e != nil {
		t.Fatal(e)
	}
	w = c.call("GET", "/api/integrations", nil, 200)
	if strings.Contains(w.Body.String(), key) || strings.Contains(w.Body.String(), "file://") {
		t.Fatal("list leaked credential")
	}
	if !strings.Contains(w.Body.String(), `"credentialPresent":true`) {
		t.Fatal("missing credential state")
	}
	c.call("PUT", "/api/integrations/"+i.ID+"/credential", map[string]any{"credential": "replacement-secret"}, 200)
	result, e := s.secrets.Resolve(t.Context(), "file://basalisk/"+i.ID)
	if e != nil || result.Secret.Reveal() != "replacement-secret" {
		t.Fatal("rotation failed")
	}
	c.call("DELETE", "/api/integrations/"+i.ID, nil, 200)
	if _, e = s.secrets.Resolve(t.Context(), "file://basalisk/"+i.ID); e == nil {
		t.Fatal("credential survived deletion")
	}
	c.call("PUT", "/api/integrations/unknown/credential", map[string]any{"credential": "replacement-secret"}, 404)
	c.call("POST", "/api/integrations", map[string]any{"name": "Unsafe URL", "provider": "Example", "baseUrl": "https://user:pass@example.test", "credential": key}, 400)
}
func TestRestartPreservesOwnerAndSession(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	c := &client{t: t, handler: s.Handler()}
	c.call("POST", "/api/setup", setupBody(), 200)
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	c.handler = s.Handler()
	c.call("GET", "/api/account", nil, 200)
	c.call("POST", "/api/setup", setupBody(), 409)
	for _, path := range []string{dir, filepath.Join(dir, "basalisk.db")} {
		info, e := os.Stat(path)
		if e != nil {
			t.Fatal(e)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatal("private data is accessible to other users")
		}
	}
}
func TestConcurrentSetupCreatesOnlyOneOwner(t *testing.T) {
	_, c := newStore(t)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wg.Go(func() {
			b, _ := json.Marshal(setupBody())
			r := httptest.NewRequest("POST", "http://localhost/api/setup", bytes.NewReader(b))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Basalisk-Request", "1")
			w := httptest.NewRecorder()
			c.handler.ServeHTTP(w, r)
			codes <- w.Code
		})
	}
	wg.Wait()
	close(codes)
	success, conflict := false, false
	for code := range codes {
		if code == 200 {
			success = true
		} else if code == 409 {
			conflict = true
		} else {
			t.Fatalf("unexpected setup status %d", code)
		}
	}
	if !success || !conflict {
		t.Fatal("concurrent setup did not close after first owner")
	}
}
func TestLoginRateLimit(t *testing.T) {
	_, c := newStore(t)
	for range 10 {
		c.call("POST", "/api/login", map[string]any{"username": "owner", "password": "wrong"}, 401)
	}
	c.call("POST", "/api/login", map[string]any{"username": "owner", "password": "wrong"}, 429)
}
