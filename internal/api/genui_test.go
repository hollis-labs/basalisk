package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"modernc.org/sqlite"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	hostgenui "github.com/hollis-labs/basalisk/internal/genui"
	"github.com/hollis-labs/basalisk/internal/persistence"
	"github.com/hollis-labs/libs/security/secrets"
	"github.com/hollis-labs/substrate/boot"
	contract "github.com/hollis-labs/substrate/genui"
	"github.com/hollis-labs/substrate/genui/gateway"
	"github.com/hollis-labs/substrate/modules/auth"
	"github.com/hollis-labs/substrate/modules/database"
	_ "github.com/hollis-labs/substrate/modules/health"
	"github.com/hollis-labs/substrate/modules/httphost"
	"github.com/hollis-labs/substrate/modules/identity"
	_ "github.com/hollis-labs/substrate/modules/logging"
)

// These fixtures use actual own identity, guard, HTTP and file SQLite, including
// the same app preference writer. There are no fabricated principals or grants.
type genuiStack struct {
	t                           *testing.T
	dir                         string
	run                         *boot.Running
	server                      *httptest.Server
	db                          *sql.DB
	id                          *identity.Service
	api                         *API
	host                        *hostgenui.Host
	producer, producerID, owner string
	executionTimeout            atomic.Int64
}

func startGenui(t *testing.T, dir string, prior *genuiStack) *genuiStack {
	t.Helper()
	fs, root := persistence.Migrations()
	database.UseMigrationFS(fs, root)
	cfg := fmt.Sprintf(`{"database":{"sqlite":{"path":%q,"synchronous":"FULL"}},"auth":{"mode":"enforce"},"identity":{"key_file":%q,"bcrypt_cost":4,"setup":"http"}}`, filepath.Join(dir, "app.db"), filepath.Join(dir, "identity.key"))
	run, e := boot.Start(t.Context(), "basalisk", []string{"health", "logging", "auth", "database", "httphost", "identity"}, cfg)
	if e != nil {
		t.Fatal(e)
	}
	f := &genuiStack{t: t, dir: dir, run: run, db: database.From(run.App).DB(), id: identity.From(run.App)}
	if prior == nil {
		u, setupErr := f.id.SetupOwner(t.Context(), identity.OwnerInput{Username: "owner", DisplayName: "Owner", Password: "fixture account password"})
		if setupErr != nil {
			t.Fatal(setupErr)
		}
		f.owner = "user:" + u.ID
		key, c, keyErr := f.id.CreateKey(t.Context(), identity.KeyInput{Name: "isolated registered producer", Scopes: []string{hostgenui.ScopePrefix + ".show"}, Audiences: []string{"basalisk"}})
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		f.producer, f.producerID = key, "key:"+c.ID
	} else {
		f.owner, f.producer, f.producerID = prior.owner, prior.producer, prior.producerID
	}
	svc, e := New(f.db, secrets.New(secrets.WithStateDir(filepath.Join(dir, "secrets"))))
	if e != nil {
		t.Fatal(e)
	}
	f.api = svc.API(IdentityUsers{Svc: f.id})
	f.host, e = f.api.EnableGenUI(f.id, []hostgenui.ProducerEnrollment{{Subject: f.producerID, OwnerID: f.owner}})
	if e != nil {
		t.Fatal(e)
	}
	f.api.RegisterScopes(auth.ScopeTable(run.App))
	inner, e := httphost.Handler(run.App, httphost.Options{Routes: f.api.Handler(), RawPrefixes: []string{"/api/genui/"}})
	if e != nil {
		t.Fatal(e)
	}
	handler := f.id.Middleware(inner)
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d := time.Duration(f.executionTimeout.Load()); d > 0 && r.URL.Path == "/api/genui/actions" {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			r = r.WithContext(ctx)
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(f.close)
	return f
}
func (f *genuiStack) close() {
	if f.server != nil {
		f.server.Close()
		f.server = nil
	}
	if f.run != nil {
		_ = f.run.Stop(context.WithoutCancel(f.t.Context()))
		f.run = nil
	}
}
func (f *genuiStack) client(username string) *http.Client {
	f.t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	f.want(c, "", "POST", "/api/login", map[string]string{"username": username, "password": "fixture account password"}, 200)
	return c
}
func (f *genuiStack) request(c *http.Client, token, method, path string, body any) (int, []byte, error) {
	var raw []byte
	var e error
	if body != nil {
		raw, e = json.Marshal(body)
		if e != nil {
			return 0, nil, e
		}
	}
	r, e := http.NewRequestWithContext(f.t.Context(), method, f.server.URL+path, bytes.NewReader(raw))
	if e != nil {
		return 0, nil, e
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Requested-With", "genui-test")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	res, e := c.Do(r)
	if e != nil {
		return 0, nil, e
	}
	defer func() { _ = res.Body.Close() }()
	b, e := io.ReadAll(res.Body)
	return res.StatusCode, b, e
}
func (f *genuiStack) want(c *http.Client, token, method, path string, body any, status int) []byte {
	f.t.Helper()
	got, b, e := f.request(c, token, method, path, body)
	if e != nil || got != status {
		f.t.Fatalf("%s %s got %d want %d: %s (%v)", method, path, got, status, b, e)
	}
	return b
}
func (f *genuiStack) show(c *http.Client) contract.Envelope {
	f.t.Helper()
	b := f.want(c, "", "POST", "/api/genui/surfaces", map[string]any{"pattern": "form", "params": map[string]any{}}, 200)
	v, e := contract.DecodeEnvelope(b)
	if e != nil {
		f.t.Fatal(e)
	}
	return v
}
func actionFor(t *testing.T, s contract.Envelope, index int) contract.Call {
	t.Helper()
	choices, e := contract.Choices(s)
	if e != nil {
		t.Fatal(e)
	}
	return contract.Call{Version: contract.ProtocolVersion, CallAgentFunction: contract.ActionRequest{SurfaceID: s.ID, FunctionCallID: fmt.Sprintf("invocation-%d", time.Now().UnixNano()), CallFunction: choices[index].CallFunction}}
}
func outcome(t *testing.T, b []byte) *contract.Outcome {
	t.Helper()
	v, e := contract.ReadResponse(b)
	if e != nil || v.Response.AgentFunctionResponse.Value == nil {
		t.Fatalf("no native outcome: %s %v", b, e)
	}
	return v.Response.AgentFunctionResponse.Value
}
func (f *genuiStack) submit(c *http.Client, call contract.Call) *contract.Outcome {
	return outcome(f.t, f.want(c, "", "POST", "/api/genui/actions", call, 200))
}
func (f *genuiStack) next(c *http.Client, o *contract.Outcome) contract.Envelope {
	f.t.Helper()
	if o.Status != contract.StatusOK {
		f.t.Fatalf("not ok: %+v", o)
	}
	var next struct {
		SurfaceID string `json:"surfaceId"`
	}
	if json.Unmarshal(o.Result, &next) != nil || next.SurfaceID == "" {
		f.t.Fatal("missing next surface")
	}
	b := f.want(c, "", "GET", "/api/genui/surfaces/"+next.SurfaceID, nil, 200)
	v, e := contract.DecodeEnvelope(b)
	if e != nil {
		f.t.Fatal(e)
	}
	return v
}
func (f *genuiStack) approval(c *http.Client, theme string) contract.Call {
	s := f.show(c)
	call := actionFor(f.t, s, 0)
	call.CallAgentFunction.CallFunction.Args.Input["draft"] = map[string]any{"theme": theme, "timezone": "UTC"}
	a := f.next(c, f.submit(c, call))
	return actionFor(f.t, a, 0)
}
func (f *genuiStack) ack(c *http.Client, call contract.Call) contract.Call {
	f.t.Helper()
	if o := f.submit(c, call); o.Status != contract.StatusNeedsConfirmation {
		f.t.Fatalf("expected bound ack: %+v", o)
	}
	var result struct {
		Confirmation string `json:"confirmation"`
	}
	b := f.want(c, "", "POST", "/api/genui/confirm", call, 200)
	if json.Unmarshal(b, &result) != nil || result.Confirmation == "" {
		f.t.Fatal("missing acknowledgment")
	}
	call.CallAgentFunction.CallFunction.Args.Action.Confirmation = result.Confirmation
	return call
}
func TestGenUIRealIdentityPreferencesRecoveryConcurrency(t *testing.T) {
	f := startGenui(t, t.TempDir(), nil)
	c := f.client("owner")
	f.want(http.DefaultClient, "", "POST", "/api/genui/surfaces", map[string]any{}, 401)
	call := f.ack(c, f.approval(c, "light"))
	var wg sync.WaitGroup
	type result struct {
		status int
		b      []byte
		e      error
	}
	results := make(chan result, 2)
	for range 2 {
		wg.Go(func() {
			status, b, e := f.request(c, "", "POST", "/api/genui/actions", call)
			results <- result{status, b, e}
		})
	}
	wg.Wait()
	close(results)
	success := 0
	for r := range results {
		if r.e != nil || (r.status != 200 && r.status != 503) {
			t.Fatalf("concurrent status %d %s %v", r.status, r.b, r.e)
		}
		if r.status == 503 {
			v, e := contract.ReadResponse(r.b)
			if e != nil || v.Response.AgentFunctionResponse.Error == nil {
				t.Fatal("invalid in-flight refusal")
			}
			continue
		}
		o := outcome(t, r.b)
		if o.Status == contract.StatusOK {
			success++
			sf := f.next(c, o)
			if sf.Data.CreateSurface.Metadata.Extensions.Pattern.ID != contract.PatternListDetail {
				t.Fatal("missing result pattern")
			}
		} else if o.Status != contract.StatusConflict {
			t.Fatalf("unexpected competing result %+v", o)
		}
	}
	if success != 1 {
		t.Fatalf("effects %d", success)
	}
	var rev int
	var theme string
	if e := f.db.QueryRow("SELECT theme,revision FROM app_preferences").Scan(&theme, &rev); e != nil || theme != "light" || rev != 2 {
		t.Fatalf("preference effect %s %d %v", theme, rev, e)
	}
	changed := call
	changed.CallAgentFunction.CallFunction.Args.Input = map[string]any{"theme": "dark", "timezone": "UTC"}
	if o := f.submit(c, changed); o.Status != contract.StatusConflict {
		t.Fatal("changed input replay")
	}
	originalSurface := f.want(c, "", "GET", "/api/genui/surfaces/"+call.CallAgentFunction.SurfaceID, nil, 200)
	f.close()
	f = startGenui(t, f.dir, f)
	c = f.client("owner")
	recovered := f.want(c, "", "GET", "/api/genui/actions/"+call.CallAgentFunction.CallFunction.Args.Action.ID+"?functionCallId=reconnected", nil, 200)
	var r gateway.Recovery
	if json.Unmarshal(recovered, &r) != nil || r.Request == nil || r.Request.CallAgentFunction.CallFunction.Args.Action.Confirmation != "" || r.Request.CallAgentFunction.CallFunction.Args.Input["theme"] != "light" || r.Response.AgentFunctionResponse.Value.Status != contract.StatusOK || r.Request.CallAgentFunction.FunctionCallID != "reconnected" {
		t.Fatalf("bad durable recovery %s", recovered)
	}
	if !bytes.Equal(originalSurface, f.want(c, "", "GET", "/api/genui/surfaces/"+call.CallAgentFunction.SurfaceID, nil, 200)) {
		t.Fatal("published envelope mutated")
	}
	if o := f.submit(c, call); o.Status != contract.StatusConflict {
		t.Fatal("restart released claim")
	}
	list := f.want(c, "", "GET", "/api/genui/surfaces", nil, 200)
	if !bytes.Contains(list, []byte(call.CallAgentFunction.CallFunction.Args.Action.ID)) {
		t.Fatal("discovery lost action")
	}
}
func TestGenUIStalePreferenceAndCurrentUserAuthority(t *testing.T) {
	f := startGenui(t, t.TempDir(), nil)
	owner := f.client("owner")
	call := f.ack(owner, f.approval(owner, "light"))
	f.want(owner, "", "PUT", "/api/preferences", map[string]any{"theme": "system", "timezone": "UTC", "revision": 1}, 200)
	if o := f.submit(owner, call); o.Status != contract.StatusStale {
		t.Fatalf("stale approval applied %+v", o)
	}
	scopes := []string{"basalisk.read", "basalisk.write", "basalisk.genui.read", "basalisk.genui.show", "basalisk.genui.act", "basalisk.genui.confirm"}
	b := f.want(owner, "", "POST", "/api/identity/users", map[string]any{"username": "reviewer", "display_name": "Reviewer", "password": "fixture account password", "scopes": scopes}, 201)
	var user identity.PublicUser
	if json.Unmarshal(b, &user) != nil {
		t.Fatal("user")
	}
	c := f.client("reviewer")
	review := f.ack(c, f.approval(c, "light"))
	var token struct {
		Token string `json:"token"`
	}
	b = f.want(c, "", "POST", "/api/token", map[string]any{}, 200)
	if json.Unmarshal(b, &token) != nil || token.Token == "" {
		t.Fatalf("token %s", b)
	}
	f.want(owner, "", "PUT", "/api/identity/users/"+user.ID, map[string]any{"scopes": []string{"basalisk.read", "basalisk.genui.read"}}, 200)
	out := outcome(t, f.want(http.DefaultClient, token.Token, "POST", "/api/genui/actions", review, 200))
	if out.Status != contract.StatusRefused {
		t.Fatalf("stale token retained authority %+v", out)
	}
	f.want(owner, "", "PUT", "/api/identity/users/"+user.ID, map[string]any{"disabled": true}, 200)
	f.want(http.DefaultClient, token.Token, "GET", "/api/genui/surfaces", nil, 403)
}
func TestGenUIRegisteredProducerCannotActOrConfirm(t *testing.T) {
	f := startGenui(t, t.TempDir(), nil)
	b := f.want(http.DefaultClient, f.producer, "POST", "/api/genui/producer/surfaces", map[string]any{"pattern": "form", "params": map[string]any{}}, 200)
	s, e := contract.DecodeEnvelope(b)
	if e != nil {
		t.Fatal(e)
	}
	call := actionFor(t, s, 0)
	call.CallAgentFunction.CallFunction.Args.Input["draft"] = map[string]any{"theme": "light", "timezone": "UTC"}
	f.want(http.DefaultClient, f.producer, "POST", "/api/genui/actions", call, 403)
	f.want(http.DefaultClient, f.producer, "POST", "/api/genui/confirm", call, 403)
	f.want(http.DefaultClient, f.producer, "GET", "/api/genui/surfaces", nil, 403)
	// A broader real service key still cannot impersonate a browser user, and an
	// unregistered service cannot select an owner by supplying request data.
	key, _, e := f.id.CreateKey(t.Context(), identity.KeyInput{Name: "unregistered", Scopes: []string{"*"}})
	if e != nil {
		t.Fatal(e)
	}
	f.want(http.DefaultClient, key, "POST", "/api/genui/producer/surfaces", map[string]any{"pattern": "form", "params": map[string]any{}}, 403)
	f.want(http.DefaultClient, key, "POST", "/api/genui/actions", call, 403)
	f.want(http.DefaultClient, key, "POST", "/api/genui/confirm", call, 403)
}
func TestGenUILostEffectAcknowledgmentRetainsClaimAcrossReopen(t *testing.T) {
	f := startGenui(t, t.TempDir(), nil)
	c := f.client("owner")
	call := f.ack(c, f.approval(c, "light"))
	_, e := f.db.Exec(`CREATE TRIGGER lose_genui_receipt BEFORE UPDATE ON genui_actions WHEN json_extract(NEW.record,'$.Phase') IN ('terminal','unknown') BEGIN SELECT RAISE(FAIL,'isolated receipt fault'); END`)
	if e != nil {
		t.Fatal(e)
	}
	b := f.want(c, "", "POST", "/api/genui/actions", call, 503)
	v, e := contract.ReadResponse(b)
	if e != nil || v.Response.AgentFunctionResponse.Error == nil {
		t.Fatalf("lost acknowledgment success %s %v", b, e)
	}
	var rev int
	if e = f.db.QueryRow("SELECT revision FROM app_preferences").Scan(&rev); e != nil || rev != 2 {
		t.Fatalf("actual effect missing %d %v", rev, e)
	}
	if _, e = f.db.Exec("DROP TRIGGER lose_genui_receipt"); e != nil {
		t.Fatal(e)
	}
	f.close()
	f = startGenui(t, f.dir, f)
	c = f.client("owner")
	b = f.want(c, "", "GET", "/api/genui/actions/"+call.CallAgentFunction.CallFunction.Args.Action.ID+"?functionCallId=after-loss", nil, 200)
	var recovery gateway.Recovery
	if json.Unmarshal(b, &recovery) != nil || recovery.Response.AgentFunctionResponse.Error == nil || recovery.Request == nil {
		t.Fatalf("uncertainty lost %s", b)
	}
	f.want(c, "", "POST", "/api/genui/actions", call, 503)
	if e = f.db.QueryRow("SELECT revision FROM app_preferences").Scan(&rev); e != nil || rev != 2 {
		t.Fatal("replayed uncertain effect")
	}
}

// The actual driver's permitting COMMIT hook can outlive request cancellation.
// We retain uncertainty and the claim even when the changed row really commits.
func TestGenUIDelayedPhysicalCommitIsUnknownAndNeverReplayed(t *testing.T) {
	f := startGenui(t, t.TempDir(), nil)
	c := f.client("owner")
	call := f.ack(c, f.approval(c, "light"))
	conn, e := f.db.Conn(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	var touched, stalled atomic.Bool
	e = conn.Raw(func(raw any) error {
		h, ok := raw.(sqlite.HookRegisterer)
		if !ok {
			return fmt.Errorf("missing real SQLite hook")
		}
		h.RegisterPreUpdateHook(func(d sqlite.SQLitePreUpdateData) {
			if d.TableName == "app_preferences" {
				touched.Store(true)
			}
		})
		h.RegisterCommitHook(func() int32 {
			if touched.Swap(false) && stalled.CompareAndSwap(false, true) {
				time.Sleep(350 * time.Millisecond)
			}
			return 0
		})
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = conn.Close(); e != nil {
		t.Fatal(e)
	}
	f.executionTimeout.Store(int64(150 * time.Millisecond))
	b := f.want(c, "", "POST", "/api/genui/actions", call, 503)
	v, e := contract.ReadResponse(b)
	if e != nil || v.Response.AgentFunctionResponse.Error == nil || !stalled.Load() {
		t.Fatalf("late commit misclassified %s %v", b, e)
	}
	f.executionTimeout.Store(0)
	var rev int
	if e = f.db.QueryRow("SELECT revision FROM app_preferences").Scan(&rev); e != nil || rev != 2 {
		t.Fatalf("probe must prove physically committed late row, got %d %v", rev, e)
	}
	f.close()
	f = startGenui(t, f.dir, f)
	c = f.client("owner")
	f.want(c, "", "POST", "/api/genui/actions", call, 503)
	if e = f.db.QueryRow("SELECT revision FROM app_preferences").Scan(&rev); e != nil || rev != 2 {
		t.Fatal("late commit replayed")
	}
}

func TestGenUIBoundAcknowledgmentAndNativeCancel(t *testing.T) {
	f := startGenui(t, t.TempDir(), nil)
	c := f.client("owner")
	call := f.approval(c, "light")
	if o := f.submit(c, call); o.Status != contract.StatusNeedsConfirmation {
		t.Fatal("save had no acknowledgment boundary")
	}
	changed := call
	changed.CallAgentFunction.CallFunction.Args.Input = map[string]any{"theme": "system", "timezone": "UTC"}
	f.want(c, "", "POST", "/api/genui/confirm", changed, 403)
	// A separate login of the same current user can recover and acknowledge the
	// exact pending review without selecting a choice from localStorage.
	alias := f.client("owner")
	var recovery gateway.Recovery
	b := f.want(alias, "", "GET", "/api/genui/actions/"+call.CallAgentFunction.CallFunction.Args.Action.ID+"?functionCallId=new-session", nil, 200)
	if json.Unmarshal(b, &recovery) != nil || recovery.Request == nil || recovery.Response.AgentFunctionResponse.Value.Status != contract.StatusNeedsConfirmation {
		t.Fatalf("pending recovery %s", b)
	}
	var ack struct {
		Confirmation string `json:"confirmation"`
	}
	b = f.want(alias, "", "POST", "/api/genui/confirm", recovery.Request, 200)
	if json.Unmarshal(b, &ack) != nil || ack.Confirmation == "" {
		t.Fatal("no acknowledgment")
	}
	recoveredCall := *recovery.Request
	recoveredCall.CallAgentFunction.CallFunction.Args.Action.Confirmation = ack.Confirmation
	if o := f.submit(alias, recoveredCall); o.Status != contract.StatusOK {
		t.Fatalf("exact recovered input not accepted %+v", o)
	}
	next := f.approval(c, "dark")
	surface, e := f.host.Get(t.Context(), gateway.Scope{Namespace: hostgenui.Namespace, Actor: f.owner}, next.CallAgentFunction.SurfaceID)
	if e != nil {
		t.Fatal(e)
	}
	cancel := actionFor(t, surface, 1)
	if o := f.submit(c, cancel); o.Status != contract.StatusCanceled {
		t.Fatalf("native cancel %+v", o)
	}
	var revision int
	if e = f.db.QueryRow("SELECT revision FROM app_preferences").Scan(&revision); e != nil || revision != 2 {
		t.Fatal("cancel mutated preferences")
	}
	high := f.approval(c, "system")
	high.CallAgentFunction.CallFunction.Args.Action.Risk = contract.RiskHigh
	if o := f.submit(c, high); o.Status != contract.StatusRefused {
		t.Fatalf("unsupported stronger presence %+v", o)
	}
}
