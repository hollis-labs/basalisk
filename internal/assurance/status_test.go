package assurance

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type testStatusAuth struct {
	calls     int
	fail      bool
	audiences []string
}

func (a *testStatusAuth) AuthenticateStatus(_ context.Context, r *http.Request) (StatusCaller, error) {
	a.calls++
	if a.fail || r.Header.Get("Authorization") != "test-workload" {
		return StatusCaller{}, ErrDenied
	}
	return StatusCaller{"tachyon-test", a.audiences}, nil
}

type testRequestAuth struct{}

func (testRequestAuth) AuthenticateRequest(_ context.Context, r *http.Request) error {
	r.Header.Set("Authorization", "test-workload")
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testStatusClient(t *testing.T, h http.Handler) *StatusClient {
	t.Helper()
	c, e := NewStatusClient("https://identity.example.test", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Result(), nil
	})}, testRequestAuth{})
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestStatusIndependentVerificationAndLiveRefusal(t *testing.T) {
	f := newFixture(t)
	completed, _ := f.complete("attempt-a")
	authn := &testStatusAuth{audiences: []string{"tether"}}
	client := testStatusClient(t, f.service.StatusHandler(authn))
	verifier := CompletionVerifier{Authority: f.service.cfg.Authority, RPID: f.service.cfg.RPID, Origins: f.service.cfg.Origins, Lookup: client, Now: func() time.Time { return f.now }}
	request, _ := json.Marshal(map[string]any{"kind": "approval", "class": "high", "op_digest": f.items.item.Digest, "operation": f.items.item.Operation, "expires_at": f.items.item.ExpiresAt})
	lookup := CompletionLookup{f.service.cfg.Authority, "tether", completed.ID, "item-a", 1, request}
	for range 2 {
		got, e := verifier.VerifyCompletion(context.Background(), lookup)
		if e != nil {
			t.Fatal(e)
		}
		want, _ := json.Marshal(completed.Evidence)
		if !bytes.Equal(got, want) {
			t.Fatal("changed evidence")
		}
	}
	if authn.calls != 2 {
		t.Fatal("current authenticated read not performed for both decision and consume")
	}
	authn.fail = true
	if _, e := verifier.VerifyCompletion(context.Background(), lookup); e == nil {
		t.Fatal("cached-good accepted during auth outage")
	}
	authn.fail = false
	authn.audiences = []string{"different-app"}
	if _, e := verifier.VerifyCompletion(context.Background(), lookup); e == nil {
		t.Fatal("cross-audience read")
	}
	authn.audiences = []string{"tether"}
	if e := f.service.RevokeKey(context.Background(), "human-a", f.key); e != nil {
		t.Fatal(e)
	}
	if _, e := verifier.VerifyCompletion(context.Background(), lookup); e == nil {
		t.Fatal("revoked credential accepted at consume")
	}
}
func TestStatusReferencesCookiesAndManagementHaveNoAuthority(t *testing.T) {
	f := newFixture(t)
	f.complete("attempt-a")
	authn := &testStatusAuth{audiences: []string{"tether"}}
	for _, tc := range []struct {
		method, path, auth string
		want               int
	}{{"POST", "/assurance/v1/status", "", 401}, {"POST", "/assurance/v1/enroll", "test-workload", 404}, {"DELETE", "/assurance/v1/status", "test-workload", 404}, {"POST", "/assurance/v1/status", "tachyon-enrollment", 401}} {
		r := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(`{"completion_id":"attempt-a","query_id":"query-a"}`))
		r.AddCookie(&http.Cookie{Name: "basalisk_session", Value: "session-a", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		r.Header.Set("Authorization", tc.auth)
		w := httptest.NewRecorder()
		f.service.StatusHandler(authn).ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
	}
}
func TestStatusClientRejectsCachedResponseAndRedirect(t *testing.T) {
	f := newFixture(t)
	f.complete("attempt-a")
	handler := f.service.StatusHandler(&testStatusAuth{audiences: []string{"tether"}})
	var retained []byte
	c := testStatusClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if retained == nil {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, r)
			retained = rec.Body.Bytes()
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(retained)
	}))
	if _, e := c.LookupStatus(context.Background(), "attempt-a"); e != nil {
		t.Fatal(e)
	}
	if _, e := c.LookupStatus(context.Background(), "attempt-a"); e == nil {
		t.Fatal("replayed status response accepted")
	}
	c = testStatusClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://elsewhere.example.test")
		w.WriteHeader(302)
		_, _ = io.WriteString(w, "redirect")
	}))
	if _, e := c.LookupStatus(context.Background(), "attempt-a"); e == nil {
		t.Fatal("redirect accepted")
	}
}
