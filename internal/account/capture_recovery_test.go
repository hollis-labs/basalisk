package account

import (
	"context"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/security/pairing"
)

// Use the independent capture profile so recovery cannot accidentally depend
// on the companion's observation permission or the owner's cookie.
func captureRecoveryFixture(t *testing.T) *deliveryFixture {
	t.Helper()
	f := newDeliveryFixture(t, false)
	f.c.call("DELETE", fmt.Sprintf("/api/fragment-submissions/%d", f.status.Sequence), nil, 200)
	f.device = pairCaptureApp(t, f.c, f.h)
	f.c.call("PUT", "/api/fragment-connections/"+f.device.Device.ID, map[string]string{"integrationId": f.in.IntegrationID}, 200)
	f.status = responseAs[fragmentSubmissionStatus](t, nativeCall(t, f.h, f.device.Token, "POST", "/companion/fragments", f.in, 202))
	return f
}

func TestCaptureRecoveryIsolationAndPagination(t *testing.T) {
	f := captureRecoveryFixture(t)
	other := pairCaptureApp(t, f.c, f.h)
	f.c.call("PUT", "/api/fragment-connections/"+other.Device.ID, map[string]string{"integrationId": f.in.IntegrationID}, 200)
	foreign := f.in
	foreign.Content = "other installation's private draft"
	// Same client capture ID in a different device namespace is still isolated.
	otherStatus := responseAs[fragmentSubmissionStatus](t, nativeCall(t, f.h, other.Token, "POST", "/companion/fragments", foreign, 202))
	for n := 0; n < 51; n++ {
		in := f.in
		in.CaptureID = fmt.Sprintf("nil-recovery-page-%04d", n)
		nativeCall(t, f.h, f.device.Token, "POST", "/companion/fragments", in, 202)
	}
	type page struct {
		Items      []fragmentSubmissionStatus `json:"items"`
		NextBefore int64                      `json:"nextBefore"`
	}
	path := "/companion/fragments"
	seen := map[string]bool{}
	for {
		result := nativeCall(t, f.h, f.device.Token, "GET", path, nil, 200)
		if strings.Contains(result.Body.String(), f.in.Content) || strings.Contains(result.Body.String(), "legacy-owner") || strings.Contains(result.Body.String(), f.device.Token) {
			t.Fatal("listing disclosed payload or credential")
		}
		p := responseAs[page](t, result)
		for _, item := range p.Items {
			if item.DeviceID != f.device.Device.ID || seen[item.CaptureID] {
				t.Fatal("pagination mixed devices or repeated an operation")
			}
			seen[item.CaptureID] = true
		}
		if p.NextBefore == 0 {
			break
		}
		path = fmt.Sprintf("/companion/fragments?before=%d", p.NextBefore)
	}
	if len(seen) != 52 {
		t.Fatal("pagination lost accepted operations", len(seen))
	}
	nativeCall(t, f.h, f.device.Token, "GET", "/companion/fragments?before=bad", nil, 400)
	missing := "/companion/fragments/nil-recovery-page-0000"
	for _, action := range []string{"/retry", "/stop"} {
		nativeCall(t, f.h, other.Token, "POST", missing+action, nil, 404)
		nativeCall(t, f.h, f.device.Token, "POST", missing+action, map[string]string{"content": "replacement"}, 400)
	}
	base := "/companion/fragments/" + f.in.CaptureID
	stopped := responseAs[fragmentSubmissionStatus](t, nativeCall(t, f.h, f.device.Token, "POST", base+"/stop", nil, 200))
	if stopped.State != "discarded" || stopped.MayHaveSent || stopped.Code != "capture_discarded" {
		t.Fatal(stopped)
	}
	unchanged := responseAs[fragmentSubmissionStatus](t, nativeCall(t, f.h, other.Token, "GET", base, nil, 200))
	if !reflect.DeepEqual(unchanged, otherStatus) {
		t.Fatal("stopped another device's capture")
	}
	// Retrying stop (including after a lost response) preserves the tombstone.
	again := responseAs[fragmentSubmissionStatus](t, nativeCall(t, f.h, f.device.Token, "POST", base+"/stop", nil, 200))
	if !reflect.DeepEqual(stopped, again) {
		t.Fatal("stop replay changed the tombstone")
	}
	nativeCall(t, f.h, f.device.Token, "POST", base+"/retry", nil, 409)
	replayed := responseAs[fragmentSubmissionStatus](t, nativeCall(t, f.h, f.device.Token, "POST", "/companion/fragments", f.in, 200))
	if replayed.State != "discarded" {
		t.Fatal("submission replay resurrected stopped capture")
	}
}

func TestCaptureRecoveryAuthorization(t *testing.T) {
	for _, mode := range []string{"revoked", "expired", "narrowed", "cookie", "cross-origin"} {
		t.Run(mode, func(t *testing.T) {
			f := captureRecoveryFixture(t)
			want := 401
			switch mode {
			case "revoked":
				f.c.call("DELETE", "/api/devices/"+f.device.Device.ID, nil, 200)
			case "expired":
				if _, err := f.s.db.Exec("UPDATE hollis_pairing_devices SET expires_at=0 WHERE id=?", f.device.Device.ID); err != nil {
					t.Fatal(err)
				}
			case "narrowed":
				nativeCall(t, f.h, f.device.Token, "POST", pairing.RenewPath, map[string]any{"scopes": []string{scopeContext}}, 200)
				want = 403
			case "cross-origin":
				want = 403
			}
			for _, route := range []struct{ method, path string }{
				{"GET", "/companion/fragments"},
				{"GET", "/companion/fragments/" + f.in.CaptureID},
				{"POST", "/companion/fragments/" + f.in.CaptureID + "/retry"},
				{"POST", "/companion/fragments/" + f.in.CaptureID + "/stop"},
			} {
				r := httptest.NewRequest(route.method, "http://localhost"+route.path, nil)
				if mode == "cookie" {
					r.AddCookie(f.c.cookie)
				} else {
					r.Header.Set("Authorization", "Bearer "+f.device.Token)
				}
				if mode == "cross-origin" {
					r.Header.Set("Origin", "https://foreign.example")
				}
				w := httptest.NewRecorder()
				f.h.ServeHTTP(w, r)
				if w.Code != want {
					t.Fatalf("%s: got %d want %d", route.path, w.Code, want)
				}
			}
			var state string
			if err := f.s.db.QueryRow("SELECT state FROM fragment_submissions WHERE sequence=?", f.status.Sequence).Scan(&state); err != nil || state != "queued" {
				t.Fatal("unauthorized recovery changed state", state, err)
			}
		})
	}
}

func TestCaptureRecoveryLostResponsesAndRestart(t *testing.T) {
	f := captureRecoveryFixture(t)
	f.peer.loseAccept, f.peer.loseComplete = true, true
	f.run(t)
	if st := f.state(t); !st.MayHaveSent || st.State != "queued" {
		t.Fatal(st)
	}
	f.s.Close()
	var err error
	f.s, err = Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	f.h, f.c.handler = f.s.CompanionHandler(), f.s.Handler()
	f.worker, err = f.s.NewFragmentWorker(f.config, f.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		nativeCall(t, f.h, f.device.Token, "POST", "/companion/fragments/"+f.in.CaptureID+"/retry", nil, 200)
		f.run(t)
	}
	if st := f.state(t); st.State != "saved" || st.FragmentID == "" {
		t.Fatal(st)
	}
	if len(f.peer.accepted) != 1 || len(f.peer.manifests) != 2 || f.peer.manifests[0] != f.peer.manifests[1] || len(f.peer.completes) != 2 || f.peer.completes[0] != f.peer.completes[1] {
		t.Fatal("recovery changed immutable upstream identity")
	}
	for _, action := range []string{"/retry", "/stop"} {
		nativeCall(t, f.h, f.device.Token, "POST", "/companion/fragments/"+f.in.CaptureID+action, nil, 409)
	}
}

func TestCaptureRecoveryLeaseAndAmbiguousStop(t *testing.T) {
	f := captureRecoveryFixture(t)
	job, err := f.worker.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	base := "/companion/fragments/" + f.in.CaptureID
	for _, action := range []string{"/retry", "/stop"} {
		nativeCall(t, f.h, f.device.Token, "POST", base+action, nil, 409)
	}
	if _, err = f.s.db.Exec("UPDATE fragment_submissions SET lease_until=? WHERE sequence=?", time.Now().Unix()-1, f.status.Sequence); err != nil {
		t.Fatal(err)
	}
	nativeCall(t, f.h, f.device.Token, "POST", base+"/retry", nil, 200)
	if err = f.worker.update(context.Background(), job, "code='stale'"); err == nil {
		t.Fatal("expired worker overwrote retry")
	}
	f.peer.loseAccept = true
	f.run(t)
	stopped := responseAs[fragmentSubmissionStatus](t, nativeCall(t, f.h, f.device.Token, "POST", base+"/stop", nil, 200))
	if stopped.State != "discarded" || !stopped.MayHaveSent || stopped.Code != "capture_stopped_delivery" {
		t.Fatal(stopped)
	}
	f.run(t)
	if len(f.peer.manifests) != 1 || len(f.peer.completes) != 0 {
		t.Fatal("stopped operation was delivered again")
	}
	var payload, manifest, receipt, hash, upstream string
	if err = f.s.db.QueryRow("SELECT payload,manifest,receipt,payload_hash,upstream_capture_id FROM fragment_submissions WHERE sequence=?", f.status.Sequence).Scan(&payload, &manifest, &receipt, &hash, &upstream); err != nil {
		t.Fatal(err)
	}
	if payload != "" || manifest != "" || receipt != "" || hash == "" || upstream == "" {
		t.Fatal("stop did not preserve only the tombstone identity")
	}
}

func TestCaptureRecoveryCannotRestoreRemovedConnection(t *testing.T) {
	f := captureRecoveryFixture(t)
	f.c.call("DELETE", "/api/fragment-connections/"+f.device.Device.ID, nil, 200)
	f.run(t)
	if st := f.state(t); st.State != "needs_attention" {
		t.Fatal(st)
	}
	base := "/companion/fragments/" + f.in.CaptureID
	// Retry requests another authorized attempt, not a new connection grant.
	nativeCall(t, f.h, f.device.Token, "POST", base+"/retry", nil, 200)
	f.run(t)
	if st := f.state(t); st.State != "needs_attention" {
		t.Fatal(st)
	}
	if len(f.peer.manifests) != 0 {
		t.Fatal("retry bypassed connection authorization")
	}
	// Removal of a connection doesn't prevent abandoning its pending operation.
	nativeCall(t, f.h, f.device.Token, "POST", base+"/stop", nil, 200)
}

func TestOwnerCaptureRetryInvalidatesExpiredLease(t *testing.T) {
	f := newDeliveryFixture(t, false)
	job, err := f.worker.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.db.Exec("UPDATE fragment_submissions SET lease_until=0 WHERE sequence=?", f.status.Sequence); err != nil {
		t.Fatal(err)
	}
	f.retry()
	if err = f.worker.update(context.Background(), job, "code='stale'"); err == nil {
		t.Fatal("expired worker overwrote owner retry")
	}
	f.run(t)
	if st := f.state(t); st.State != "saved" {
		t.Fatal(st)
	}
}
