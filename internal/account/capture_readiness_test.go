package account

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/security/pairing"
)

func TestCaptureReadinessScopedEvidenceAndNoProbes(t *testing.T) {
	f := captureRecoveryFixture(t)
	other := pairCaptureApp(t, f.c, f.h)
	f.c.call("PUT", "/api/fragment-connections/"+other.Device.ID, map[string]string{"integrationId": f.in.IntegrationID}, 200)
	foreign := f.in
	foreign.Content = "another app's private text"
	nativeCall(t, f.h, other.Token, "POST", "/companion/fragments", foreign, 202)
	read := func() captureReadinessSnapshot {
		t.Helper()
		r := nativeCall(t, f.h, f.device.Token, "GET", "/companion/capture-readiness", nil, 200)
		for _, secret := range []string{f.in.Content, foreign.Content, other.Device.ID, f.device.Token, "legacy-owner-must-never-be-used", "file://delivery/test-parent", f.server.URL} {
			if strings.Contains(r.Body.String(), secret) {
				t.Fatal("readiness leaked another device, content or credential/config")
			}
		}
		return responseAs[captureReadinessSnapshot](t, r)
	}
	snapshot := read()
	if len(snapshot.Apps) != 1 || snapshot.Apps[0].DeviceID != f.device.Device.ID || snapshot.Apps[0].Queue.Queued != 1 || snapshot.Apps[0].Worker.State != "configured" || snapshot.Apps[0].Connection.State != "bound" || snapshot.Apps[0].LastConfirmed != nil {
		t.Fatal(snapshot)
	}
	if snapshot.UpstreamHealth != "not_probed" || snapshot.PortfolioHealth != "not_connected" {
		t.Fatal("local configuration implied remote health")
	}
	if len(f.peer.manifests) != 0 {
		t.Fatal("status read triggered delivery")
	}
	f.run(t)
	snapshot = read()
	if snapshot.Apps[0].Queue.Saved != 1 || snapshot.Apps[0].LastConfirmed == nil || snapshot.Apps[0].LatestPending != nil || snapshot.UpstreamHealth != "not_probed" {
		t.Fatal("historical delivery was mistaken for health", snapshot)
	}
	f.c.call("DELETE", "/api/fragment-connections/"+f.device.Device.ID, nil, 200)
	snapshot = read()
	if snapshot.Apps[0].Connection.State != "missing" || snapshot.Apps[0].Worker.State != "not_assigned" || snapshot.Apps[0].LastConfirmed == nil {
		t.Fatal("lost historical receipt or ignored removed connection")
	}
	// Reads do not resolve a private secret; an unavailable resolver is irrelevant.
	resolver := f.s.secrets
	f.s.secrets = nil
	read()
	f.s.secrets = resolver
}

func TestCaptureReadinessAuthorizationAndOwnerView(t *testing.T) {
	f := captureRecoveryFixture(t)
	other := pairCaptureApp(t, f.c, f.h)
	f.c.call("DELETE", "/api/devices/"+other.Device.ID, nil, 200)
	viewer := pairDevice(t, f.c, f.h, []string{scopeContext})
	nativeCall(t, f.h, viewer.Token, "GET", "/companion/capture-readiness", nil, 403)
	nativeCall(t, f.h, other.Token, "GET", "/companion/capture-readiness", nil, 401)
	nativeCall(t, f.s.Handler(), f.device.Token, "GET", "/api/capture-readiness", nil, 401)
	for _, cross := range []bool{false, true} {
		r := httptest.NewRequest("GET", "http://localhost/companion/capture-readiness", nil)
		r.AddCookie(f.c.cookie)
		want := 401
		if cross {
			r.Header.Set("Authorization", "Bearer "+f.device.Token)
			r.Header.Set("Origin", "https://foreign.example")
			want = 403
		}
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatal(w.Code, want)
		}
	}
	owner := responseAs[captureReadinessSnapshot](t, f.c.call("GET", "/api/capture-readiness", nil, 200))
	found := false
	for _, app := range owner.Apps {
		if app.DeviceID == other.Device.ID {
			found = true
			if app.Authorization != "revoked" {
				t.Fatal(app)
			}
			if app.ContextAllowed {
				t.Fatal("revoked app appeared permitted to read context")
			}
		}
	}
	if !found {
		t.Fatal("owner cannot see revoked installation")
	}
	nativeCall(t, f.h, f.device.Token, "POST", pairing.RenewPath, map[string]any{"scopes": []string{scopeContext}}, 200)
	nativeCall(t, f.h, f.device.Token, "GET", "/companion/capture-readiness", nil, 403)
}

func TestCaptureReadinessWorkerFreshnessAndRestart(t *testing.T) {
	f := captureRecoveryFixture(t)
	f.s.setDeliveryActivity(f.in.IntegrationID, "running")
	if st := f.s.deliveryActivityAt(time.Now().Add(61 * time.Second)); st.State != "stale" {
		t.Fatal(st)
	}
	// Expired evidence retains its timestamp; it cannot be refreshed by an API read.
	old := f.s.deliveryActivityAt(time.Now().Add(61 * time.Second)).ObservedAt
	if f.s.deliveryActivityAt(time.Now().Add(62*time.Second)).ObservedAt != old {
		t.Fatal("read refreshed worker evidence")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.worker.Run(ctx)
	if st := f.s.deliveryActivityAt(time.Now()); st.State != "stopped" {
		t.Fatal(st)
	}
	f.run(t)
	f.s.Close()
	var err error
	f.s, err = Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	f.h = f.s.CompanionHandler()
	r := responseAs[captureReadinessSnapshot](t, nativeCall(t, f.h, f.device.Token, "GET", "/companion/capture-readiness", nil, 200))
	if r.Apps[0].Worker.State != "not_configured" || r.Apps[0].LastConfirmed == nil {
		t.Fatal("restart retained runtime state or lost receipt", r)
	}
}
