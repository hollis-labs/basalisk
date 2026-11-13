package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fragIn(id, content string, tags ...string) map[string]any {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return map[string]any{"captureId": id, "content": content, "tags": tags, "draftAt": now, "submittedAt": now,
		"context": map[string]any{"window": map[string]string{"app": "notes", "title": "plan.md"}}}
}

func ownerRow(t *testing.T, f *deliveryFixture, seq int64) (state, code, integration, fragment string) {
	t.Helper()
	if err := f.s.db.QueryRow(`SELECT state,code,integration_id,fragment_id FROM fragment_submissions WHERE sequence=?`, seq).Scan(&state, &code, &integration, &fragment); err != nil {
		t.Fatal(err)
	}
	return
}

// Personal mode: no delivery configuration. The owner's captures go out with the FE
// integration's own key; a paired device's notes do not.
func TestFragPersonalMode(t *testing.T) {
	f := newDeliveryFixture(t, false) // a device note is queued for the integration
	personal, err := f.s.NewFragmentWorker(DeliveryConfig{}, f.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	// The fixture's integration holds a key FE refuses; personal mode needs its own.
	f.s.db.Exec(`DELETE FROM integrations`)
	f.peer.jwt = "personal-owner-token-0123456789abcdef"

	q := responseAs[fragmentSubmissionStatus](t, f.c.call("POST", "/api/frag/captures", fragIn("frag-capture-000001", "  a thought  ", "idea", "later"), 202))
	if q.State != "queued" || q.DeviceID != "owner:"+ownerPrincipal.ID {
		t.Fatal(q)
	}
	// No integration yet: nothing is claimed, the note waits.
	if err = personal.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st, _, _, _ := ownerRow(t, f, q.Sequence); st != "queued" || len(f.peer.manifests) != 0 {
		t.Fatal("delivered without an integration")
	}
	var state struct {
		Mode        string
		Integration *struct{ ID, Name string }
	}
	_ = json.Unmarshal(f.c.call("GET", "/api/frag", nil, 200).Body.Bytes(), &state)
	if state.Mode != "personal" || state.Integration != nil {
		t.Fatalf("state = %+v", state)
	}

	integration := responseAs[Integration](t, f.c.call("POST", "/api/integrations", map[string]any{"name": "My FE", "provider": "fragments-engine", "baseUrl": f.server.URL, "credential": f.peer.jwt}, 201))
	_ = json.Unmarshal(f.c.call("GET", "/api/frag", nil, 200).Body.Bytes(), &state)
	if state.Integration == nil || state.Integration.ID != integration.ID {
		t.Fatalf("state = %+v", state)
	}
	if err = personal.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, code, bound, fragment := ownerRow(t, f, q.Sequence)
	if st != "saved" || code != "confirmed" || bound != integration.ID || fragment != "confirmed-fragment" {
		t.Fatal(st, code, bound, fragment)
	}
	if len(f.peer.manifests) != 1 {
		t.Fatalf("manifests = %d", len(f.peer.manifests))
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(f.peer.manifests[0]), &m)
	doc := m["document"].(map[string]any)
	body := doc["content"].(map[string]any)["body"]
	prov := doc["extensions"].(map[string]any)["provider.basalisk"].(map[string]any)
	if m["principal_id"] != personalPrincipal || body != "  a thought  " || fmt.Sprint(m["tags"]) != "[idea later]" || prov["device_id"] != "owner" {
		t.Fatalf("manifest: principal=%v body=%q tags=%v device=%v", m["principal_id"], body, m["tags"], prov["device_id"])
	}
	if strings.Contains(f.peer.manifests[0], "plan.md") || strings.Contains(f.peer.manifests[0], f.peer.jwt) {
		t.Fatal("context or credential reached FE")
	}

	// The device's note was never touched in personal mode.
	if err = personal.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if dev := f.state(t); dev.State != "queued" || dev.MayHaveSent || len(f.peer.manifests) != 1 {
		t.Fatalf("device note delivered in personal mode: %+v", dev)
	}

	// The same capture again is the same row; different text under its id is refused.
	again := responseAs[fragmentSubmissionStatus](t, f.c.call("POST", "/api/frag/captures", fragIn("frag-capture-000001", "  a thought  ", "idea", "later"), 200))
	if again.Sequence != q.Sequence || again.State != "saved" {
		t.Fatal(again)
	}
	f.c.call("POST", "/api/frag/captures", fragIn("frag-capture-000001", "something else"), http.StatusConflict)

	// The owner's list carries the context; the outbox can retry and discard as usual.
	var list struct {
		Items []struct {
			fragmentSubmissionStatus
			Content string
			Context json.RawMessage
		}
	}
	_ = json.Unmarshal(f.c.call("GET", "/api/frag/captures", nil, 200).Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0].Content != "  a thought  " || !strings.Contains(string(list.Items[0].Context), "plan.md") || list.Items[0].FragmentURL == "" {
		t.Fatalf("list = %+v", list.Items)
	}
}

func TestFragPersonalRefusedKeyNeedsAttention(t *testing.T) {
	f := newDeliveryFixture(t, false)
	personal, _ := f.s.NewFragmentWorker(DeliveryConfig{}, f.server.Client())
	f.s.db.Exec(`DELETE FROM integrations`)
	f.c.call("POST", "/api/integrations", map[string]any{"name": "FE", "provider": "fragments-engine", "baseUrl": f.server.URL, "credential": "a-key-fe-does-not-know-0123456789"}, 201) //nolint:gosec // a test key a stub FE refuses
	q := responseAs[fragmentSubmissionStatus](t, f.c.call("POST", "/api/frag/captures", fragIn("frag-capture-000002", "note"), 202))
	if err := personal.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st, code, _, _ := ownerRow(t, f, q.Sequence); st != "needs_attention" || code != "authorization_required" {
		t.Fatal(st, code)
	}
	// Retry from the outbox puts it back in the queue.
	f.c.call("POST", fmt.Sprintf("/api/fragment-submissions/%d/retry", q.Sequence), nil, 200)
	if st, _, _, _ := ownerRow(t, f, q.Sequence); st != "queued" {
		t.Fatal(st)
	}
}

// With delivery configured, owner captures take the issuer exchange like device notes.
func TestFragExchangeMode(t *testing.T) {
	f := newDeliveryFixture(t, false)
	q := responseAs[fragmentSubmissionStatus](t, f.c.call("POST", "/api/frag/captures", fragIn("frag-capture-000003", "exchanged"), 202))
	for range 3 {
		if err := f.worker.Once(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if st, _, bound, _ := ownerRow(t, f, q.Sequence); st != "saved" || bound != f.config.IntegrationID {
		t.Fatal(st, bound)
	}
	owner := ""
	for _, m := range f.peer.manifests {
		if strings.Contains(m, "exchanged") {
			owner = m
		}
	}
	if !strings.Contains(owner, `"principal_id":"svc:basalisk-test"`) || strings.Contains(owner, "legacy-owner") {
		t.Fatal(owner)
	}
	var state struct{ Mode string }
	_ = json.Unmarshal(f.c.call("GET", "/api/frag", nil, 200).Body.Bytes(), &state)
	if state.Mode != "exchange" {
		t.Fatal(state)
	}
}

func TestFragCaptureValidation(t *testing.T) {
	s, _ := newStore(t)
	c := &client{t: t, handler: s.Handler()}
	c.signIn()
	c.call("POST", "/api/frag/captures", fragIn("short", "x"), 400)
	c.call("POST", "/api/frag/captures", fragIn("frag-capture-000004", "   "), 400)
	big := fragIn("frag-capture-000005", "x")
	big["context"] = strings.Repeat("a", 20000)
	c.call("POST", "/api/frag/captures", big, 400)
	c.signOut()
	r := httptest.NewRequest("POST", "http://localhost/api/frag/captures", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}
