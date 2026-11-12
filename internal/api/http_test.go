package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/core/store"
	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/libs/security/secrets"

	"github.com/hollis-labs/basalisk/internal/persistence/persistencetest"
)

// These tests exercise the handlers directly. The auth guard and the identity module are not
// in front of them here (boot_test.go in cmd/basalisk runs the real stack), so a request is
// "signed in" by stamping the verified principal the guard would have stamped.

// ownerPrincipal is the person every test signs in as.
var ownerPrincipal = libauth.Principal{Verified: true, ID: "user:usr_owner", Kind: "user", Display: "Owner", Scopes: []string{"*"}}

type fakeUsers map[string]User

func (f fakeUsers) User(_ context.Context, id string) (User, error) {
	if u, ok := f[id]; ok {
		return u, nil
	}
	return User{}, ErrNoUser
}

var testUsers = fakeUsers{"usr_owner": {Username: "owner", DisplayName: "Owner", Email: ""}}

// testStore is the Service plus the HTTP layer over it, so the tests read like the
// single object they exercised before.
type testStore struct {
	*Service
	api   *API
	store *store.Store
}

func (s *testStore) Handler() http.Handler          { return s.api.Handler() }
func (s *testStore) CompanionHandler() http.Handler { return s.api.CompanionHandler() }
func (s *testStore) Close() error                   { return s.store.Close() }

// Open opens (or reopens) the database in dir.
func Open(dir string) (*testStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	st, err := persistencetest.Open(filepath.Join(dir, "basalisk.db"))
	if err != nil {
		return nil, err
	}
	svc, err := New(st.DB(), secrets.New(secrets.WithStateDir(filepath.Join(dir, "secrets"))))
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	return &testStore{Service: svc, api: svc.API(testUsers), store: st}, nil
}

type client struct {
	t       *testing.T
	handler http.Handler
	// principal is who the guard would have verified; nil is an unauthenticated caller.
	principal *libauth.Principal
}

// signIn makes the client the owner.
func (c *client) signIn() { p := ownerPrincipal; c.principal = &p }

func (c *client) signOut() { c.principal = nil }

// sign stamps the client's principal on a hand-built request.
func (c *client) sign(r *http.Request) *http.Request {
	if c.principal == nil {
		return r
	}
	return r.WithContext(libauth.WithPrincipal(r.Context(), *c.principal))
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
	w := httptest.NewRecorder()
	c.handler.ServeHTTP(w, c.sign(r))
	if w.Code != want {
		c.t.Fatalf("%s %s: status=%d want=%d: %s", method, path, w.Code, want, w.Body.String())
	}
	return w
}

func newStore(t *testing.T) (*testStore, *client) {
	t.Helper()
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s, &client{t: t, handler: s.Handler()}
}

func TestAccountComposesIdentityAndPreferences(t *testing.T) {
	_, c := newStore(t)
	c.call("GET", "/api/account", nil, 401)
	c.call("GET", "/api/integrations", nil, 401)
	c.signIn()

	var p Profile
	if e := json.Unmarshal(c.call("GET", "/api/account", nil, 200).Body.Bytes(), &p); e != nil {
		t.Fatal(e)
	}
	if p != (Profile{Username: "owner", DisplayName: "Owner", Theme: "dark", Timezone: "UTC", Revision: 1}) {
		t.Fatalf("defaults: %+v", p)
	}
	raw := c.call("GET", "/api/account", nil, 200).Body.String()
	for _, key := range []string{`"username"`, `"displayName"`, `"email"`, `"theme"`, `"timezone"`, `"revision"`} {
		if !strings.Contains(raw, key) {
			t.Fatalf("account shape lost %s: %s", key, raw)
		}
	}

	// First save works against the default revision and bumps it.
	w := c.call("PUT", "/api/preferences", map[string]any{"theme": "light", "timezone": "America/Chicago", "revision": 1}, 200)
	if e := json.Unmarshal(w.Body.Bytes(), &p); e != nil {
		t.Fatal(e)
	}
	if p.Theme != "light" || p.Timezone != "America/Chicago" || p.Revision != 2 || p.Username != "owner" {
		t.Fatalf("saved: %+v", p)
	}
	// A stale revision, a bad theme and a bad zone are refused.
	c.call("PUT", "/api/preferences", map[string]any{"theme": "dark", "timezone": "UTC", "revision": 1}, 409)
	c.call("PUT", "/api/preferences", map[string]any{"theme": "neon", "timezone": "UTC", "revision": 2}, 400)
	c.call("PUT", "/api/preferences", map[string]any{"theme": "system", "timezone": "Invalid/Zone", "revision": 2}, 400)
	c.call("PUT", "/api/preferences", map[string]any{"theme": "system", "timezone": "Local", "revision": 2}, 400)
	c.call("PUT", "/api/preferences", map[string]any{"theme": "system", "timezone": "UTC", "revision": 2, "extra": 1}, 400)
	c.call("PUT", "/api/preferences", map[string]any{"theme": "system", "timezone": "UTC", "revision": 2}, 200)

	// Preferences belong to the person: another user starts from the defaults.
	other := ownerPrincipal
	other.ID = "user:usr_other"
	testUsers["usr_other"] = User{Username: "other", DisplayName: "Other"}
	defer delete(testUsers, "usr_other")
	c.principal = &other
	if e := json.Unmarshal(c.call("GET", "/api/account", nil, 200).Body.Bytes(), &p); e != nil {
		t.Fatal(e)
	}
	if p.Username != "other" || p.Theme != "dark" || p.Revision != 1 {
		t.Fatalf("preferences leaked between users: %+v", p)
	}

	// A caller that is not a person (an API key) has no account.
	service := libauth.Principal{Verified: true, ID: "key:key_1", Kind: "service", Scopes: []string{"*"}}
	c.principal = &service
	c.call("GET", "/api/account", nil, 403)
	c.call("PUT", "/api/preferences", map[string]any{"theme": "dark", "timezone": "UTC", "revision": 1}, 403)
}

func TestUnverifiedCallersAreRefusedByEveryRoute(t *testing.T) {
	s, c := newStore(t)
	unverified := libauth.Principal{ID: "user:usr_owner", Scopes: []string{"*"}} // not Verified
	c.principal = &unverified
	for _, rt := range s.api.routes() {
		path := strings.NewReplacer("{id}", "x").Replace(rt.Pattern)
		c.call(rt.Method, path, map[string]any{}, 401)
	}
}

func TestIntegrationSecretsNeverReturned(t *testing.T) {
	s, c := newStore(t)
	c.signIn()
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
	c.call("POST", "/api/integrations", map[string]any{"name": "Unsafe URL", "provider": "Example", "baseUrl": "https://user:pass@example.test", "credential": key}, 400) //nolint:gosec // a URL carrying credentials is the rejected input
}

func TestRestartPreservesPreferencesAndPrivateModes(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	c := &client{t: t, handler: s.Handler()}
	c.signIn()
	c.call("PUT", "/api/preferences", map[string]any{"theme": "light", "timezone": "UTC", "revision": 1}, 200)
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	c.handler = s.Handler()
	var p Profile
	if err := json.Unmarshal(c.call("GET", "/api/account", nil, 200).Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Theme != "light" || p.Revision != 2 {
		t.Fatalf("preferences did not survive a restart: %+v", p)
	}
	// Directory and file modes are the app's (internal/appdirs); the secrets store makes its own.
	info, e := os.Stat(filepath.Join(dir, "secrets"))
	if e == nil && info.Mode().Perm()&0o077 != 0 {
		t.Fatal("secrets directory is accessible to other users")
	}
}
