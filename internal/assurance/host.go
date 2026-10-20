package assurance

import (
	"io"
	"os"
	"path/filepath"

	"github.com/hollis-labs/basalisk/internal/requestorigin"
)

// HostConfig contains only public pins and private-file references. No token
// values are accepted, exported by an API or persisted in the account database.
type HostConfig struct {
	Authority      string   `json:"authority"`
	Origin         string   `json:"origin"`
	RPID           string   `json:"rp_id"`
	Audiences      []string `json:"audiences"`
	Authentication struct {
		Issuer  string        `json:"issuer"`
		JWKSURL string        `json:"jwks_url"`
		Callers []WorkloadPin `json:"callers"`
	} `json:"authentication"`
	CurrentTrust CurrentTrustConfig `json:"current_trust"`
	Tachyon      struct {
		Origin   string         `json:"origin"`
		Exchange ExchangeConfig `json:"exchange"`
	} `json:"tachyon"`
}

func LoadHostConfig(path string) (*HostConfig, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path) // #nosec G304 -- operator-selected startup configuration, never request input.
	if err != nil {
		return nil, ErrUnavailable
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, (32<<10)+1))
	if err != nil || len(raw) > 32<<10 {
		return nil, ErrInvalid
	}
	var cfg HostConfig
	if decodeWire(raw, &cfg) != nil || cfg.Validate() != nil {
		return nil, ErrInvalid
	}
	return &cfg, nil
}
func (c HostConfig) Validate() error {
	if !validID(c.Authority) || c.Origin != requestorigin.CanonicalOrigin || c.RPID != requestorigin.CanonicalHost || len(c.Audiences) == 0 || !validID(c.Authentication.Issuer) || len(c.Authentication.Callers) == 0 || c.CurrentTrust.Issuer != c.Authentication.Issuer || c.Tachyon.Exchange.Issuer != c.Authentication.Issuer || c.CurrentTrust.Origin != c.Tachyon.Exchange.Origin || !httpsOrigin(c.CurrentTrust.Origin) || c.Authentication.JWKSURL != c.CurrentTrust.Origin+"/.well-known/jwks.json" || !httpsOrigin(c.Tachyon.Origin) || c.CurrentTrust.ReaderTokenFile == "" || c.Tachyon.Exchange.ParentTokenFile == "" || filepath.Clean(c.CurrentTrust.ReaderTokenFile) == filepath.Clean(c.Tachyon.Exchange.ParentTokenFile) {
		return ErrInvalid
	}
	for _, p := range c.Authentication.Callers {
		for _, aud := range p.Audiences {
			if !contains(c.Audiences, aud) {
				return ErrInvalid
			}
		}
	}
	return nil
}
