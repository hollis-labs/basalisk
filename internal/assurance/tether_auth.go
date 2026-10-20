package assurance

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"time"

	auth "github.com/hollis-labs/libs/security/auth"
)

const StatusTokenAudience = "basalisk-status"
const StatusTokenScope = "basalisk-status.assurance_read" // #nosec G101 -- public authorization scope, not a credential.

// CurrentWorkloadTrust must check current issuer/token revocation AND the
// principal/parent lifecycle and scopes. A successful cached result or the
// issuer's deny-list alone is insufficient: Tether's current device revocation
// updates its principal store separately from the issuer deny-list.
// Implementations must make an authenticated current read and fail on outage.
type CurrentWorkloadTrust interface {
	CheckCurrentWorkload(context.Context, auth.Principal) error
}

type WorkloadPin struct {
	Subject   string   `json:"subject"`
	Kind      string   `json:"kind"`
	Audiences []string `json:"audiences"`
}

type TetherStatusConfig struct {
	Issuer, JWKSURL string
	Callers         []WorkloadPin
	Client          *http.Client
	Current         CurrentWorkloadTrust
	Now             func() time.Time
}

// TetherStatusAuth verifies the real Tether JWT wire format using the shared
// verifier. No bootstrap key, operator credential, browser cookie, wildcard
// scope, delegated agent token or cached-good trust fallback is supported.
type TetherStatusAuth struct {
	issuer, jwks string
	pins         map[string]WorkloadPin
	client       *http.Client
	current      CurrentWorkloadTrust
	now          func() time.Time
}

func NewTetherStatusAuth(cfg TetherStatusConfig) (*TetherStatusAuth, error) {
	u, err := url.Parse(cfg.JWKSURL)
	if !validID(cfg.Issuer) || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/.well-known/jwks.json" || len(cfg.Callers) == 0 || cfg.Current == nil {
		return nil, ErrInvalid
	}
	pins := make(map[string]WorkloadPin, len(cfg.Callers))
	for _, p := range cfg.Callers {
		if !validID(p.Subject) || (p.Kind != "device" && p.Kind != "service") || len(p.Audiences) == 0 {
			return nil, ErrInvalid
		}
		if _, exists := pins[p.Subject]; exists {
			return nil, ErrInvalid
		}
		for _, audience := range p.Audiences {
			if !validID(audience) || strings.Contains(audience, "*") {
				return nil, ErrInvalid
			}
		}
		p.Audiences = append([]string(nil), p.Audiences...)
		pins[p.Subject] = p
	}
	client := http.Client{Timeout: 5 * time.Second}
	if cfg.Client != nil {
		client = *cfg.Client
	}
	client.Jar = nil
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = freshJWKS{transport}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Timeout <= 0 || client.Timeout > 5*time.Second {
		client.Timeout = 5 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &TetherStatusAuth{cfg.Issuer, cfg.JWKSURL, pins, &client, cfg.Current, cfg.Now}, nil
}

type freshJWKS struct{ transport http.RoundTripper }

func (f freshJWKS) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	// Tether advertises a cacheable public JWKS. Require revalidation at any
	// intermediary on this stronger current-status boundary.
	r.Header.Set("Cache-Control", "no-cache, no-store")
	r.Header.Set("Pragma", "no-cache")
	return f.transport.RoundTrip(r)
}

type workloadClaims struct {
	Issuer   string `json:"iss"`
	Subject  string `json:"sub"`
	Audience string `json:"aud"`
	Scope    string `json:"scope"`
	Expiry   int64  `json:"exp"`
	Issued   int64  `json:"iat"`
	ID       string `json:"jti"`
	Kind     string `json:"kind"`
	Session  string `json:"sid,omitempty"`
	Parent   string `json:"par,omitempty"`
	Before   int64  `json:"nbf,omitempty"`
}

func (a *TetherStatusAuth) AuthenticateStatus(ctx context.Context, r *http.Request) (StatusCaller, error) {
	if a == nil || len(r.Header.Values("Authorization")) != 1 {
		return StatusCaller{}, ErrDenied
	}
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") || len(header) > 8192 {
		return StatusCaller{}, ErrDenied
	}
	token := strings.TrimPrefix(header, "Bearer ")
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return StatusCaller{}, ErrDenied
	}
	var h struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
		Key       string `json:"kid"`
	}
	var c workloadClaims
	hraw, he := base64.RawURLEncoding.DecodeString(parts[0])
	craw, ce := base64.RawURLEncoding.DecodeString(parts[1])
	if he != nil || ce != nil || exact(hraw, 1024, &h) != nil || exact(craw, 6144, &c) != nil || h.Algorithm != "EdDSA" || h.Type != "JWT" || !validID(h.Key) {
		return StatusCaller{}, ErrDenied
	}
	pin, ok := a.pins[c.Subject]
	// Restrict to direct workload tokens. The parent is the same pinned
	// principal, and cannot be a delegated operator or human session.
	if !ok || c.Issuer != a.issuer || c.Audience != StatusTokenAudience || c.Scope != StatusTokenScope || c.Kind != pin.Kind || c.Parent != c.Subject || c.Session != "" || !validID(c.ID) || !c.validAt(a.now()) {
		return StatusCaller{}, ErrDenied
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// A new verifier per request prevents a failed refresh, concurrent caller,
	// or removed signing key from falling back to an older successful snapshot.
	v, err := auth.NewJWTVerifier(auth.JWTConfig{Issuer: a.issuer, Audience: StatusTokenAudience, JWKSURL: a.jwks, Client: a.client, Now: a.now})
	if err != nil || v.Refresh(ctx) != nil {
		return StatusCaller{}, ErrUnavailable
	}
	p, err := v.Verify(ctx, token)
	if err != nil || !p.Verified {
		return StatusCaller{}, ErrDenied
	}
	if err := a.current.CheckCurrentWorkload(ctx, p); err != nil {
		return StatusCaller{}, ErrUnavailable
	}
	// The shared verifier has clock leeway; this boundary deliberately has
	// none, including time spent awaiting authoritative current trust.
	if ctx.Err() != nil || !c.validAt(a.now()) {
		return StatusCaller{}, ErrDenied
	}
	return StatusCaller{Subject: p.ID, Audiences: append([]string(nil), pin.Audiences...)}, nil
}

func (c workloadClaims) validAt(now time.Time) bool {
	return c.Issued > 0 && c.Expiry > c.Issued && c.Expiry-c.Issued <= 900 && c.Issued <= now.Unix() && now.Unix() < c.Expiry && c.Before >= 0 && c.Before <= now.Unix()
}
