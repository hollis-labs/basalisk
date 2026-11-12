// Package settings reads the app's own "basalisk" block from the manifest config. The shell
// modules read theirs; this block holds what only Basalisk has: the extra hosts it answers
// to and the optional fragment delivery worker.
//
//	basalisk:
//	  allowed_hosts: []          # extra Host values (besides loopback) served behind a local TLS proxy
//	  fe_integration_id: ""      # integration the delivery worker serves
//	  public_origin: ""          # canonical origin of this Basalisk, https://host
//	  tether_issuer_url: ""      # HTTPS origin of the capture credential issuer
//	  tether_subject: ""         # principal the capture credential is minted for
//	  tether_parent_ref: ""      # secret reference of the dedicated enrollment parent
//
// Delivery stays off while all five delivery keys are empty. Setting some but not all, or an
// invalid value, is refused when the worker is built (api.Service.NewFragmentWorker).
package settings

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/hollis-labs/basalisk/internal/api"
)

// Key is the name of the config block.
const Key = "basalisk"

// Settings are the parsed block.
type Settings struct {
	AllowedHosts []string
	Delivery     api.DeliveryConfig
}

// Parse reads the block (nil for a missing one). An unknown key is an error, so a typo is
// found at startup instead of silently turning a protection off.
func Parse(raw any) (Settings, error) {
	var s Settings
	if raw == nil {
		return s, nil
	}
	block, ok := raw.(map[string]any)
	if !ok {
		return s, fmt.Errorf("basalisk: the config block must be a map")
	}
	str := func(k string) (string, error) {
		v, present := block[k]
		if !present || v == nil {
			return "", nil
		}
		t, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("basalisk.%s must be a string", k)
		}
		return strings.TrimSpace(t), nil
	}
	known := map[string]bool{"allowed_hosts": true, "fe_integration_id": true, "public_origin": true, "tether_issuer_url": true, "tether_subject": true, "tether_parent_ref": true}
	var unknown []string
	for k := range block {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return s, fmt.Errorf("basalisk: unknown config key(s): %s", strings.Join(unknown, ", "))
	}
	var err error
	if s.Delivery.IntegrationID, err = str("fe_integration_id"); err != nil {
		return s, err
	}
	if s.Delivery.PublicOrigin, err = str("public_origin"); err != nil {
		return s, err
	}
	if s.Delivery.IssuerURL, err = str("tether_issuer_url"); err != nil {
		return s, err
	}
	if s.Delivery.Subject, err = str("tether_subject"); err != nil {
		return s, err
	}
	if s.Delivery.ParentRef, err = str("tether_parent_ref"); err != nil {
		return s, err
	}
	switch hosts := block["allowed_hosts"].(type) {
	case nil:
	case []any:
		for _, h := range hosts {
			t, ok := h.(string)
			if !ok {
				return s, fmt.Errorf("basalisk.allowed_hosts must be a list of strings")
			}
			t = strings.ToLower(strings.TrimSpace(t))
			if t == "" || strings.ContainsAny(t, "/@ ") {
				return s, fmt.Errorf("basalisk.allowed_hosts: %q is not a host name", t)
			}
			if _, _, err := net.SplitHostPort(t); err != nil && strings.Contains(t, ":") {
				return s, fmt.Errorf("basalisk.allowed_hosts: %q is not a host name", t)
			}
			s.AllowedHosts = append(s.AllowedHosts, t)
		}
	default:
		return s, fmt.Errorf("basalisk.allowed_hosts must be a list of strings")
	}
	return s, nil
}
