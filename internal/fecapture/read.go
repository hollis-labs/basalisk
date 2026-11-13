package fecapture

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// ReadScope is the scope a token needs to read fragments from FE's reader.
const ReadScope = "fragments-engine.read"

// ExchangeRead is Exchange for the read scope: the same dedicated parent credential and the
// same consistency checks on the issuer's answer, for a token that reads instead of captures.
func (c *Client) ExchangeRead(ctx context.Context) (string, error) {
	if c.resolver == nil {
		return "", failed("invalid_configuration", false)
	}
	parent, err := c.resolver.Resolve(ctx, c.config.ParentRef)
	if err != nil {
		return "", failed("credential_unavailable", false)
	}
	var reply struct {
		Token     string    `json:"token"`
		Audience  string    `json:"aud"`
		Scopes    []string  `json:"scopes"`
		Kind      string    `json:"kind"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	raw := []byte(`{"audience":"fragments-engine","scopes":["` + ReadScope + `"],"ttl_seconds":900}`)
	if err = c.request(ctx, "POST", c.config.IssuerURL+"/auth/token", parent.Secret.Reveal(), raw, &reply, 200); err != nil {
		return "", err
	}
	parts := strings.Split(reply.Token, ".")
	var claims struct {
		Sub, Aud, Scope, Kind string
		Exp                   int64
	}
	if len(parts) != 3 || len(reply.Token) > 8192 {
		return "", failed("invalid_issuer_response", false)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(payload, &claims) != nil || claims.Sub != c.config.Subject || claims.Aud != "fragments-engine" || claims.Scope != ReadScope || claims.Kind != "service" || claims.Exp <= time.Now().Add(30*time.Second).Unix() || reply.Audience != "fragments-engine" || len(reply.Scopes) != 1 || reply.Scopes[0] != ReadScope || reply.Kind != "service" || !reply.ExpiresAt.After(time.Now().Add(30*time.Second)) {
		return "", failed("invalid_issuer_response", false)
	}
	return reply.Token, nil
}
