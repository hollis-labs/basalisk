// Package fecapture consumes FE's scoped public capture contract. It never uses
// an FE owner key, browser cookie, or a device/activity-session credential.
package fecapture

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/hollis-labs/libs/security/secrets"
)

const Scope = "fragments-engine.capture"

type Config struct{ IssuerURL, Subject, ParentRef string }
type Client struct {
	config   Config
	resolver *secrets.Resolver
	http     *http.Client
}
type Failure struct {
	Code      string
	Retryable bool
}

func (e *Failure) Error() string           { return e.Code }
func failed(code string, retry bool) error { return &Failure{code, retry} }
func New(config Config, resolver *secrets.Resolver, client *http.Client) (*Client, error) {
	u, err := url.Parse(config.IssuerURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || config.ParentRef == "" || strings.TrimSpace(config.Subject) == "" {
		return nil, errors.New("invalid FE issuer configuration (HTTPS issuer origin, subject and dedicated parent reference required)")
	}
	if client == nil {
		client = &http.Client{}
	}
	copy := *client
	copy.Timeout = 10 * time.Second
	copy.Jar = nil
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	config.IssuerURL = strings.TrimRight(config.IssuerURL, "/")
	return &Client{config, resolver, &copy}, nil
}
func (c *Client) request(ctx context.Context, method, endpoint, bearer string, body []byte, out any, accepted ...int) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return failed("invalid_configuration", false)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return failed("connection_unavailable", true)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		switch res.StatusCode {
		case 401, 403:
			return failed("authorization_required", false)
		case 409:
			return failed("capture_conflict", false)
		case 400, 404, 413, 422:
			return failed("capture_rejected", false)
		case 408, 429:
			return failed("service_busy", true)
		default:
			return failed("service_unavailable", res.StatusCode >= 500)
		}
	}
	if !slices.Contains(accepted, res.StatusCode) {
		return failed("invalid_receipt", false)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1024*1024+1))
	if err != nil {
		return failed("connection_unavailable", true)
	}
	if len(raw) > 1024*1024 || json.Unmarshal(raw, out) != nil {
		return failed("invalid_receipt", false)
	}
	return nil
}

// Exchange resolves the dedicated enrollment parent on every attempt so rotation
// is observed without a restart. JWTs stay in memory and are never journalled.
func (c *Client) Exchange(ctx context.Context) (string, error) {
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
	raw := []byte(`{"audience":"fragments-engine","scopes":["fragments-engine.capture"],"ttl_seconds":900}`)
	if err = c.request(ctx, "POST", c.config.IssuerURL+"/auth/token", parent.Secret.Reveal(), raw, &reply, 200); err != nil {
		return "", err
	}
	// These are consistency checks on the trusted HTTPS issuer response, not JWT
	// verification or delegated authority. FE independently verifies the signature.
	parts := strings.Split(reply.Token, ".")
	var claims struct {
		Sub, Aud, Scope, Kind string
		Exp                   int64
	}
	if len(parts) != 3 || len(reply.Token) > 8192 {
		return "", failed("invalid_issuer_response", false)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(payload, &claims) != nil || claims.Sub != c.config.Subject || claims.Aud != "fragments-engine" || claims.Scope != Scope || claims.Kind != "service" || claims.Exp <= time.Now().Add(30*time.Second).Unix() || reply.Audience != "fragments-engine" || len(reply.Scopes) != 1 || reply.Scopes[0] != Scope || reply.Kind != "service" || !reply.ExpiresAt.After(time.Now().Add(30*time.Second)) {
		return "", failed("invalid_issuer_response", false)
	}
	return reply.Token, nil
}

type Receipt struct {
	SchemaVersion     string            `json:"schema_version"`
	CaptureID         string            `json:"capture_id"`
	FragmentID        string            `json:"fragment_id"`
	RevisionID        string            `json:"fragment_revision_id"`
	AttemptID         string            `json:"capture_attempt_id"`
	Completion        string            `json:"completion"`
	AssetInstructions []json.RawMessage `json:"asset_instructions"`
	Assets            []json.RawMessage `json:"assets"`
}

func safeID(s string) bool {
	if len(s) == 0 || len(s) > 255 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
func (r Receipt) Valid(captureID string) bool {
	return r.CaptureID == captureID && safeID(r.FragmentID) && safeID(r.RevisionID) && safeID(r.AttemptID)
}
func (c *Client) Accept(ctx context.Context, origin, bearer, captureID string, manifest []byte) (Receipt, error) {
	var r Receipt
	err := c.request(ctx, "POST", origin+"/v1/captures", bearer, manifest, &r, 200, 201)
	if err != nil {
		return r, err
	}
	if r.SchemaVersion != "fe.capture.result.v1" || !r.Valid(captureID) || len(r.AssetInstructions) != 0 || (r.Completion != "accepting" && r.Completion != "complete") {
		return r, failed("invalid_receipt", false)
	}
	return r, nil
}
func (c *Client) Complete(ctx context.Context, origin, bearer string, accepted Receipt) (Receipt, error) {
	var r Receipt
	body, _ := json.Marshal(map[string]any{"schema_version": "fe.capture.completion.v1", "idempotency_key": "complete-" + accepted.CaptureID, "assets": []any{}, "warnings": []any{}})
	err := c.request(ctx, "POST", origin+"/v1/captures/"+url.PathEscape(accepted.CaptureID)+"/complete", bearer, body, &r, 200)
	if err != nil {
		return r, err
	}
	if r.SchemaVersion != "fe.capture.status.v1" || !r.Valid(accepted.CaptureID) || r.FragmentID != accepted.FragmentID || r.RevisionID != accepted.RevisionID || r.AttemptID != accepted.AttemptID || r.Completion != "complete" || len(r.Assets) != 0 {
		return r, failed("invalid_receipt", false)
	}
	return r, nil
}
