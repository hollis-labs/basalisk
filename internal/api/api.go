package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/apierr"
	"github.com/hollis-labs/substrate/modules/httphost"
)

// API serves the owner routes and the companion routes over one Service.
type API struct {
	svc   *Service
	users UserDirectory
	// mu serializes integration credential replacement and removal, so a concurrent delete
	// cannot leave a newly written key orphaned after its metadata is removed, and guards
	// the device pairing attempt window.
	mu           sync.Mutex
	pairAttempts int
	pairWindow   time.Time
}

// API returns the HTTP layer. users supplies the sign-in identity (username, display name,
// email) that GET /api/account composes with the stored preferences.
func (s *Service) API(users UserDirectory) *API { return &API{svc: s, users: users} }

// Scopes the owner routes require. The owner holds "*" and so passes both.
const (
	ScopeRead  = "basalisk.read"
	ScopeWrite = "basalisk.write"
)

// route is one owner-facing HTTP route. The same table registers the handler on the mux and
// the scope on the guard's table, so a route cannot exist without a scope.
type route struct {
	Method, Pattern, Scope string
	Handler                http.HandlerFunc
}

func (a *API) routes() []route {
	r, w := ScopeRead, ScopeWrite
	return []route{
		{"GET", "/api/account", r, a.account},
		{"PUT", "/api/preferences", w, a.preferences},
		{"GET", "/api/integrations", r, a.integrations},
		{"POST", "/api/integrations", w, a.createIntegration},
		{"PUT", "/api/integrations/{id}/credential", w, a.rotateIntegration},
		{"DELETE", "/api/integrations/{id}", w, a.deleteIntegration},
		{"GET", "/api/activity", r, a.activityState},
		{"GET", "/api/activity/sessions", r, a.activitySessions},
		{"GET", "/api/activity/sessions/{id}/events", r, a.activityEvents},
		{"GET", "/api/activity/sessions/{id}/artifacts", r, a.artifacts},
		{"POST", "/api/activity/commands", w, a.activityCommand},
		{"GET", "/api/devices", r, a.devices},
		{"POST", "/api/devices/grants", w, a.deviceGrant},
		{"DELETE", "/api/devices/grants/{id}", w, a.revokeGrant},
		{"DELETE", "/api/devices/{id}", w, a.revokeDevice},
		{"GET", "/api/fragment-connections", r, a.fragmentConnections},
		{"PUT", "/api/fragment-connections/{id}", w, a.setFragmentConnection},
		{"DELETE", "/api/fragment-connections/{id}", w, a.removeFragmentConnection},
		{"GET", "/api/fragment-submissions", r, a.fragmentSubmissions},
		{"GET", "/api/fragment-submissions/{id}", r, a.fragmentSubmissionDetail},
		{"DELETE", "/api/fragment-submissions/{id}", w, a.discardFragmentSubmission},
		{"POST", "/api/fragment-submissions/{id}/retry", w, a.retryFragmentSubmission},
		{"GET", "/api/capture-readiness", r, a.ownerCaptureReadiness},
		{"GET", "/api/observations", r, a.observations},
	}
}

// Handler serves the owner routes; it is httphost.Options.Routes. The auth guard in front of
// it has already verified the caller and the route's scope.
func (a *API) Handler() *http.ServeMux {
	m := http.NewServeMux()
	for _, rt := range a.routes() {
		h := a.authorized(rt.Handler)
		m.HandleFunc(rt.Method+" "+rt.Pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			h(w, r)
		})
	}
	return m
}

// RegisterScopes declares the scope every owner route requires. A route not listed is
// refused by the guard in enforce mode, so adding a handler without a scope fails closed.
//
//	basalisk.read   every GET
//	basalisk.write  every change
func (a *API) RegisterScopes(t *libauth.ScopeTable) {
	for _, rt := range a.routes() {
		t.Route(rt.Method, rt.Pattern, rt.Scope)
	}
}

// Mounts are the device-facing routes at the fixed paths the companion app speaks. They are
// open to the guard because their handlers authenticate device tokens themselves and have
// deliberately no cookie or session fallback.
func (a *API) Mounts() []httphost.Mount {
	h := a.CompanionHandler()
	return []httphost.Mount{
		{Pattern: "/auth/", Handler: h, Open: true},
		{Pattern: "/companion/", Handler: h, Open: true},
	}
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// fail writes the framework's flat {code, message} error body.
func fail(w http.ResponseWriter, status int, message string) {
	apierr.Write(w, status, errorCode(status), message, nil)
}

func errorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request"
	case http.StatusUnauthorized:
		return "unauthenticated"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusRequestEntityTooLarge:
		return "too_large"
	case http.StatusUnsupportedMediaType:
		return "unsupported_media_type"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusServiceUnavailable:
		return "unavailable"
	}
	if status >= 500 {
		return "internal"
	}
	return "error"
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeLimit(w, r, v, 16384)
}
func decodeLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		fail(w, 415, "Expected JSON.")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		fail(w, 400, "Invalid request.")
		return false
	}
	return true
}
func token() string              { return rand.Text() }
func digest(value string) string { h := sha256.Sum256([]byte(value)); return hex.EncodeToString(h[:]) }

// authorized is the handler-side half of the guard: it refuses a request that carries no
// verified caller, so a route wired outside the guard (a test, a misconfigured host) still
// fails closed. Scopes are the guard's job.
func (a *API) authorized(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := caller(r); !ok {
			fail(w, 401, "Please sign in.")
			return
		}
		next(w, r)
	}
}

// caller is the verified principal the guard stamped on the request.
func caller(r *http.Request) (libauth.Principal, bool) {
	p, ok := libauth.FromContext(r.Context())
	return p, ok && p.Verified && p.ID != ""
}

// ownerID is the verified caller's subject: the owner of the devices, activity sessions and
// capture bindings the caller creates. It is never read from a request body.
func (a *API) ownerID(r *http.Request) (string, error) {
	p, ok := caller(r)
	if !ok {
		return "", errNoCaller
	}
	return p.ID, nil
}

// Integration URLs are metadata only. Provider adapters must explicitly own
// outbound requests and credential use; saving a record never calls that URL.
func validBaseURL(raw string) bool {
	if raw == "" {
		return true
	}
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && len(raw) <= 1000
}
