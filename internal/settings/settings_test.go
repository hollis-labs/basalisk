package settings

import (
	"strings"
	"testing"
)

func TestEmptyBlockMeansNoDeliveryAndLoopbackOnly(t *testing.T) {
	for _, raw := range []any{nil, map[string]any{}} {
		s, err := Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(s.AllowedHosts) != 0 || s.Delivery.IntegrationID != "" || s.Delivery.PublicOrigin != "" || s.Delivery.IssuerURL != "" || s.Delivery.Subject != "" || s.Delivery.ParentRef != "" {
			t.Fatalf("defaults are not empty: %+v", s)
		}
	}
}

// One key per former environment variable.
func TestKeysMapToTheFormerEnvironment(t *testing.T) {
	s, err := Parse(map[string]any{
		"fe_integration_id": "int-1", "public_origin": "https://basalisk.example.test",
		"tether_issuer_url": "https://issuer.example.test", "tether_subject": "svc:basalisk", "tether_parent_ref": "file://delivery/parent",
		"allowed_hosts": []any{"Basalisk.Example.Test", "other.example.test:8443"},
	})
	if err != nil {
		t.Fatal(err)
	}
	d := s.Delivery
	if d.IntegrationID != "int-1" || d.PublicOrigin != "https://basalisk.example.test" || d.IssuerURL != "https://issuer.example.test" || d.Subject != "svc:basalisk" || d.ParentRef != "file://delivery/parent" {
		t.Fatalf("delivery: %+v", d)
	}
	if len(s.AllowedHosts) != 2 || s.AllowedHosts[0] != "basalisk.example.test" || s.AllowedHosts[1] != "other.example.test:8443" {
		t.Fatalf("hosts: %v", s.AllowedHosts)
	}
}

func TestBadValuesAreRefusedNamingTheKey(t *testing.T) {
	for name, c := range map[string]struct {
		in   any
		want string
	}{
		"unknown key":   {map[string]any{"https_proxy": true}, "https_proxy"},
		"not a map":     {"nope", "map"},
		"string typed":  {map[string]any{"public_origin": 7}, "public_origin"},
		"hosts typed":   {map[string]any{"allowed_hosts": "a.example.test"}, "allowed_hosts"},
		"host item":     {map[string]any{"allowed_hosts": []any{3}}, "allowed_hosts"},
		"host with url": {map[string]any{"allowed_hosts": []any{"https://a.example.test"}}, "allowed_hosts"},
		"empty host":    {map[string]any{"allowed_hosts": []any{" "}}, "allowed_hosts"},
	} {
		if _, err := Parse(c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to name %q", name, err, c.want)
		}
	}
}
