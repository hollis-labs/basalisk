package assurance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWireRejectsAmbiguousResponsesAndRedirects(t *testing.T) {
	for _, body := range []string{`{"ok":true,"ok":true}`, `{"OK":true}`, `{"ok":true,"extra":true}`, `{"ok":null}`, `{"ok":true} {"ok":true}`} {
		var result struct {
			OK bool `json:"ok"`
		}
		if decodeWire([]byte(body), &result) == nil {
			t.Fatal("ambiguous envelope accepted")
		}
	}
	for _, mode := range []string{"valid", "redirect", "oversized", "cacheable", "nonjson"} {
		t.Run(mode, func(t *testing.T) {
			peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/pinned" {
					t.Error("followed redirect with credential")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				switch mode {
				case "redirect":
					http.Redirect(w, r, "/untrusted", http.StatusFound)
					return
				case "oversized":
					_, _ = w.Write([]byte(strings.Repeat(" ", 2049)))
					return
				case "cacheable":
					w.Header().Set("Cache-Control", "max-age=60")
				case "nonjson":
					w.Header().Set("Content-Type", "text/plain")
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer peer.Close()
			var result struct {
				OK bool `json:"ok"`
			}
			err := postWire(context.Background(), wireClient(peer.Client()), peer.URL+"/pinned", "synthetic", false, struct{}{}, &result, 2048)
			if (err == nil) != (mode == "valid") {
				t.Fatal("wire acceptance mismatch")
			}
		})
	}
}
