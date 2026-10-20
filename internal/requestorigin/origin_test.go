package requestorigin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxyBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, host, peer, proto   string
		enabled, accepted, secure bool
	}{
		{"local", "localhost:8080", "127.0.0.1:1234", "", false, true, false},
		{"ipv6", "[::1]:8080", "[::1]:1234", "", false, true, false},
		{"spoof local scheme", "localhost:8080", "127.0.0.1:1234", "https", true, true, false},
		{"proxy", CanonicalHost, "127.0.0.1:1234", "https", true, true, true},
		{"proxy ipv6", CanonicalHost, "[::1]:1234", "https", true, true, true},
		{"disabled", CanonicalHost, "127.0.0.1:1234", "https", false, false, false},
		{"missing proto", CanonicalHost, "127.0.0.1:1234", "", true, false, false},
		{"plaintext", CanonicalHost, "127.0.0.1:1234", "http", true, false, false},
		{"ambiguous proto", CanonicalHost, "127.0.0.1:1234", "https,http", true, false, false},
		{"remote", CanonicalHost, "100.64.0.1:1234", "https", true, false, false},
		{"invalid peer", CanonicalHost, "localhost:1234", "https", true, false, false},
		{"alias", "basalisk.os.nanite.cloud", "127.0.0.1:1234", "https", true, false, false},
		{"foreign", "evil.example", "127.0.0.1:1234", "https", true, false, false},
		{"alternate port", CanonicalHost + ":443", "127.0.0.1:1234", "https", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://"+tc.host+"/", nil)
			r.RemoteAddr = tc.peer
			if tc.proto != "" {
				r.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			r.Header.Set("X-Forwarded-Host", CanonicalHost)
			r.Header.Set("Forwarded", "host="+CanonicalHost+";proto=https")
			called := false
			h := Handler(tc.enabled, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if Secure(r) != tc.secure {
					t.Fatal("wrong secure context")
				}
				if tc.secure && Origin(r) != CanonicalOrigin {
					t.Fatal("wrong canonical origin")
				}
				if r.Header.Get("X-Forwarded-Proto") != "" || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-Host") != "" {
					t.Fatal("forwarded input leaked downstream")
				}
				w.WriteHeader(204)
			}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if called != tc.accepted || (!tc.accepted && w.Code != 403) {
				t.Fatalf("accepted=%v status=%d", called, w.Code)
			}
		})
	}
}
