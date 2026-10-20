package assurance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/basalisk/internal/requestorigin"
)

func testHostConfig() HostConfig {
	c := HostConfig{Authority: "bas_test", Origin: requestorigin.CanonicalOrigin, RPID: requestorigin.CanonicalHost, Audiences: []string{"tether"}, CurrentTrust: CurrentTrustConfig{"https://tether.test", "issuer", "/private/trust-reader"}}
	c.Authentication.Issuer = "issuer"
	c.Authentication.JWKSURL = "https://tether.test/.well-known/jwks.json"
	c.Authentication.Callers = []WorkloadPin{{"tachyon", "device", []string{"tether"}}}
	c.Tachyon.Origin = "https://tachyon.test"
	c.Tachyon.Exchange = ExchangeConfig{"https://tether.test", "issuer", "/private/tachyon-parent", "basalisk", "device"}
	return c
}
func TestHostConfigurationRequiresExplicitConsistentPins(t *testing.T) {
	if cfg, err := LoadHostConfig(""); cfg != nil || err != nil {
		t.Fatal("absence activated assurance")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	for name, change := range map[string]func(*HostConfig){
		"valid":                           func(*HostConfig) {},
		"wrong RP":                        func(c *HostConfig) { c.RPID = "localhost" },
		"wrong origin":                    func(c *HostConfig) { c.Origin = "http://localhost" },
		"issuer mismatch":                 func(c *HostConfig) { c.CurrentTrust.Issuer = "other" },
		"key endpoint mismatch":           func(c *HostConfig) { c.Authentication.JWKSURL = "https://other.test/.well-known/jwks.json" },
		"shared credentials":              func(c *HostConfig) { c.CurrentTrust.ReaderTokenFile = c.Tachyon.Exchange.ParentTokenFile },
		"unconfigured operation audience": func(c *HostConfig) { c.Authentication.Callers[0].Audiences = []string{"other"} },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := testHostConfig()
			change(&cfg)
			raw, _ := json.Marshal(cfg)
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadHostConfig(path)
			if (err == nil) != (name == "valid") {
				t.Fatalf("configuration accepted=%v", err == nil)
			}
		})
	}
	if err := os.WriteFile(path, []byte(`{"Authority":"bas_test"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHostConfig(path); err == nil {
		t.Fatal("incomplete alias config accepted")
	}
}

func TestExchangeWaitRespectsCancellation(t *testing.T) {
	e, err := NewWorkloadExchange(testHostConfig().Tachyon.Exchange, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.gate <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err = e.Token(ctx, ItemReadScope); err == nil {
		t.Fatal("canceled exchange queue accepted")
	}
	<-e.gate
}
