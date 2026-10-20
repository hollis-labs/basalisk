package account

import (
	"fmt"
	"github.com/hollis-labs/libs/security/pairing"
	"net/http"
	"strings"
	"testing"
	"time"
)

func captureDevice(t *testing.T, c *client, h http.Handler) pairing.Exchange {
	t.Helper()
	g := responseAs[pairing.Grant](t, c.call("POST", "/api/devices/grants", map[string]any{"label": "Capture Mac", "allowFragments": true}, 201))
	return responseAs[pairing.Exchange](t, nativeCall(t, h, "", "POST", pairing.ExchangePath, pairing.ExchangeRequest{Code: g.Code, Audience: "basalisk"}, 200))
}
func TestSubmissionDurableReplayAndOwnerDiscard(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := &client{t: t, handler: s.Handler()}
	c.call("POST", "/api/setup", setupBody(), 200)
	h := s.CompanionHandler()
	d := captureDevice(t, c, h)
	i := responseAs[Integration](t, c.call("POST", "/api/integrations", map[string]any{"name": "FE", "provider": "fragments-engine", "baseUrl": "https://fe.example.test", "credential": "synthetic-unused-key"}, 201))
	c.call("PUT", "/api/fragment-connections/"+d.Device.ID, map[string]string{"integrationId": i.ID}, 200)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	in := fragmentSubmission{CaptureID: "test-capture-000001", IntegrationID: i.ID, Content: "private draft survives restart", Tags: []string{}, DraftAt: now, SubmittedAt: now}
	status := responseAs[fragmentSubmissionStatus](t, nativeCall(t, h, d.Token, "POST", "/companion/fragments", in, 202))
	if status.State != "queued" || status.Code != "delivery_unavailable" {
		t.Fatal("unconfirmed capture reported success", status)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h = s.CompanionHandler()
	c.handler = s.Handler()
	replay := responseAs[fragmentSubmissionStatus](t, nativeCall(t, h, d.Token, "POST", "/companion/fragments", in, 200))
	if replay.Sequence != status.Sequence {
		t.Fatal("duplicate operation")
	}
	changed := in
	changed.Content = "changed"
	nativeCall(t, h, d.Token, "POST", "/companion/fragments", changed, 409)
	other := captureDevice(t, c, h)
	nativeCall(t, h, other.Token, "GET", "/companion/fragments/"+in.CaptureID, nil, 404)
	old := pairDevice(t, c, h, nil)
	nativeCall(t, h, old.Token, "POST", "/companion/fragments", in, 403)
	nativeCall(t, s.Handler(), d.Token, "GET", "/api/fragment-submissions", nil, 401)
	listing := c.call("GET", "/api/fragment-submissions", nil, 200).Body.String()
	if strings.Contains(listing, in.Content) || strings.Contains(listing, "synthetic-unused-key") {
		t.Fatal("list leaked private content")
	}
	detail := fmt.Sprintf("/api/fragment-submissions/%d", status.Sequence)
	c.call("DELETE", "/api/integrations/"+i.ID, nil, 200)
	if !strings.Contains(c.call("GET", detail, nil, 200).Body.String(), in.Content) {
		t.Fatal("integration deletion lost note")
	}
	nativeCall(t, h, d.Token, "POST", "/companion/fragments", in, 200)
	fresh := in
	fresh.CaptureID = "test-capture-000002"
	nativeCall(t, h, d.Token, "POST", "/companion/fragments", fresh, 409)
	c.call("DELETE", detail, nil, 200)
	replay = responseAs[fragmentSubmissionStatus](t, nativeCall(t, h, d.Token, "POST", "/companion/fragments", in, 200))
	if replay.State != "discarded" {
		t.Fatal("discarded note resurrected")
	}
	if strings.Contains(c.call("GET", detail, nil, 200).Body.String(), in.Content) {
		t.Fatal("discard left note readable")
	}
	c.call("DELETE", "/api/devices/"+d.Device.ID, nil, 200)
	nativeCall(t, h, d.Token, "GET", "/companion/fragments/"+in.CaptureID, nil, 401)
	var n int
	s.db.QueryRow("SELECT count(*) FROM companion_artifacts").Scan(&n)
	if n != 0 {
		t.Fatal("unconfirmed capture created provenance")
	}
}
func TestSubmissionBoundsAndFrozenContext(t *testing.T) {
	s, c := newStore(t)
	c.call("POST", "/api/setup", setupBody(), 200)
	h := s.CompanionHandler()
	d := captureDevice(t, c, h)
	i := responseAs[Integration](t, c.call("POST", "/api/integrations", map[string]any{"name": "FE", "provider": "fragments-engine", "baseUrl": "https://fe.example.test", "credential": "synthetic"}, 201))
	c.call("PUT", "/api/fragment-connections/"+d.Device.ID, map[string]string{"integrationId": i.ID}, 200)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	in := fragmentSubmission{CaptureID: "bounded-capture-0001", IntegrationID: i.ID, Content: strings.Repeat("x", 64*1024), Tags: []string{}, DraftAt: now, SubmittedAt: now}
	nativeCall(t, h, d.Token, "POST", "/companion/fragments", in, 202)
	in.CaptureID = "bounded-capture-0002"
	in.Content += "x"
	nativeCall(t, h, d.Token, "POST", "/companion/fragments", in, 400)
	in.Content = "note"
	in.Draft = &artifactContext{SchemaVersion: 1, DeviceID: d.Device.ID, SessionID: "invented"}
	nativeCall(t, h, d.Token, "POST", "/companion/fragments", in, 400)
	in.Draft = nil
	// Fill using distinct valid operations to verify the safety boundary without
	// weakening the queue limit for test code. Exact replay still works at capacity.
	for n := 2; n <= 100; n++ {
		in.CaptureID = fmt.Sprintf("bounded-capture-%04d", n)
		nativeCall(t, h, d.Token, "POST", "/companion/fragments", in, 202)
	}
	nativeCall(t, h, d.Token, "POST", "/companion/fragments", in, 200)
	in.CaptureID = "bounded-capture-0101"
	nativeCall(t, h, d.Token, "POST", "/companion/fragments", in, 409)
}
