package assurance

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	wa "github.com/go-webauthn/webauthn/webauthn"
	proof "github.com/hollis-labs/libs/security/webauthn"
)

// The source fixture includes consumer policy scenarios as well as signatures.
// This harness feeds every assertion to the actual shared verifier. External
// requester/continuation and HITL descriptor policies are asserted separately
// below as fixture preconditions, not represented as implemented Tether behavior.
func TestSharedInteroperabilityVectors(t *testing.T) {
	data, e := os.ReadFile("testdata/operation-proof-v1.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		PEM             string `json:"public_key_pem"`
		OperationDigest string `json:"operation_digest"`
		Challenge       string `json:"challenge_base64url"`
		Cases           []struct {
			Name      string          `json:"name"`
			Expected  string          `json:"expected"`
			Operation json.RawMessage `json:"operation"`
			Trusted   struct {
				Binding             Binding `json:"binding"`
				Now                 string  `json:"now"`
				Origin              string  `json:"origin"`
				RPID                string  `json:"rp_id"`
				CredentialID        string  `json:"credential_id"`
				Counter             string  `json:"previous_counter"`
				CheckCounter        bool    `json:"check_counter"`
				RequesterHuman      string  `json:"requester_human"`
				RequesterAuthorized bool    `json:"requester_authorized"`
				OperationCurrent    bool    `json:"operation_current"`
				KeyRevoked          bool    `json:"key_revoked"`
				TrustFresh          bool    `json:"trust_fresh"`
				ProofConsumed       bool    `json:"proof_consumed"`
			} `json:"trusted_state"`
			Response struct {
				Decision string   `json:"decision"`
				Evidence Evidence `json:"assurance_evidence"`
			} `json:"response"`
			Participant struct {
				Authority string `json:"authority"`
				Human     string `json:"principal_ref"`
				Proof     struct {
					Scheme string `json:"scheme"`
					Key    string `json:"key_ref"`
					Binds  []struct {
						Name  string `json:"name"`
						Value string `json:"value"`
					} `json:"binds"`
				} `json:"proof"`
			} `json:"participant"`
		} `json:"cases"`
	}
	if e = json.Unmarshal(data, &fixture); e != nil {
		t.Fatal(e)
	}
	block, _ := pem.Decode([]byte(fixture.PEM))
	key, e := x509.ParsePKIXPublicKey(block.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	pub := key.(*ecdsa.PublicKey)
	point, e := pub.Bytes()
	if e != nil {
		t.Fatal(e)
	}
	cose, e := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: point[1:33], -3: point[33:65]})
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			st := tc.Trusted
			party, e := proof.NewParty(st.RPID, "Synthetic", []string{st.Origin})
			if e != nil {
				t.Fatal(e)
			}
			id, _ := base64.RawURLEncoding.DecodeString(st.CredentialID)
			count, _ := strconv.ParseUint(st.Counter, 10, 32)
			user := proof.User{ID: []byte(st.Binding.HumanSubject), Name: "synthetic", DisplayName: "Synthetic", Credentials: []wa.Credential{{ID: id, PublicKey: cose, Authenticator: wa.Authenticator{SignCount: uint32(count)}}}}
			raw, _ := json.Marshal(tc.Response.Evidence)
			now, _ := seconds(st.Now)
			end := time.Unix(1900000600, 0)
			_, verifyErr := VerifyEvidence(party, user, raw, st.Binding, tc.Operation, st.Origin, now, end, !st.CheckCounter)
			externalDenied := st.ProofConsumed || st.KeyRevoked || !st.TrustFresh || !st.RequesterAuthorized || !st.OperationCurrent || st.RequesterHuman == st.Binding.HumanSubject || tc.Response.Decision != "approved" || tc.Participant.Authority != st.Binding.Authority || tc.Participant.Human != st.Binding.HumanSubject || tc.Participant.Proof.Scheme != EvidenceProfile || tc.Participant.Proof.Key != st.Binding.KeyRef
			binds := map[string]string{}
			for _, b := range tc.Participant.Proof.Binds {
				binds[b.Name] = b.Value
			}
			for k, v := range map[string]string{"op_digest": st.Binding.OpDigest, "item_id": st.Binding.ItemID, "proof_id": st.Binding.ProofID, "audience": st.Binding.Audience} {
				if binds[k] != v {
					externalDenied = true
				}
			}
			accepted := verifyErr == nil && !externalDenied
			if accepted != (tc.Expected == "accept") {
				t.Fatalf("accepted=%v expected=%s verifier=%v external_policy_denied=%v", accepted, tc.Expected, verifyErr, externalDenied)
			}
			if tc.Name == "valid" {
				_, digest, e := ParseOperation(tc.Operation)
				if e != nil || digest != fixture.OperationDigest {
					t.Fatal("operation canonicalization mismatch", e)
				}
				challenge, _ := st.Binding.Challenge()
				if base64.RawURLEncoding.EncodeToString(challenge) != fixture.Challenge {
					t.Fatal("binding canonicalization mismatch")
				}
			}
		})
	}
}
func TestProfileParsingRejectsAmbiguity(t *testing.T) {
	f := newFixture(t)
	valid := string(f.items.item.Operation)
	for _, bad := range []string{
		strings.Replace(valid, `"profile":`, `"profile":"duplicate","profile":`, 1),
		strings.Replace(valid, `"profile":`, `"Profile":`, 1),
		strings.Replace(valid, `"revision":"1"`, `"revision":1`, 1),
		strings.Replace(valid, `"revision":"1"`, `"revision":"01"`, 1),
		strings.Replace(valid, `"purpose":"approval producer"`, `"purpose":"\ud800"`, 1),
		strings.Replace(valid, `"purpose":"approval producer"`, `"purpose":null`, 1),
		strings.Replace(valid, `"action":"create"`, `"action":"rotate"`, 1),
		strings.Replace(valid, `"scopes":["tachyon.approvals_read"]`, `"scopes":["z","a"]`, 1),
		strings.Replace(valid, `"scopes":["tachyon.approvals_read"]`, `"scopes":["a","a"]`, 1),
	} {
		if _, _, e := ParseOperation([]byte(bad)); e == nil {
			t.Fatalf("accepted ambiguous profile: %s", bad)
		}
	}
	unicode := strings.Replace(valid, "approval producer", "<é> 😀 \u2028", 1)
	if _, _, e := ParseOperation([]byte(unicode)); e != nil {
		t.Fatal("valid Unicode rejected", e)
	}
}
