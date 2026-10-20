// Package requestorigin validates the application's local/proxy boundary and
// carries the trusted external scheme to cookie and CSRF handling.
package requestorigin

import (
	"context"
	"net"
	"net/http"
	"strings"
)

const CanonicalHost = "basalisk.nanite.cloud"
const CanonicalOrigin = "https://" + CanonicalHost

type secureProxyKey struct{}

func Secure(r *http.Request) bool {
	trusted, _ := r.Context().Value(secureProxyKey{}).(bool)
	return r.TLS != nil || trusted
}

func Origin(r *http.Request) string {
	scheme := "http"
	if Secure(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// Handler enables only the fixed canonical host when explicitly configured.
// The backend remains bound to loopback. The local TLS proxy must preserve
// Host/Origin and overwrite X-Forwarded-Proto. Other forwarded values never
// choose the host, origin, caller identity, or WebAuthn trust configuration.
func Handler(httpsProxy bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trusted := false
		if r.Host == CanonicalHost {
			peer, _, err := net.SplitHostPort(r.RemoteAddr)
			ip := net.ParseIP(peer)
			values := r.Header.Values("X-Forwarded-Proto")
			if !httpsProxy || err != nil || ip == nil || !ip.IsLoopback() || len(values) != 1 || values[0] != "https" {
				http.Error(w, "Untrusted HTTPS proxy", http.StatusForbidden)
				return
			}
			trusted = true
		} else if !localHost(r.Host) {
			http.Error(w, "Invalid host", http.StatusForbidden)
			return
		}
		r = r.Clone(context.WithValue(r.Context(), secureProxyKey{}, trusted))
		for _, name := range []string{"Forwarded", "X-Forwarded-Proto", "X-Forwarded-Host", "X-Forwarded-For", "X-Real-IP"} {
			r.Header.Del(name)
		}
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func localHost(host string) bool {
	name := host
	if strings.Contains(name, ":") {
		var err error
		name, _, err = net.SplitHostPort(name)
		if err != nil {
			return false
		}
	}
	ip := net.ParseIP(name)
	return name == "localhost" || (ip != nil && ip.IsLoopback())
}
