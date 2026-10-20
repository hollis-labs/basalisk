package assurance

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	auth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/libs/security/secrets/tokenfile"
)

func TestTachyonExchangeItemsAndOutcomeRecovery(t *testing.T) {
	f := newFixture(t)
	completion, _ := f.complete("attempt-wire")
	retained := hitl.Resolved{ItemID: "item-a", InteractionRevision: 2, Resolution: hitl.ResolutionRecord{ResolutionID: "resolution-a", InteractionRevision: 2, ResolvedAt: completion.AuthTime, Response: hitl.Response{Kind: "approval", Decision: "approved"}, Participant: hitl.Participant{PrincipalRef: "human-a", Authority: "basalisk-test", Assurance: "webauthn-uv"}}}
	path := filepath.Join(t.TempDir(), "parent")
	if err := tokenfile.Create(path, "tth_parent_one"); err != nil {
		t.Fatal(err)
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var mints, delivered atomic.Int32
	var mode atomic.Int32
	var peer *httptest.Server
	now := time.Now().UTC().Truncate(time.Second)
	peer = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/.well-known/jwks.json" {
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "OKP", "crv": "Ed25519", "kid": "signer", "x": base64.RawURLEncoding.EncodeToString(pub)}}})
			return
		}
		if r.URL.Path == "/auth/token" {
			mints.Add(1)
			parent := r.Header.Get("Authorization")
			if parent != "Bearer tth_parent_one" && parent != "Bearer tth_parent_two" {
				t.Error("wrong parent")
			}
			if r.Header.Get("Tether-Protocol") != "1" {
				t.Error("missing protocol")
			}
			var in struct {
				Audience string   `json:"audience"`
				Scopes   []string `json:"scopes"`
				TTL      int      `json:"ttl_seconds"`
			}
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.Audience != TachyonAudience || len(in.Scopes) != 1 || in.TTL != 300 {
				t.Error("wrong exchange profile")
				w.WriteHeader(400)
				return
			}
			claims := workloadClaims{Issuer: "issuer", Subject: "basalisk", Parent: "basalisk", Kind: "device", Audience: TachyonAudience, Scope: in.Scopes[0], Issued: now.Unix(), Expiry: now.Add(5 * time.Minute).Unix(), ID: "mint-wire"}
			token := signedWorkload(t, key, map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": "signer"}, claims)
			_ = json.NewEncoder(w).Encode(map[string]any{"token": token, "expires_at": time.Unix(claims.Expiry, 0).UTC(), "ttl_seconds": 300, "aud": TachyonAudience, "scopes": in.Scopes, "kind": "device", "kid": "signer", "jti": claims.ID})
			return
		}
		if mode.Load() == 1 {
			w.WriteHeader(503)
			return
		}
		v, e := auth.NewJWTVerifier(auth.JWTConfig{Issuer: "issuer", Audience: TachyonAudience, JWKSURL: peer.URL + "/.well-known/jwks.json", Client: peer.Client()})
		if e != nil {
			t.Error(e)
			return
		}
		p, e := v.Verify(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if e != nil || !p.Verified || p.ID != "basalisk" {
			t.Error("invalid workload bearer")
			w.WriteHeader(401)
			return
		}
		var in struct {
			ItemID       string `json:"item_id"`
			QueryID      string `json:"query_id"`
			CompletionID string `json:"completion_id"`
			Revision     int64  `json:"expected_revision"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			t.Error("bad command")
			return
		}
		if in.ItemID != "item-a" {
			t.Error("changed retained item")
		}
		query := in.QueryID
		if mode.Load() == 2 {
			query = "stale"
		}
		switch r.URL.Path {
		case "/api/approvals/trusted/v1/item":
			if len(p.Scopes) != 1 || p.Scopes[0] != ItemReadScope {
				t.Error("wrong item scope")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"query_id": query, "item": f.items.item})
		case "/api/approvals/trusted/v1/complete":
			if len(p.Scopes) != 1 || p.Scopes[0] != CompletionScope || in.CompletionID != completion.ID || in.Revision != 1 || in.QueryID != "" {
				t.Error("wrong delivery")
			}
			delivered.Add(1)
			if mode.Load() == 3 {
				_ = json.NewEncoder(w).Encode(map[string]any{"outcome": retained})
				return
			}
			// Model an ambiguous response after acceptance. Recovery must use
			// the saved item and never mint another human ceremony.
			w.WriteHeader(503)
		case "/api/approvals/trusted/v1/outcome":
			if len(p.Scopes) != 1 || p.Scopes[0] != OutcomeReadScope {
				t.Error("wrong outcome scope")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"query_id": query, "item_id": in.ItemID, "revision": 2, "state": "resolved", "terminal_outcome": retained})
		default:
			w.WriteHeader(404)
		}
	}))
	defer peer.Close()
	exchange, err := NewWorkloadExchange(ExchangeConfig{peer.URL, "issuer", path, "basalisk", "device"}, peer.Client())
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewTachyonClient(peer.URL, exchange, peer.Client())
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if item, e := client.LookupItem(context.Background(), "item-a"); e != nil || item.Digest != f.items.item.Digest {
			t.Fatal("item lookup", e)
		}
	}
	if mints.Load() != 1 {
		t.Fatal("per-call mint amplification")
	}
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			if _, e := exchange.Token(context.Background(), ItemReadScope); e != nil {
				t.Error(e)
			}
		})
	}
	group.Wait()
	if mints.Load() != 1 {
		t.Fatal("concurrent token mint amplification")
	}
	if err = tokenfile.Write(path, "tth_parent_two"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.LookupItem(context.Background(), "item-a"); err != nil || mints.Load() != 2 {
		t.Fatal("rotation not honored", err)
	}
	if _, err = f.service.Deliver(context.Background(), "session-a", completion.ID, client); err == nil || delivered.Load() != 1 {
		t.Fatal("delivery loss not surfaced")
	}
	if _, err = f.service.Deliver(context.Background(), "session-b", completion.ID, client); err == nil {
		t.Fatal("different initiating session delivered")
	}
	mode.Store(3)
	if _, err = f.service.Deliver(context.Background(), "session-a", completion.ID, client); err != nil || delivered.Load() != 2 {
		t.Fatal("same-reference retry", err)
	}
	mode.Store(0)
	f.now = f.now.Add(6 * time.Minute)
	if _, err = f.service.Deliver(context.Background(), "session-a", completion.ID, client); err == nil || delivered.Load() != 2 {
		t.Fatal("expired proof redelivered")
	}
	result, err := f.service.RecoverOutcome(context.Background(), "session-b", completion.ID, client)
	if err != nil || result.State != hitl.StateResolved {
		t.Fatal("proof-independent outcome recovery", err)
	}
	if _, err = f.service.RecoverOutcome(context.Background(), "session-other", completion.ID, client); err == nil {
		t.Fatal("other human read outcome")
	}
	mode.Store(2)
	if _, err = client.LookupItem(context.Background(), "item-a"); err == nil {
		t.Fatal("stale item echo accepted")
	}
	if _, err = client.LookupOutcome(context.Background(), "item-a"); err == nil {
		t.Fatal("stale outcome echo accepted")
	}
	mode.Store(1)
	if _, err = client.LookupOutcome(context.Background(), "item-a"); err == nil {
		t.Fatal("cached outcome on outage")
	}
}
