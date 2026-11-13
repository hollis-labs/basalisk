package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/basalisk/internal/fecapture"
)

const fragID = "00064016b0ca7d9c6613d8f2dfd3fca66149856604d107808548e1e13e6e5954"

// readerStub answers FE's reader for one fragment, to one bearer.
func readerStub(t *testing.T, bearer string, issuer bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if issuer && r.URL.Path == "/auth/token" {
			var req struct{ Scopes []string }
			_ = json.NewDecoder(r.Body).Decode(&req)
			if r.Header.Get("Authorization") != "Bearer test-enrollment-parent" || len(req.Scopes) != 1 || req.Scopes[0] != fecapture.ReadScope {
				http.Error(w, "wrong grant", http.StatusForbidden)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"token": bearer, "aud": "fragments-engine", "scopes": []string{fecapture.ReadScope}, "kind": "service", "expires_at": time.Now().Add(15 * time.Minute)})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+bearer {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		if r.URL.Path != "/v1/reader/items/"+fragID {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"schema_version":"fe.reader.item.v1","fragment_id":"` + fragID + `","fragment_revision_id":"rev1","revision":3,
			"display":{"title":{"value":"Brackets idea"},"summary":{"value":"Corner brackets in the theme"}},"article":{"preview_markdown":"# Brackets\n\nbody"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFragmentCitePersonal(t *testing.T) {
	key := "personal-owner-token-0123456789abcdef"
	srv := readerStub(t, key, false)
	s, c := newStore(t)
	c.signIn()
	w, err := s.NewFragmentWorker(DeliveryConfig{}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fragmentCite(context.Background(), nil, fragID, false); err == nil {
		t.Fatal("resolved without an integration")
	}
	c.call("POST", "/api/integrations", map[string]any{"name": "FE", "provider": "fragments-engine", "baseUrl": srv.URL, "credential": key}, 201)
	e, err := fragmentCite(context.Background(), nil, fragID, true)
	if err != nil {
		t.Fatal(err)
	}
	if e.Title != "Brackets idea" || e.Description != "Corner brackets in the theme" || !strings.Contains(e.Body, "# Brackets") {
		t.Fatalf("%+v", e)
	}
	if _, err = fragmentCite(context.Background(), nil, strings.Repeat("a", 64), false); !errors.Is(err, errFragmentMissing) || !notFound(err) {
		t.Fatalf("missing: %v", err)
	}
	_ = w
}

func TestFragmentCiteExchange(t *testing.T) {
	claims, _ := json.Marshal(map[string]any{"sub": "svc:basalisk-test", "aud": "fragments-engine", "scope": fecapture.ReadScope, "kind": "service", "exp": time.Now().Add(15 * time.Minute).Unix()})
	jwt := "test." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
	srv := readerStub(t, jwt, true)
	f := newDeliveryFixture(t, false)
	f.s.db.Exec(`UPDATE integrations SET base_url=?`, srv.URL)
	f.config.IssuerURL = srv.URL
	if _, err := f.s.NewFragmentWorker(f.config, srv.Client()); err != nil {
		t.Fatal(err)
	}
	e, err := fragmentCite(context.Background(), nil, fragID, false)
	if err != nil || e.Title != "Brackets idea" || e.Body != "" {
		t.Fatalf("%+v %v", e, err)
	}
}
