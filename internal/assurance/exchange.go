package assurance

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/libs/security/secrets/tokenfile"
)

const TachyonAudience = "tachyon-assurance"
const ItemReadScope = TachyonAudience + ".items_read"
const CompletionScope = TachyonAudience + ".completions_deliver"
const OutcomeReadScope = TachyonAudience + ".outcomes_read"

type ExchangeConfig struct {
	Origin          string `json:"origin"`
	Issuer          string `json:"issuer"`
	ParentTokenFile string `json:"parent_token_file"`
	Subject         string `json:"subject"`
	Kind            string `json:"kind"`
}
type scopedJWT struct {
	token  string
	expiry time.Time
	parent [32]byte
}
type WorkloadExchange struct {
	cfg    ExchangeConfig
	client *http.Client
	now    func() time.Time
	gate   chan struct{}
	cached map[string]scopedJWT
}

func NewWorkloadExchange(cfg ExchangeConfig, client *http.Client) (*WorkloadExchange, error) {
	if !httpsOrigin(cfg.Origin) || !validID(cfg.Issuer) || !validID(cfg.Subject) || cfg.ParentTokenFile == "" || (cfg.Kind != "device" && cfg.Kind != "service") {
		return nil, ErrInvalid
	}
	return &WorkloadExchange{cfg: cfg, client: wireClient(client), now: time.Now, gate: make(chan struct{}, 1), cached: map[string]scopedJWT{}}, nil
}

// Token only mints route-scoped Tachyon tokens. Cached short JWTs still undergo
// the recipient's fresh lifecycle check on EVERY use, avoiding needless mints
// against Tether's bounded live-mint ledger. There is no refresh-on-denial loop.
func (e *WorkloadExchange) Token(ctx context.Context, scope string) (string, error) {
	if e == nil || (scope != ItemReadScope && scope != CompletionScope && scope != OutcomeReadScope) {
		return "", ErrDenied
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case e.gate <- struct{}{}:
		defer func() { <-e.gate }()
	case <-ctx.Done():
		return "", ErrUnavailable
	}
	parent, err := tokenfile.Read(e.cfg.ParentTokenFile, tokenfile.Options{Validate: validParent})
	if err != nil {
		clear(e.cached)
		return "", ErrUnavailable
	}
	digest := sha256.Sum256([]byte(parent))
	if old, ok := e.cached[scope]; ok && old.parent == digest && e.now().Add(5*time.Second).Before(old.expiry) && ctx.Err() == nil {
		return old.token, nil
	}
	delete(e.cached, scope)
	input := struct {
		Audience string   `json:"audience"`
		Scopes   []string `json:"scopes"`
		TTL      int      `json:"ttl_seconds"`
	}{TachyonAudience, []string{scope}, 300}
	var reply struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
		TTL       int       `json:"ttl_seconds"`
		Audience  string    `json:"aud"`
		Scopes    []string  `json:"scopes"`
		Kind      string    `json:"kind"`
		KID       string    `json:"kid"`
		JTI       string    `json:"jti"`
	}
	if err = postWire(ctx, e.client, e.cfg.Origin+"/auth/token", parent, true, input, &reply, 16<<10); err != nil {
		return "", err
	}
	parts := strings.Split(reply.Token, ".")
	if len(parts) != 3 || len(reply.Token) > 8192 {
		return "", ErrInvalid
	}
	var h struct {
		Alg  string `json:"alg"`
		Type string `json:"typ"`
		Key  string `json:"kid"`
	}
	var c workloadClaims
	hraw, he := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	craw, ce := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	sig, se := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if he != nil || ce != nil || se != nil || len(sig) != 64 || exact(hraw, 1024, &h) != nil || exact(craw, 6144, &c) != nil || h.Alg != "EdDSA" || h.Type != "JWT" || !validID(h.Key) || h.Key != reply.KID {
		return "", ErrInvalid
	}
	if ctx.Err() != nil || c.Issuer != e.cfg.Issuer || c.Subject != e.cfg.Subject || c.Parent != c.Subject || c.Kind != e.cfg.Kind || c.Session != "" || c.Audience != TachyonAudience || c.Scope != scope || !validID(c.ID) || !c.validAt(e.now()) || c.Expiry-c.Issued > 300 || reply.TTL < 1 || reply.TTL > 300 || reply.Audience != c.Audience || reply.Kind != c.Kind || reply.JTI != c.ID || len(reply.Scopes) != 1 || reply.Scopes[0] != scope || !reply.ExpiresAt.Equal(time.Unix(c.Expiry, 0)) {
		return "", ErrDenied
	}
	// This endpoint is the configured authenticated HTTPS issuer. Its response
	// is private transport material; Tachyon verifies the actual JWT signature
	// and current lifecycle before trusting it, independently of this parsing.
	e.cached[scope] = scopedJWT{reply.Token, reply.ExpiresAt, digest}
	return reply.Token, nil
}
