package account

import (
	"strings"
	"testing"

	"github.com/hollis-labs/libs/security/pairing"
)

func TestFragmentConnectionRequiresExplicitOwnerAndDeviceGrants(t *testing.T) {
	s, c := newStore(t)
	h := s.CompanionHandler()
	c.call("POST", "/api/setup", setupBody(), 200)
	key := "synthetic-fe-secret-not-returned"
	i := responseAs[Integration](t, c.call("POST", "/api/integrations", map[string]any{"name": "FE", "provider": "fragments-engine", "baseUrl": "https://fe.example.test", "credential": key}, 201))
	old := pairDevice(t, c, h, nil)
	nativeCall(t, h, old.Token, "GET", "/companion/fragment-connection", nil, 403)
	c.call("PUT", "/api/fragment-connections/"+old.Device.ID, map[string]string{"integrationId": i.ID}, 403)
	g := responseAs[pairing.Grant](t, c.call("POST", "/api/devices/grants", map[string]any{"label": "Capture Mac", "allowFragments": true}, 201))
	x := responseAs[pairing.Exchange](t, nativeCall(t, h, "", "POST", pairing.ExchangePath, pairing.ExchangeRequest{Code: g.Code, Audience: "basalisk"}, 200))
	nativeCall(t, h, x.Token, "GET", "/companion/fragment-connection", nil, 409)
	// A paired device cannot grant itself integration access through owner APIs.
	nativeCall(t, s.Handler(), x.Token, "GET", "/api/fragment-connections", nil, 401)
	c.call("PUT", "/api/fragment-connections/"+x.Device.ID, map[string]string{"integrationId": i.ID}, 200)
	w := nativeCall(t, h, x.Token, "GET", "/companion/fragment-connection", nil, 200)
	if strings.Contains(w.Body.String(), key) || strings.Contains(w.Body.String(), "file://") || strings.Contains(w.Body.String(), "https://") {
		t.Fatal("connection exposed credential or destination")
	}
	connection := responseAs[fragmentConnection](t, w)
	if connection.DeviceID != x.Device.ID || connection.IntegrationID != i.ID {
		t.Fatal("wrong connection")
	}
	c.call("DELETE", "/api/fragment-connections/"+x.Device.ID, nil, 200)
	nativeCall(t, h, x.Token, "GET", "/companion/fragment-connection", nil, 409)
	c.call("PUT", "/api/fragment-connections/"+x.Device.ID, map[string]string{"integrationId": i.ID}, 200)
	c.call("DELETE", "/api/devices/"+x.Device.ID, nil, 200)
	nativeCall(t, h, x.Token, "GET", "/companion/fragment-connection", nil, 401)
	c.call("PUT", "/api/fragment-connections/"+x.Device.ID, map[string]string{"integrationId": i.ID}, 403)
	c.call("DELETE", "/api/integrations/"+i.ID, nil, 200)
	connections := responseAs[[]fragmentConnection](t, c.call("GET", "/api/fragment-connections", nil, 200))
	if len(connections) != 0 {
		t.Fatal("grant survived integration deletion")
	}
}

func TestFragmentConnectionRejectsOtherProvidersAndPaths(t *testing.T) {
	s, c := newStore(t)
	h := s.CompanionHandler()
	c.call("POST", "/api/setup", setupBody(), 200)
	g := responseAs[pairing.Grant](t, c.call("POST", "/api/devices/grants", map[string]any{"label": "Capture Mac", "allowFragments": true}, 201))
	x := responseAs[pairing.Exchange](t, nativeCall(t, h, "", "POST", pairing.ExchangePath, pairing.ExchangeRequest{Code: g.Code, Audience: "basalisk"}, 200))
	for _, tc := range []struct{ provider, origin string }{{"other-service", "https://other.example.test"}, {"fragments-engine", "https://fe.example.test/admin"}, {"fragments-engine", ""}} {
		i := responseAs[Integration](t, c.call("POST", "/api/integrations", map[string]any{"name": "Unsafe adapter", "provider": tc.provider, "baseUrl": tc.origin, "credential": "synthetic-key"}, 201))
		c.call("PUT", "/api/fragment-connections/"+x.Device.ID, map[string]string{"integrationId": i.ID}, 400)
	}
}
