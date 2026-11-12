package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// started boots the real stack (every module the manifest selects, the real migrations, the
// real handler chain) on a temp data directory and a free loopback port, and returns its base
// URL. It is the acceptance test: nothing is stubbed between the HTTP client and the database.
func started(t *testing.T, dataDir string, override string) string {
	t.Helper()
	env := map[string]string{"BASALISK_DATA_DIR": dataDir}
	if override != "" {
		file := filepath.Join(t.TempDir(), "override.yaml")
		if err := os.WriteFile(file, []byte(override), 0o600); err != nil {
			t.Fatal(err)
		}
		env["SUBSTRATE_CONFIG"] = file
	}
	// The config module reads SUBSTRATE_CONFIG from the process environment.
	if f := env["SUBSTRATE_CONFIG"]; f != "" {
		t.Setenv("SUBSTRATE_CONFIG", f)
	}
	ctx, cancel := context.WithCancel(context.Background())
	run, err := bootWith(ctx, func(k string) string { return env[k] }, func(cfg map[string]any) {
		cfg["httphost"] = map[string]any{"addr": "127.0.0.1:0"}
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	addr := make(chan net.Addr, 1)
	b := &basalisk{app: run.App, ready: func(a net.Addr) { addr <- a }}
	done := make(chan error, 1)
	// httphost.Serve binds the configured address; port 0 asks for a free one.
	go func() { done <- b.serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve ended with %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("serve did not stop on cancel")
		}
		_ = run.Stop(context.Background())
	})
	select {
	case a := <-addr:
		return "http://" + a.String()
	case err := <-done:
		t.Fatalf("serve returned before listening: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("serve never listened")
	}
	return ""
}

type session struct {
	t    *testing.T
	base string
	http *http.Client
	// xrw adds the X-Requested-With header the identity module requires of cookie writes.
	xrw bool
}

func newSession(t *testing.T, base string) *session {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &session{t: t, base: base, http: &http.Client{Jar: jar, Timeout: 10 * time.Second}, xrw: true}
}

func (s *session) do(method, path string, body any, headers map[string]string) (int, []byte) {
	s.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			s.t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, s.base+path, rdr)
	if err != nil {
		s.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.xrw {
		req.Header.Set("X-Requested-With", "basalisk-ui")
	}
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	res, err := s.http.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out
}

func (s *session) want(status int, method, path string, body any) map[string]any {
	s.t.Helper()
	got, out := s.do(method, path, body, nil)
	if got != status {
		s.t.Fatalf("%s %s: %d want %d: %s", method, path, got, status, out)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	return m
}

func TestAcceptance(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	// A cheap password hash keeps the suite fast; it also exercises the SUBSTRATE_CONFIG override.
	base := started(t, dir, "identity:\n  bcrypt_cost: 4\n")

	// The host is up and unauthenticated health is open.
	anon := newSession(t, base)
	anon.want(200, "GET", "/healthz", nil)
	// Every API route is closed to an anonymous caller (enforce mode), except the identity entry points.
	anon.want(401, "GET", "/api/account", nil)
	anon.want(401, "GET", "/api/integrations", nil)
	anon.want(401, "POST", "/api/activity/commands", map[string]any{})
	if st := anon.want(200, "GET", "/api/setup", nil); st["setup_required"] != true {
		t.Fatalf("first run should require setup: %v", st)
	}

	// First-run setup creates the owner and signs in.
	owner := newSession(t, base)
	owner.want(200, "POST", "/api/setup", map[string]any{"display_name": "Owner", "username": "owner", "password": "a long test passphrase"})
	owner.want(409, "POST", "/api/setup", map[string]any{"display_name": "Again", "username": "again", "password": "a long test passphrase"})

	// Login on a fresh browser session works and a wrong password does not.
	login := newSession(t, base)
	login.want(401, "POST", "/api/login", map[string]any{"username": "owner", "password": "wrong wrong wrong"})
	login.want(200, "POST", "/api/login", map[string]any{"username": "owner", "password": "a long test passphrase"})

	// The account view composes the identity user and the preferences, in the shape the frontend reads.
	acct := login.want(200, "GET", "/api/account", nil)
	if acct["username"] != "owner" || acct["displayName"] != "Owner" || acct["email"] != "" || acct["theme"] != "dark" || acct["timezone"] != "UTC" || acct["revision"] != float64(1) {
		t.Fatalf("account: %v", acct)
	}
	// Writes need the CSRF header; the preferences keep their optimistic revision.
	login.xrw = false
	login.want(403, "PUT", "/api/preferences", map[string]any{"theme": "light", "timezone": "America/Chicago", "revision": 1})
	login.xrw = true
	prefs := login.want(200, "PUT", "/api/preferences", map[string]any{"theme": "light", "timezone": "America/Chicago", "revision": 1})
	if prefs["theme"] != "light" || prefs["revision"] != float64(2) {
		t.Fatalf("preferences: %v", prefs)
	}
	login.want(409, "PUT", "/api/preferences", map[string]any{"theme": "dark", "timezone": "UTC", "revision": 1})
	// Profile edits are the identity module's.
	login.want(200, "PUT", "/api/identity/profile", map[string]any{"display_name": "The Owner", "email": "owner@example.test"})
	if acct = login.want(200, "GET", "/api/account", nil); acct["displayName"] != "The Owner" || acct["email"] != "owner@example.test" || acct["theme"] != "light" {
		t.Fatalf("account after profile edit: %v", acct)
	}

	// Integrations: create, rotate, delete; the credential never comes back.
	created := login.want(201, "POST", "/api/integrations", map[string]any{"name": "Example", "provider": "Example API", "baseUrl": "https://api.example.test", "credential": "secret-value-1"})
	id, _ := created["id"].(string)
	if id == "" || created["credentialPresent"] != true {
		t.Fatalf("integration: %v", created)
	}
	login.want(200, "PUT", "/api/integrations/"+id+"/credential", map[string]any{"credential": "secret-value-2"})
	if _, raw := login.do("GET", "/api/integrations", nil, nil); strings.Contains(string(raw), "secret-value") || strings.Contains(string(raw), "file://") {
		t.Fatalf("integration list leaked a credential: %s", raw)
	}
	login.want(200, "DELETE", "/api/integrations/"+id, nil)
	login.want(404, "DELETE", "/api/integrations/"+id, nil)

	// A start of work, a device grant, the companion exchange and an observation.
	act := login.want(200, "POST", "/api/activity/commands", map[string]any{"requestId": "acceptance-start-0001", "action": "start", "title": "Acceptance", "project": "Basalisk"})
	sessionID, _ := act["id"].(string)
	ctxID := act["context"].(map[string]any)["id"].(string)
	grant := login.want(201, "POST", "/api/devices/grants", map[string]any{"label": "Test Mac"})
	code, _ := grant["code"].(string)
	if code == "" {
		t.Fatalf("grant: %v", grant)
	}
	device := newSession(t, base)
	device.xrw = false // the companion is not a browser
	proto := map[string]string{"Hollis-Pairing-Protocol": "1"}
	status, out := device.do("POST", "/auth/pair/exchange", map[string]any{"code": code, "audience": "basalisk"}, proto)
	if status != 200 {
		t.Fatalf("exchange: %d %s", status, out)
	}
	var x struct {
		Device struct {
			ID string `json:"id"`
		} `json:"device"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(out, &x); err != nil || x.Token == "" {
		t.Fatalf("exchange body: %s", out)
	}
	bearer := map[string]string{"Hollis-Pairing-Protocol": "1", "Authorization": "Bearer " + x.Token}
	if status, out = device.do("GET", "/companion/context", nil, bearer); status != 200 || !strings.Contains(string(out), sessionID) {
		t.Fatalf("companion context: %d %s", status, out)
	}
	// The observation happens after the session started, within the context's freshness.
	now := time.Now().UTC()
	stamp := "2006-01-02T15:04:05.000000000Z"
	batch := map[string]any{"generation": "acceptance-generation-1", "events": []map[string]any{{
		"sequence": 1, "occurredAt": now.Format(stamp), "kind": "app.activated", "bundleId": "test.editor", "appName": "Editor",
		"activitySessionId": sessionID, "contextId": ctxID, "contextAsOf": now.Add(-time.Millisecond).Format(stamp),
	}}}
	if status, out = device.do("POST", "/companion/observations", batch, bearer); status != 200 || !strings.Contains(string(out), `"accepted":1`) {
		t.Fatalf("observation: %d %s", status, out)
	}
	if _, raw := login.do("GET", "/api/observations", nil, nil); !strings.Contains(string(raw), `"appName":"Editor"`) {
		t.Fatalf("observation not listed: %s", raw)
	}
	// Companion paths are not the SPA, and a browser session is not a device credential.
	if status, _ = device.do("GET", "/companion/context", nil, proto); status != 401 {
		t.Fatalf("companion without a device token: %d", status)
	}
	if status, _ = login.do("GET", "/companion/context", nil, proto); status != 401 {
		t.Fatalf("a browser session admitted on the device API: %d", status)
	}
	// A device token is no credential for the owner API.
	if status, _ = device.do("GET", "/api/account", nil, bearer); status != 401 && status != 403 {
		t.Fatalf("device token on the owner API: %d", status)
	}
	// Revoking the device ends its access.
	login.want(200, "DELETE", "/api/devices/"+x.Device.ID, nil)
	if status, _ = device.do("POST", "/companion/observations", batch, bearer); status != 401 {
		t.Fatalf("revoked device: %d", status)
	}

	// Scopes: a reader reads and cannot write; a user with neither sees nothing of the app.
	login.want(201, "POST", "/api/identity/users", map[string]any{"display_name": "Reader", "username": "reader", "password": "a reader test passphrase", "scopes": []string{"basalisk.read"}})
	reader := newSession(t, base)
	reader.want(200, "POST", "/api/login", map[string]any{"username": "reader", "password": "a reader test passphrase"})
	reader.want(200, "GET", "/api/account", nil)
	reader.want(200, "GET", "/api/integrations", nil)
	reader.want(403, "PUT", "/api/preferences", map[string]any{"theme": "dark", "timezone": "UTC", "revision": 1})
	reader.want(403, "POST", "/api/integrations", map[string]any{"name": "x", "provider": "x", "credential": "x"})
	reader.want(403, "GET", "/api/identity/users", nil)

	// The Host check: only loopback names are served.
	if status, _ = anon.do("GET", "/healthz", nil, map[string]string{"Host": "evil.test"}); status != 403 {
		t.Fatalf("foreign Host: %d", status)
	}

	// The data directory and its files are private.
	for _, p := range []string{dir, filepath.Join(dir, "basalisk.db"), filepath.Join(dir, "identity-signing.key")} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is accessible to other users: %v", p, info.Mode())
		}
	}
}

func TestStartupRefusesAPreSubstrateDatabase(t *testing.T) {
	for name, setup := range map[string]string{
		"owner table":  `CREATE TABLE owner(id INTEGER PRIMARY KEY, username TEXT)`,
		"user_version": `PRAGMA user_version=8`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			createLegacy(t, filepath.Join(dir, "basalisk.db"), setup)
			_, err := prepare(func(k string) string { return map[string]string{"BASALISK_DATA_DIR": dir}[k] })
			if err == nil || !strings.Contains(err.Error(), "move the old data directory aside") {
				t.Fatalf("err = %v, want the move-aside instruction", err)
			}
		})
	}
}

func TestOverrideFileMovesTheGuardedDatabase(t *testing.T) {
	// The guard judges the file the database module will open, which SUBSTRATE_CONFIG can change.
	legacy := filepath.Join(t.TempDir(), "elsewhere.db")
	createLegacy(t, legacy, `CREATE TABLE owner(id INTEGER PRIMARY KEY)`)
	override := filepath.Join(t.TempDir(), "o.yaml")
	if err := os.WriteFile(override, []byte("database:\n  sqlite:\n    path: "+legacy+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"BASALISK_DATA_DIR": t.TempDir(), "SUBSTRATE_CONFIG": override}
	if _, err := prepare(func(k string) string { return env[k] }); err == nil {
		t.Fatal("the overridden database was not guarded")
	}
}

func TestPrepareWritesAbsolutePathsIntoTheDataDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "d")
	out, err := prepare(func(k string) string { return map[string]string{"BASALISK_DATA_DIR": dir}[k] })
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]map[string]any
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg["database"]["sqlite"].(map[string]any)["path"]; got != filepath.Join(dir, "basalisk.db") {
		t.Errorf("database path = %v", got)
	}
	if got := cfg["identity"]["key_file"]; got != filepath.Join(dir, "identity-signing.key") {
		t.Errorf("identity key = %v", got)
	}
	if got := cfg["secrets"]["state_dir"]; got != filepath.Join(dir, "secrets") {
		t.Errorf("secrets dir = %v", got)
	}
	if cfg["auth"]["mode"] != "enforce" {
		t.Errorf("auth mode = %v, want enforce", cfg["auth"]["mode"])
	}
}

func TestServeRefusesANonLoopbackAddress(t *testing.T) {
	dir := t.TempDir()
	run, err := bootWith(context.Background(), func(k string) string { return map[string]string{"BASALISK_DATA_DIR": dir}[k] }, func(cfg map[string]any) {
		cfg["httphost"] = map[string]any{"addr": "0.0.0.0:8085"}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Stop(context.Background()) }()
	if err := (&basalisk{app: run.App}).serve(context.Background()); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("err = %v, want a loopback refusal", err)
	}
}

func createLegacy(t *testing.T, path, statement string) {
	t.Helper()
	db, err := sqlOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(statement); err != nil {
		t.Fatal(err)
	}
}
