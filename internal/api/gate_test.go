package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/basalisk/internal/gatecrasher"
	"github.com/hollis-labs/basalisk/internal/gatecrasher/gatecrashertest"
)

// A secret's value must never pass through Basalisk: no gate route takes one, and decide
// refuses any field it does not know, so the canary reaches neither Gatecrasher, the log
// nor the kept state.
func TestGateRoutesNeverTakeASecret(t *testing.T) {
	const canary = "CANARY-7f3a91-secret-value"
	f := gatecrashertest.New()
	dir := t.TempDir()
	key := filepath.Join(dir, "owner.key")
	_ = os.WriteFile(key, []byte(gatecrashertest.Key), 0o600)
	cfgPath := filepath.Join(dir, "g.json")
	_ = os.WriteFile(cfgPath, []byte(`{"url":"`+f.URL+`"}`), 0o600)
	cfg, err := gatecrasher.LoadConfig(cfgPath, key)
	if err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	state := filepath.Join(dir, "state.json")
	in := gatecrasher.New(cfg, state, slog.New(slog.NewTextHandler(logs, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { in.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		f.CloseClientConnections()
		<-done
		f.Close()
	})

	s, c := newStore(t)
	s.api.EnableGate(in)
	c.handler = s.Handler()
	c.signIn()

	f.Add("apr_s", "request_secret")
	f.Add("apr_a", "approve")
	deadline := time.Now().Add(2 * time.Second)
	var list struct {
		Cards   []gatecrasher.Card `json:"cards"`
		Version int64              `json:"version"`
		Waiting int                `json:"waiting"`
	}
	for time.Now().Before(deadline) {
		_ = json.Unmarshal(c.call("GET", "/api/gate/cards", nil, 200).Body.Bytes(), &list)
		if list.Waiting == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if list.Waiting != 2 {
		t.Fatalf("cards within 2s: %+v", list)
	}

	// Every way a value might be pushed through a gate route is refused.
	c.call("POST", "/api/gate/cards/apr_s/decide", map[string]any{"outcome": "approved", "value": canary}, 400)
	c.call("POST", "/api/gate/cards/apr_s/decide", map[string]any{"outcome": "approved", "answer": canary}, 400)
	c.call("POST", "/api/gate/cards/apr_a/decide", map[string]any{"outcome": "approved", "secret": map[string]any{"value": canary}}, 400)
	c.call("POST", "/api/gate/cards/apr_s/refresh", map[string]any{"value": canary}, 200)
	c.call("POST", "/api/gate/cards/apr_s/decide", map[string]any{"outcome": "denied"}, 200)

	for _, b := range f.Bodies() {
		if strings.Contains(b, canary) {
			t.Fatalf("the canary reached Gatecrasher: %s", b)
		}
	}
	if strings.Contains(logs.String(), canary) {
		t.Fatal("the canary reached the log")
	}
	raw, _ := os.ReadFile(state) //nolint:gosec // a test temp file
	if strings.Contains(string(raw), canary) {
		t.Fatal("the canary reached the kept state")
	}

	// Long poll: a request at the current version waits for the next change.
	_ = json.Unmarshal(c.call("GET", "/api/gate/cards", nil, 200).Body.Bytes(), &list)
	got := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "http://localhost/api/gate/cards?v="+itoa(list.Version), nil)
		s.Handler().ServeHTTP(w, c.sign(r))
		got <- w.Code
	}()
	time.Sleep(50 * time.Millisecond)
	f.Add("apr_b", "approve")
	select {
	case code := <-got:
		if code != 200 {
			t.Fatalf("long poll: %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the long poll did not return on a new card")
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
