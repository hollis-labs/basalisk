package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/basalisk/internal/account"
	"github.com/hollis-labs/basalisk/internal/webui"
)

func TestCompanionRoutesDoNotFallThroughToSPA(t *testing.T) {
	store, err := account.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ui, err := webui.New(store.Handler())
	if err != nil {
		t.Fatal(err)
	}
	h := applicationHandler(ui.Handler(), store.CompanionHandler(), nil)
	for _, path := range []string{"/auth/pair/exchange", "/auth/renew", "/companion/observations"} {
		r := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Hollis-Pairing-Protocol", "1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("native path %s reached wrong handler: %d %s", path, w.Code, w.Header().Get("Content-Type"))
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/assurance/v1/status", strings.NewReader(`{}`)))
	if w.Code != 503 || w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("inactive status endpoint reached SPA or returned success")
	}
}
