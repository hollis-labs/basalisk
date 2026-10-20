package account

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/basalisk/internal/activity"
	"github.com/hollis-labs/basalisk/internal/fecapture"
	"github.com/hollis-labs/libs/security/pairing"
)

type capturePeer struct {
	forward                                  http.Handler
	mu                                       sync.Mutex
	manifests                                []string
	completes                                []string
	accepted                                 map[string]string
	loseAccept, loseComplete, invalidReceipt bool
	failure                                  int
	jwt                                      string
	hook                                     func()
}

func (p *capturePeer) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.URL.Path == "/auth/token" {
		if r.Header.Get("Authorization") != "Bearer test-enrollment-parent" {
			http.Error(w, "wrong credential", 403)
			return
		}
		var req struct {
			Audience string   `json:"audience"`
			Scopes   []string `json:"scopes"`
			TTL      int      `json:"ttl_seconds"`
			Session  string   `json:"session_id"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Audience != "fragments-engine" || len(req.Scopes) != 1 || req.Scopes[0] != fecapture.Scope || req.TTL != 900 || req.Session != "" {
			http.Error(w, "wrong grant", 403)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"token": p.jwt, "aud": "fragments-engine", "scopes": []string{fecapture.Scope}, "kind": "service", "expires_at": time.Now().Add(15 * time.Minute)})
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+p.jwt {
		http.Error(w, "wrong credential", 403)
		return
	}
	if p.forward != nil {
		p.forward.ServeHTTP(w, r)
		return
	}
	if p.failure != 0 {
		http.Error(w, "private upstream detail must not escape", p.failure)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	id := ""
	schema := "fe.capture.status.v1"
	completion := "complete"
	if r.URL.Path == "/v1/captures" {
		p.manifests = append(p.manifests, string(raw))
		var m map[string]any
		json.Unmarshal(raw, &m)
		id, _ = m["capture_id"].(string)
		if old, ok := p.accepted[id]; ok && old != string(raw) {
			http.Error(w, "conflict", 409)
			return
		}
		p.accepted[id] = string(raw)
		if p.hook != nil {
			p.hook()
			p.hook = nil
		}
		if p.loseAccept {
			p.loseAccept = false
			http.Error(w, "lost accepted response", 500)
			return
		}
		schema = "fe.capture.result.v1"
		completion = "accepting"
	} else if strings.HasSuffix(r.URL.Path, "/complete") {
		p.completes = append(p.completes, string(raw))
		id = strings.Split(r.URL.Path, "/")[3]
		if p.loseComplete {
			p.loseComplete = false
			http.Error(w, "lost completion response", 500)
			return
		}
	} else {
		http.NotFound(w, r)
		return
	}
	if p.invalidReceipt {
		id = "foreign-capture"
	}
	json.NewEncoder(w).Encode(map[string]any{"schema_version": schema, "capture_id": id, "fragment_id": "confirmed-fragment", "fragment_revision_id": "confirmed-revision", "capture_attempt_id": "confirmed-attempt", "completion": completion, "asset_instructions": []any{}, "assets": []any{}})
}

type deliveryFixture struct {
	s       *Store
	c       *client
	h       http.Handler
	device  pairing.Exchange
	in      fragmentSubmission
	status  fragmentSubmissionStatus
	worker  *FragmentWorker
	config  DeliveryConfig
	server  *httptest.Server
	peer    *capturePeer
	dir     string
	session activity.Session
}

func newDeliveryFixture(t *testing.T, withContext bool, metadata ...*captureMetadata) *deliveryFixture {
	t.Helper()
	f := &deliveryFixture{dir: t.TempDir(), peer: &capturePeer{accepted: map[string]string{}}}
	claims, _ := json.Marshal(map[string]any{"sub": "svc:basalisk-test", "aud": "fragments-engine", "scope": fecapture.Scope, "kind": "service", "exp": time.Now().Add(15 * time.Minute).Unix()})
	f.peer.jwt = "test." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
	f.server = httptest.NewTLSServer(http.HandlerFunc(f.peer.serve))
	t.Cleanup(f.server.Close)
	var err error
	f.s, err = Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.s.Close() })
	f.c = &client{t: t, handler: f.s.Handler()}
	f.c.call("POST", "/api/setup", setupBody(), 200)
	f.h = f.s.CompanionHandler()
	f.device = captureDevice(t, f.c, f.h)
	integration := responseAs[Integration](t, f.c.call("POST", "/api/integrations", map[string]any{"name": "FE", "provider": "fragments-engine", "baseUrl": f.server.URL, "credential": "legacy-owner-must-never-be-used"}, 201))
	f.c.call("PUT", "/api/fragment-connections/"+f.device.Device.ID, map[string]string{"integrationId": integration.ID}, 200)
	var draft *artifactContext
	if withContext {
		f.session = command(f.c, activity.Command{RequestID: "worker-session-start-01", Action: "start", Title: "Original draft", Project: "Original"}, 200)
	}
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	if withContext {
		draft = &artifactContext{SchemaVersion: 1, Origin: "http://127.0.0.1:18080", DeviceID: f.device.Device.ID, SessionID: f.session.ID, ContextID: f.session.Context.ID, Project: "Original", ContextAsOf: stamp, PublishedAt: stamp, ExpiresAt: now.Add(20 * time.Second).Format(time.RFC3339Nano), HistoryRefs: []string{}}
	}
	f.in = fragmentSubmission{CaptureID: "worker-capture-00001", IntegrationID: integration.ID, Content: "a private note", Tags: []string{"project-test"}, DraftAt: stamp, SubmittedAt: stamp, Draft: draft}
	if len(metadata) > 0 {
		f.in.Metadata = metadata[0]
		f.in.Content = "## Review\n\n- [ ] Follow up with [team](urn:example:team)\n\n`@literal` stays Markdown."
	}
	f.status = responseAs[fragmentSubmissionStatus](t, nativeCall(t, f.h, f.device.Token, "POST", "/companion/fragments", f.in, 202))
	if err = f.s.secrets.Set(context.Background(), "file://delivery/test-parent", strings.NewReader("test-enrollment-parent")); err != nil {
		t.Fatal(err)
	}
	f.config = DeliveryConfig{IntegrationID: integration.ID, PublicOrigin: "http://127.0.0.1:18080", Config: fecapture.Config{IssuerURL: f.server.URL, Subject: "svc:basalisk-test", ParentRef: "file://delivery/test-parent"}}
	f.worker, err = f.s.NewFragmentWorker(f.config, f.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *deliveryFixture) run(t *testing.T) {
	t.Helper()
	if err := f.worker.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func (f *deliveryFixture) retry() {
	f.c.call("POST", fmt.Sprintf("/api/fragment-submissions/%d/retry", f.status.Sequence), nil, 200)
}
func (f *deliveryFixture) state(t *testing.T) fragmentSubmissionStatus {
	return responseAs[fragmentSubmissionStatus](t, nativeCall(t, f.h, f.device.Token, "GET", "/companion/fragments/"+f.in.CaptureID, nil, 200))
}
func TestDeliveryLostResponsesRestartAndProvenance(t *testing.T) {
	f := newDeliveryFixture(t, true)
	f.peer.loseAccept = true
	f.peer.loseComplete = true
	command(f.c, activity.Command{RequestID: "worker-session-change", Action: "context", SessionID: f.session.ID, Revision: f.session.Revision, Project: "Next"}, 200)
	f.run(t)
	if st := f.state(t); st.State != "queued" || !st.MayHaveSent || st.FragmentID != "" {
		t.Fatal(st)
	}
	f.s.Close()
	var err error
	f.s, err = Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	f.c.handler = f.s.Handler()
	f.h = f.s.CompanionHandler()
	f.worker, err = f.s.NewFragmentWorker(f.config, f.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	f.retry()
	f.run(t)
	if st := f.state(t); st.State != "queued" || st.FragmentID != "" {
		t.Fatal(st)
	}
	var associations int
	f.s.db.QueryRow("SELECT count(*) FROM companion_artifacts").Scan(&associations)
	if associations != 0 {
		t.Fatal("unconfirmed association")
	}
	f.retry()
	f.run(t)
	st := f.state(t)
	if st.State != "saved" || st.FragmentID != "confirmed-fragment" || st.FragmentRevisionID != "confirmed-revision" || st.FragmentURL != f.server.URL+"/sysop/reader/confirmed-fragment" {
		t.Fatal(st)
	}
	if len(f.peer.accepted) != 1 || len(f.peer.manifests) != 2 || f.peer.manifests[0] != f.peer.manifests[1] || len(f.peer.completes) != 2 || f.peer.completes[0] != f.peer.completes[1] {
		t.Fatal("retry identity changed")
	}
	if strings.Contains(f.peer.manifests[0], "test-enrollment-parent") || strings.Contains(f.peer.manifests[0], "legacy-owner") || !strings.Contains(f.peer.manifests[0], `"project":"Original"`) {
		t.Fatal("bad immutable metadata")
	}
	page := f.c.call("GET", "/api/activity/sessions/"+f.session.ID+"/artifacts", nil, 200).Body.String()
	if !strings.Contains(page, "server-confirmed") || !strings.Contains(page, "Original") || !strings.Contains(page, "confirmed-revision") {
		t.Fatal(page)
	}
	f.run(t)
	f.s.db.QueryRow("SELECT count(*) FROM companion_artifacts").Scan(&associations)
	if associations != 1 {
		t.Fatal("duplicate association")
	}
	f.c.call("POST", fmt.Sprintf("/api/fragment-submissions/%d/retry", st.Sequence), nil, 409)
}
func TestDeliveryWithoutSessionAndLeaseExclusion(t *testing.T) {
	f := newDeliveryFixture(t, false)
	j, err := f.worker.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.run(t)
	if len(f.peer.manifests) != 0 {
		t.Fatal("second worker ignored lease")
	}
	f.c.call("DELETE", fmt.Sprintf("/api/fragment-submissions/%d", f.status.Sequence), nil, 409)
	if err = f.worker.deliver(context.Background(), &j); err != nil {
		t.Fatal(err)
	}
	if f.state(t).State != "saved" {
		t.Fatal("sessionless note not saved")
	}
	var n int
	f.s.db.QueryRow("SELECT count(*) FROM companion_artifacts").Scan(&n)
	if n != 0 {
		t.Fatal("fabricated session context")
	}
}
func TestDeliveryAuthorizationAndReceipts(t *testing.T) {
	for _, name := range []string{"revoke-before", "revoke-after-accept", "remove-binding", "invalid-receipt", "forbidden", "conflict", "missing-parent", "subject-change"} {
		t.Run(name, func(t *testing.T) {
			f := newDeliveryFixture(t, false)
			switch name {
			case "revoke-before":
				f.c.call("DELETE", "/api/devices/"+f.device.Device.ID, nil, 200)
			case "revoke-after-accept":
				f.peer.hook = func() { f.c.call("DELETE", "/api/devices/"+f.device.Device.ID, nil, 200) }
			case "remove-binding":
				f.c.call("DELETE", "/api/fragment-connections/"+f.device.Device.ID, nil, 200)
			case "invalid-receipt":
				f.peer.invalidReceipt = true
			case "forbidden":
				f.peer.failure = 403
			case "conflict":
				f.peer.failure = 409
			case "missing-parent":
				f.s.secrets.Delete(context.Background(), f.config.ParentRef)
			case "subject-change":
				f.peer.loseAccept = true
				f.run(t)
				f.retry()
				f.worker.config.Subject = "svc:other"
			}
			f.run(t)
			var state, code, fragment, payload string
			f.s.db.QueryRow("SELECT state,code,fragment_id,payload FROM fragment_submissions WHERE sequence=?", f.status.Sequence).Scan(&state, &code, &fragment, &payload)
			if state != "needs_attention" || fragment != "" || !strings.Contains(payload, f.in.Content) || strings.Contains(code, "private") {
				t.Fatal(state, code, fragment)
			}
			if len(f.peer.completes) != 0 {
				t.Fatal("unauthorized or unconfirmed completion")
			}
		})
	}
}
func TestDeliveryExpiredLeaseAndDiscardAfterAmbiguousSend(t *testing.T) {
	f := newDeliveryFixture(t, false)
	old, err := f.worker.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.db.Exec("UPDATE fragment_submissions SET lease_until=0 WHERE sequence=?", old.sequence); err != nil {
		t.Fatal(err)
	}
	f.peer.loseAccept = true
	f.run(t)
	if err = f.worker.update(context.Background(), old, "code='stale'"); err == nil {
		t.Fatal("stale worker overwrote result")
	}
	f.c.call("DELETE", fmt.Sprintf("/api/fragment-submissions/%d", f.status.Sequence), nil, 200)
	st := f.state(t)
	if st.State != "discarded" || st.Code != "owner_stopped_delivery" || !st.MayHaveSent {
		t.Fatal(st)
	}
	f.run(t)
	if len(f.peer.manifests) != 1 {
		t.Fatal("discarded submission resent")
	}
	var payload, manifest string
	f.s.db.QueryRow("SELECT payload,manifest FROM fragment_submissions WHERE sequence=?", st.Sequence).Scan(&payload, &manifest)
	if payload != "" || manifest != "" {
		t.Fatal("discard left readable content")
	}
}

func TestDeliveryShutdownRetainsInterruptedNote(t *testing.T) {
	f := newDeliveryFixture(t, false)
	entered := make(chan struct{})
	released := make(chan struct{})
	f.peer.forward = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
		case <-released:
		}
	})
	defer close(released)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); f.worker.Run(ctx) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("worker did not send")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not shut down")
	}
	st := f.state(t)
	if st.State != "queued" || st.Delivering || st.FragmentID != "" || !st.MayHaveSent {
		t.Fatal(st)
	}
}
