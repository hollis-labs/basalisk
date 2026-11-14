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
//	  tether_mcp: []             # command that starts a Tether MCP gateway on stdio; resolves cited records
//	  agent_gateway_url: ""      # the agent's model gateway: an Anthropic-compatible base, requests go to <url>/v1/messages
//	  agent_key_file: ""         # file holding the gateway's bearer key (default ~/.config/basalisk/agent-key)
//	  agent_model: ""            # the model or gateway alias the agent uses
//	  agent_urn: ""              # the agent's own Tether address (msg://agent/…), for mail; empty: no mail tools
//
// Delivery stays off while all five delivery keys are empty. Setting some but not all, or an
// invalid value, is refused when the worker is built (api.Service.NewFragmentWorker).
package settings

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"

	"github.com/hollis-labs/basalisk/internal/agent"
	"github.com/hollis-labs/basalisk/internal/api"
)

// Key is the name of the config block.
const Key = "basalisk"

// Settings are the parsed block.
type Settings struct {
	AllowedHosts []string
	// TetherMCP is the command that starts a Tether MCP gateway on stdio (internal/tetherlink).
	// Empty means references are not resolved.
	TetherMCP []string
	Delivery  api.DeliveryConfig
	// Agent is where the agent's model is (internal/agent). The agent answers only when the
	// gateway and the model are both set.
	Agent agent.Config
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
	known := map[string]bool{"tether_mcp": true, "allowed_hosts": true, "fe_integration_id": true, "public_origin": true, "tether_issuer_url": true, "tether_subject": true, "tether_parent_ref": true,
		"agent_gateway_url": true, "agent_key_file": true, "agent_model": true, "agent_urn": true}
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
	if s.Agent.GatewayURL, err = str("agent_gateway_url"); err != nil {
		return s, err
	}
	if s.Agent.GatewayURL != "" {
		if u, perr := url.Parse(s.Agent.GatewayURL); perr != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return s, fmt.Errorf("basalisk.agent_gateway_url must be an http(s) URL")
		}
	}
	if s.Agent.KeyFile, err = str("agent_key_file"); err != nil {
		return s, err
	}
	if s.Agent.Model, err = str("agent_model"); err != nil {
		return s, err
	}
	if s.Agent.URN, err = str("agent_urn"); err != nil {
		return s, err
	}
	if s.Agent.URN != "" && !strings.HasPrefix(s.Agent.URN, "msg://agent/") {
		return s, fmt.Errorf("basalisk.agent_urn must be a Tether agent address (msg://agent/…)")
	}
	switch argv := block["tether_mcp"].(type) {
	case nil:
	case []any:
		for _, part := range argv {
			t, ok := part.(string)
			if !ok || t == "" {
				return s, fmt.Errorf("basalisk.tether_mcp must be a list of non-empty strings (a command and its arguments)")
			}
			s.TetherMCP = append(s.TetherMCP, t)
		}
	default:
		return s, fmt.Errorf("basalisk.tether_mcp must be a list of strings (a command and its arguments)")
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
