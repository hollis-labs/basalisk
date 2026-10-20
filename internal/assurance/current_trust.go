package assurance

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"slices"
	"strings"
	"time"

	auth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/libs/security/secrets/tokenfile"
)

type CurrentTrustConfig struct {
	Origin          string `json:"origin"`
	Issuer          string `json:"issuer"`
	ReaderTokenFile string `json:"reader_token_file"`
}
type currentWorkload struct {
	Issuer    string    `json:"issuer"`
	Subject   string    `json:"subject"`
	Kind      string    `json:"kind"`
	Audience  string    `json:"audience"`
	Scopes    []string  `json:"scopes"`
	TokenID   string    `json:"token_id"`
	Parent    string    `json:"parent"`
	ExpiresAt time.Time `json:"expires_at"`
}
type CurrentTrustClient struct {
	cfg    CurrentTrustConfig
	client *http.Client
	now    func() time.Time
}

func validParent(value string) bool {
	return strings.HasPrefix(value, "tth_") && len(value) > 4 && len(value) <= 256 && strings.IndexFunc(value, func(c rune) bool { return c <= ' ' || c >= 127 }) < 0
}
func NewCurrentTrustClient(cfg CurrentTrustConfig, client *http.Client) (*CurrentTrustClient, error) {
	if !httpsOrigin(cfg.Origin) || !validID(cfg.Issuer) || cfg.ReaderTokenFile == "" {
		return nil, ErrInvalid
	}
	return &CurrentTrustClient{cfg, wireClient(client), time.Now}, nil
}
func (c *CurrentTrustClient) CheckCurrentWorkload(ctx context.Context, p auth.Principal) error {
	if c == nil || !p.Verified || !validID(p.ID) || p.Parent != p.ID || (p.Kind != "device" && p.Kind != "service") || p.SessionID != "" || !validID(p.TokenID) || p.Audience != StatusTokenAudience || !slices.Equal(p.Scopes, []string{StatusTokenScope}) || !c.now().Before(p.ExpiresAt) {
		return ErrDenied
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Read through shared custody on every request: removal, permissions and
	// rotation must not retain a previously usable reader credential.
	reader, err := tokenfile.Read(c.cfg.ReaderTokenFile, tokenfile.Options{Validate: validParent})
	if err != nil {
		return ErrUnavailable
	}
	nonce := make([]byte, 32)
	if _, err = rand.Read(nonce); err != nil {
		return ErrUnavailable
	}
	query := base64.RawURLEncoding.EncodeToString(nonce)
	target := currentWorkload{c.cfg.Issuer, p.ID, p.Kind, p.Audience, append([]string(nil), p.Scopes...), p.TokenID, p.Parent, p.ExpiresAt.UTC()}
	input := struct {
		QueryID  string          `json:"query_id"`
		Workload currentWorkload `json:"workload"`
	}{query, target}
	var reply struct {
		QueryID   string          `json:"query_id"`
		Workload  currentWorkload `json:"workload"`
		Active    bool            `json:"active"`
		CheckedAt time.Time       `json:"checked_at"`
	}
	if err = postWire(ctx, c.client, c.cfg.Origin+"/auth/workload-trust", reader, true, input, &reply, 16<<10); err != nil {
		return err
	}
	w := reply.Workload
	if ctx.Err() != nil || !c.now().Before(p.ExpiresAt) || !reply.Active || reply.QueryID != query || reply.CheckedAt.IsZero() || reply.CheckedAt.After(c.now().Add(time.Second)) || w.Issuer != target.Issuer || w.Subject != target.Subject || w.Kind != target.Kind || w.Audience != target.Audience || !slices.Equal(w.Scopes, target.Scopes) || w.TokenID != target.TokenID || w.Parent != target.Parent || !w.ExpiresAt.Equal(target.ExpiresAt) {
		return ErrDenied
	}
	return nil
}
