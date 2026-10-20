package account

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/basalisk/internal/activity"
	"github.com/hollis-labs/libs/security/pairing"
)

func pairCaptureApp(t *testing.T, c *client, h http.Handler) pairing.Exchange {
	t.Helper()
	g := responseAs[pairing.Grant](t, c.call("POST", "/api/devices/grants", map[string]any{"label": "Nil", "profile": "capture"}, 201))
	return responseAs[pairing.Exchange](t, nativeCall(t, h, "", "POST", pairing.ExchangePath, pairing.ExchangeRequest{Code: g.Code, Audience: "basalisk"}, 200))
}

func TestCaptureAppLeastPrivilegeAndDiscovery(t *testing.T) {
	s, c := newStore(t)
	c.call("POST", "/api/setup", setupBody(), 200)
	h := s.CompanionHandler()
	c.call("POST", "/api/devices/grants", map[string]string{"label": "Nil", "profile": "admin"}, 400)
	d := pairCaptureApp(t, c, h)
	if !slices.Contains(d.Device.Scopes, scopeCapture) || !slices.Contains(d.Device.Scopes, scopeContext) || slices.Contains(d.Device.Scopes, scopeObserve) {
		t.Fatal(d.Device.Scopes)
	}
	nativeCall(t, h, d.Token, "POST", "/companion/observations", map[string]any{}, 403)
	nativeCall(t, h, d.Token, "POST", "/companion/artifacts", map[string]any{}, 403)
	nativeCall(t, h, d.Token, "POST", pairing.RenewPath, map[string]any{"scopes": []string{scopeContext, scopeCapture, scopeObserve}}, 401)
	capabilities := nativeCall(t, h, d.Token, "GET", "/companion/capture-capabilities", nil, 200)
	if !strings.Contains(capabilities.Body.String(), `"nativeHistory":"unavailable"`) || strings.Contains(capabilities.Body.String(), d.Token) {
		t.Fatal(capabilities.Body.String())
	}
	readOnly := pairDevice(t, c, h, []string{scopeContext})
	nativeCall(t, h, readOnly.Token, "GET", "/companion/capture-capabilities", nil, 403)
	nativeCall(t, h, readOnly.Token, "POST", "/companion/capture-context", map[string]string{"captureID": "capture-no-write-001"}, 403)
	r := httptest.NewRequest("GET", "http://localhost/companion/capture-capabilities", nil)
	r.AddCookie(c.cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("cookie authenticated capture API")
	}
	r = httptest.NewRequest("GET", "http://localhost/companion/capture-capabilities", nil)
	r.Header.Set("Authorization", "Bearer "+d.Token)
	r.Header.Set("Origin", "https://foreign.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin request accepted")
	}
	c.call("DELETE", "/api/devices/"+d.Device.ID, nil, 200)
	nativeCall(t, h, d.Token, "GET", "/companion/capture-capabilities", nil, 401)
	nativeCall(t, h, d.Token, "POST", "/companion/capture-context", map[string]string{"captureID": "revoked-capture-001"}, 401)
}

func TestCaptureBindingRestartImmutableAndDelivered(t *testing.T) {
	f := newDeliveryFixture(t, true)
	f.c.call("DELETE", fmt.Sprintf("/api/fragment-submissions/%d", f.status.Sequence), nil, 200)
	f.device = pairCaptureApp(t, f.c, f.h)
	f.c.call("PUT", "/api/fragment-connections/"+f.device.Device.ID, map[string]string{"integrationId": f.in.IntegrationID}, 200)
	id := "nil-bound-capture-0001"
	binding := responseAs[captureContextBinding](t, nativeCall(t, f.h, f.device.Token, "POST", "/companion/capture-context", map[string]string{"captureID": id}, 201))
	if binding.State != "active" || binding.Draft == nil || binding.Draft.DeviceID != f.device.Device.ID || binding.Draft.Project != "Original" || len(binding.Draft.HistoryRefs) != 0 || binding.Draft.PrecedingApp != nil {
		t.Fatal(binding)
	}
	command(f.c, activity.Command{RequestID: "nil-project-switch-001", Action: "context", SessionID: f.session.ID, Revision: f.session.Revision, Project: "Next"}, 200)
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
	replayed := responseAs[captureContextBinding](t, nativeCall(t, f.h, f.device.Token, "POST", "/companion/capture-context", map[string]string{"captureID": id}, 200))
	if !reflect.DeepEqual(binding, replayed) {
		t.Fatal("binding changed across restart/project switch")
	}
	f.in = fragmentSubmission{CaptureID: id, IntegrationID: f.in.IntegrationID, Content: "# Nil draft", DraftAt: binding.DraftAt, SubmittedAt: time.Now().UTC().Format(timestampFormat), ContextBindingID: binding.BindingID, Metadata: nilCaptureMetadata()}
	wrong := f.in
	wrong.DraftAt = time.Now().UTC().Format(timestampFormat)
	nativeCall(t, f.h, f.device.Token, "POST", "/companion/fragments", wrong, 400)
	wrong = f.in
	wrong.CaptureID = "other-nil-capture-0001"
	nativeCall(t, f.h, f.device.Token, "POST", "/companion/fragments", wrong, 400)
	wrong = f.in
	wrong.Draft = binding.Draft
	nativeCall(t, f.h, f.device.Token, "POST", "/companion/fragments", wrong, 400)
	wrong.ContextBindingID = ""
	nativeCall(t, f.h, f.device.Token, "POST", "/companion/fragments", wrong, 400)
	other := pairCaptureApp(t, f.c, f.h)
	f.c.call("PUT", "/api/fragment-connections/"+other.Device.ID, map[string]string{"integrationId": f.in.IntegrationID}, 200)
	nativeCall(t, f.h, other.Token, "POST", "/companion/fragments", f.in, 400)
	bytesIn, _ := json.Marshal(f.in)
	r := httptest.NewRequest("POST", "http://127.0.0.1/companion/fragments", bytes.NewReader(bytesIn))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+f.device.Token)
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("binding crossed origins", w.Code)
	}
	f.status = responseAs[fragmentSubmissionStatus](t, nativeCall(t, f.h, f.device.Token, "POST", "/companion/fragments", f.in, 202))
	nativeCall(t, f.h, f.device.Token, "POST", "/companion/fragments", f.in, 200)
	f.peer.loseAccept = true
	f.run(t)
	f.retry()
	f.run(t)
	if f.state(t).State != "saved" || len(f.peer.accepted) != 1 {
		t.Fatal(f.state(t))
	}
	if !strings.Contains(f.peer.manifests[0], `"project":"Original"`) || strings.Contains(f.peer.manifests[0], `"project":"Next"`) {
		t.Fatal("lost original context")
	}
	page := f.c.call("GET", "/api/activity/sessions/"+f.session.ID+"/artifacts", nil, 200).Body.String()
	if !strings.Contains(page, "server-confirmed") || !strings.Contains(page, f.device.Device.ID) {
		t.Fatal("missing confirmed Nil provenance")
	}
}

func TestCaptureBindingNullContextNarrowingAndNoRetroactiveBinding(t *testing.T) {
	s, c := newStore(t)
	c.call("POST", "/api/setup", setupBody(), 200)
	h := s.CompanionHandler()
	d := pairCaptureApp(t, c, h)
	i := responseAs[Integration](t, c.call("POST", "/api/integrations", map[string]any{"name": "FE", "provider": "fragments-engine", "baseUrl": "https://fe.example.test", "credential": "synthetic"}, 201))
	c.call("PUT", "/api/fragment-connections/"+d.Device.ID, map[string]string{"integrationId": i.ID}, 200)
	id := "nil-without-session-001"
	b := responseAs[captureContextBinding](t, nativeCall(t, h, d.Token, "POST", "/companion/capture-context", map[string]string{"captureID": id}, 201))
	if b.State != "none" || b.Draft != nil {
		t.Fatal(b)
	}
	sess := command(c, activity.Command{RequestID: "nil-session-start-001", Action: "start", Title: "Work"}, 200)
	command(c, activity.Command{RequestID: "nil-session-pause-001", Action: "pause", SessionID: sess.ID, Revision: sess.Revision}, 200)
	p := responseAs[captureContextBinding](t, nativeCall(t, h, d.Token, "POST", "/companion/capture-context", map[string]string{"captureID": "nil-paused-session-001"}, 201))
	if p.State != "paused" || p.Draft != nil {
		t.Fatal(p)
	}
	in := fragmentSubmission{CaptureID: id, IntegrationID: i.ID, Content: "sessionless", DraftAt: b.DraftAt, SubmittedAt: time.Now().UTC().Format(timestampFormat), ContextBindingID: b.BindingID}
	nativeCall(t, h, d.Token, "POST", "/companion/fragments", in, 202)
	var raw string
	s.db.QueryRow("SELECT payload FROM fragment_submissions WHERE capture_id=?", id).Scan(&raw)
	var stored fragmentSubmission
	json.Unmarshal([]byte(raw), &stored)
	if stored.Draft != nil {
		t.Fatal("invented session")
	}
	in.CaptureID = "nil-offline-capture-001"
	in.ContextBindingID = ""
	nativeCall(t, h, d.Token, "POST", "/companion/fragments", in, 202)
	nativeCall(t, h, d.Token, "POST", "/companion/capture-context", map[string]string{"captureID": in.CaptureID}, 409)
	nativeCall(t, h, d.Token, "POST", pairing.RenewPath, map[string]any{"scopes": []string{scopeCapture}}, 200)
	nativeCall(t, h, d.Token, "POST", "/companion/capture-context", map[string]string{"captureID": id}, 403)
	in.CaptureID = p.CaptureID
	in.ContextBindingID = p.BindingID
	in.DraftAt = p.DraftAt
	nativeCall(t, h, d.Token, "POST", "/companion/fragments", in, 403)
	in.ContextBindingID = ""
	nativeCall(t, h, d.Token, "POST", "/companion/fragments", in, 202)
	if _, err := s.db.Exec("UPDATE hollis_pairing_devices SET expires_at=0 WHERE id=?", d.Device.ID); err != nil {
		t.Fatal(err)
	}
	nativeCall(t, h, d.Token, "GET", "/companion/capture-capabilities", nil, 401)
}
