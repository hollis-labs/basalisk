// Package assurance implements Basalisk's operation-bound human proof profile.
// Transport identity, inbox decisions and execution remain separate boundaries.
package assurance

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	jcs "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	proof "github.com/hollis-labs/libs/security/webauthn"
)

const OperationProfile = "privileged-operation-v1-draft"
const EvidenceProfile = "operation-webauthn-v1-draft"

var (
	ErrInvalid     = errors.New("invalid assurance profile")
	ErrDenied      = errors.New("assurance refused")
	ErrUnavailable = errors.New("assurance dependency unavailable")
	ErrConflict    = errors.New("assurance state changed")
)

type Requester struct {
	Authority string `json:"authority"`
	Subject   string `json:"subject"`
}
type Registration struct {
	Type                string   `json:"type"`
	Action              string   `json:"action"`
	PrincipalID         string   `json:"principal_id"`
	OwnerID             string   `json:"owner_id"`
	Purpose             string   `json:"purpose"`
	CredentialExpiresAt string   `json:"credential_expires_at"`
	OverlapSeconds      string   `json:"overlap_seconds"`
	DestinationID       string   `json:"destination_id"`
	DestinationRevision string   `json:"destination_revision"`
	Scopes              []string `json:"scopes"`
}
type Operation struct {
	Profile              string         `json:"profile"`
	OperationID          string         `json:"operation_id"`
	Revision             string         `json:"revision"`
	Requester            Requester      `json:"requester"`
	Audience             string         `json:"audience"`
	Class                string         `json:"class"`
	ExpiresAt            string         `json:"expires_at"`
	AuthorizationDetails []Registration `json:"authorization_details"`
}
type Binding struct {
	Profile           string `json:"profile"`
	Authority         string `json:"authority"`
	HumanSubject      string `json:"human_subject"`
	KeyRef            string `json:"key_ref"`
	Audience          string `json:"audience"`
	OpDigest          string `json:"op_digest"`
	OperationID       string `json:"operation_id"`
	OperationRevision string `json:"operation_revision"`
	ItemID            string `json:"item_id"`
	Decision          string `json:"decision"`
	ProofID           string `json:"proof_id"`
	Nonce             string `json:"nonce"`
	IssuedAt          string `json:"issued_at"`
	ExpiresAt         string `json:"expires_at"`
}
type Assertion struct {
	CredentialID      string `json:"credential_id"`
	ClientDataJSON    string `json:"client_data_json"`
	AuthenticatorData string `json:"authenticator_data"`
	Signature         string `json:"signature"`
}
type Evidence struct {
	Binding   Binding   `json:"binding"`
	Assertion Assertion `json:"assertion"`
}

func validID(s string) bool {
	return s != "" && len(s) <= 256 && utf8.ValidString(s) && strings.IndexFunc(s, unicode.IsControl) < 0
}
func decimal(s string, positive bool) (int64, error) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, ErrInvalid
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, ErrInvalid
		}
	}
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil || (positive && n == 0) {
		return 0, ErrInvalid
	}
	return n, nil
}
func seconds(s string) (time.Time, error) {
	n, e := decimal(s, false)
	if e != nil || n > 253402300799 {
		return time.Time{}, ErrInvalid
	}
	return time.Unix(n, 0).UTC(), nil
}
func b64(s string) ([]byte, error) {
	b, e := base64.RawURLEncoding.Strict().DecodeString(s)
	if e != nil || len(b) == 0 || base64.RawURLEncoding.EncodeToString(b) != s {
		return nil, ErrInvalid
	}
	return b, nil
}

// Exact JSON rejects aliases, missing/unknown fields, duplicate names, invalid
// Unicode, nulls and type coercions before any signature input is accepted.
// This profile contains only strings, objects and arrays; JCS handles their
// complete Unicode representation, including UTF-16 property ordering.
func exact(raw []byte, limit int, out any) error {
	if len(raw) == 0 || len(raw) > limit || !utf8.Valid(raw) {
		return ErrInvalid
	}
	if boundedJSON(raw) != nil {
		return ErrInvalid
	}
	canonical, e := jcs.Transform(raw)
	if e != nil {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	encoded, e := json.Marshal(out)
	if e != nil {
		return ErrInvalid
	}
	want, e := jcs.Transform(encoded)
	if e != nil || !bytes.Equal(canonical, want) {
		return ErrInvalid
	}
	// The canonicalizer rejects duplicates. Reject null anywhere separately.
	d = json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	for {
		tok, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil || tok == nil {
			return ErrInvalid
		}
	}
	return nil
}

func boundedJSON(raw []byte) error {
	if !utf8.Valid(raw) {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	depth := 0
	for {
		t, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return ErrInvalid
		}
		if v, ok := t.(json.Delim); ok {
			if v == '{' || v == '[' {
				depth++
			} else {
				depth--
			}
			if depth > 16 {
				return ErrInvalid
			}
		}
	}
	if depth != 0 {
		return ErrInvalid
	}
	// Also reject duplicate names and lone surrogates in extensible carriers,
	// whose field set cannot be compared with a closed profile struct.
	if _, err := jcs.Transform(raw); err != nil {
		return ErrInvalid
	}
	return nil
}
func ParseOperation(raw []byte) (Operation, string, error) {
	var op Operation
	if exact(raw, 16<<10, &op) != nil {
		return op, "", ErrInvalid
	}
	if op.Profile != OperationProfile || op.Class != "high" || !validID(op.OperationID) || !validID(op.Audience) || !validID(op.Requester.Authority) || !validID(op.Requester.Subject) || len(op.AuthorizationDetails) != 1 {
		return op, "", ErrInvalid
	}
	if _, e := decimal(op.Revision, true); e != nil {
		return op, "", e
	}
	expires, e := seconds(op.ExpiresAt)
	if e != nil {
		return op, "", e
	}
	a := op.AuthorizationDetails[0]
	if a.Type != "credential_registration" || a.Action != "create" || a.OverlapSeconds != "0" || !validID(a.PrincipalID) || !validID(a.OwnerID) || !validID(a.DestinationID) || a.Purpose == "" || len(a.Purpose) > 1024 || !utf8.ValidString(a.Purpose) || strings.IndexFunc(a.Purpose, unicode.IsControl) >= 0 {
		return op, "", ErrInvalid
	}
	if _, e = decimal(a.DestinationRevision, true); e != nil {
		return op, "", e
	}
	credentialExpiry, e := seconds(a.CredentialExpiresAt)
	if e != nil || !credentialExpiry.After(expires) {
		return op, "", ErrInvalid
	}
	if len(a.Scopes) < 1 || len(a.Scopes) > 32 || !sort.StringsAreSorted(a.Scopes) {
		return op, "", ErrInvalid
	}
	for i, s := range a.Scopes {
		if !validID(s) || len(s) > 128 || (i > 0 && s == a.Scopes[i-1]) {
			return op, "", ErrInvalid
		}
	}
	canonical, _ := jcs.Transform(raw)
	h := sha256.Sum256(canonical)
	return op, "sha256:" + hex.EncodeToString(h[:]), nil
}
func (b Binding) Challenge() ([]byte, error) {
	raw, e := json.Marshal(b)
	if e != nil {
		return nil, e
	}
	canonical, e := jcs.Transform(raw)
	if e != nil {
		return nil, e
	}
	h := sha256.New()
	h.Write([]byte("hollis operation proof v1\x00"))
	h.Write(canonical)
	return h.Sum(nil), nil
}
func (b Binding) Validate(op Operation, digest, authority, human, key, item, audience string, now, approvalExpiry time.Time) error {
	if b.Profile != EvidenceProfile || b.Decision != "approved" || b.Authority != authority || b.HumanSubject != human || b.KeyRef != key || b.ItemID != item || b.Audience != audience || op.Audience != audience || b.OpDigest != digest || b.OperationID != op.OperationID || b.OperationRevision != op.Revision {
		return ErrDenied
	}
	for _, s := range []string{b.Authority, b.HumanSubject, b.KeyRef, b.ItemID, b.Audience, b.ProofID} {
		if !validID(s) {
			return ErrInvalid
		}
	}
	nonce, e := b64(b.Nonce)
	if e != nil || len(nonce) != 32 {
		return ErrInvalid
	}
	start, e := seconds(b.IssuedAt)
	if e != nil {
		return e
	}
	end, e := seconds(b.ExpiresAt)
	if e != nil {
		return e
	}
	opEnd, e := seconds(op.ExpiresAt)
	if e != nil {
		return e
	}
	if now.Before(start) || !now.Before(end) || !end.After(start) || end.Sub(start) > 5*time.Minute || end.After(opEnd) || approvalExpiry.IsZero() || end.After(approvalExpiry) {
		return ErrDenied
	}
	if op.Requester.Authority == b.Authority && op.Requester.Subject == b.HumanSubject {
		return ErrDenied
	}
	return nil
}

// Raw reconstructs a WebAuthn response without changing any signed bytes.
func (a Assertion) Raw() (json.RawMessage, error) {
	for _, s := range []string{a.CredentialID, a.ClientDataJSON, a.AuthenticatorData, a.Signature} {
		if _, e := b64(s); e != nil {
			return nil, e
		}
	}
	cd, _ := b64(a.ClientDataJSON)
	if len(cd) > 16<<10 || boundedJSON(cd) != nil {
		return nil, ErrInvalid
	}
	// go-webauthn uses encoding/json; reject duplicate client-data names first.
	if _, e := jcs.Transform(cd); e != nil {
		return nil, ErrInvalid
	}
	return json.Marshal(map[string]any{"id": a.CredentialID, "rawId": a.CredentialID, "type": "public-key", "response": map[string]string{"clientDataJSON": a.ClientDataJSON, "authenticatorData": a.AuthenticatorData, "signature": a.Signature}, "clientExtensionResults": map[string]any{}})
}
func assertionFromRaw(raw []byte) (Assertion, error) {
	if len(raw) > 32<<10 || boundedJSON(raw) != nil {
		return Assertion{}, ErrInvalid
	}
	if _, e := jcs.Transform(raw); e != nil {
		return Assertion{}, ErrInvalid
	}
	var x struct {
		ID       string `json:"id"`
		RawID    string `json:"rawId"`
		Type     string `json:"type"`
		Response struct {
			Client    string `json:"clientDataJSON"`
			Auth      string `json:"authenticatorData"`
			Signature string `json:"signature"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &x) != nil || x.ID != x.RawID || x.Type != "public-key" {
		return Assertion{}, ErrInvalid
	}
	a := Assertion{x.RawID, x.Response.Client, x.Response.Auth, x.Response.Signature}
	_, e := a.Raw()
	return a, e
}

// VerifyEvidence is for independent consumers after authenticated CURRENT
// status lookup. expected is the retained server binding, not browser input.
// recorded=true rechecks a committed assertion without advancing its counter.
func VerifyEvidence(party *proof.Party, user proof.User, raw []byte, expected Binding, opRaw []byte, origin string, now, approvalExpiry time.Time, recorded bool) (Evidence, error) {
	var ev Evidence
	if party == nil || exact(raw, 32<<10, &ev) != nil || ev.Binding != expected {
		return ev, ErrDenied
	}
	op, digest, e := ParseOperation(opRaw)
	if e != nil {
		return ev, e
	}
	b := ev.Binding
	if e = b.Validate(op, digest, expected.Authority, expected.HumanSubject, expected.KeyRef, expected.ItemID, expected.Audience, now, approvalExpiry); e != nil {
		return ev, e
	}
	if string(user.ID) != b.HumanSubject {
		return ev, ErrDenied
	}
	assertion, e := ev.Assertion.Raw()
	if e != nil {
		return ev, e
	}
	challenge, e := b.Challenge()
	if e != nil {
		return ev, e
	}
	// Copy before resetting counters so revalidation cannot mutate registry state.
	if recorded {
		rawUser, _ := json.Marshal(user)
		_ = json.Unmarshal(rawUser, &user)
		for i := range user.Credentials {
			user.Credentials[i].Authenticator.SignCount = 0
			user.Credentials[i].Authenticator.CloneWarning = false
		}
	}
	cred, e := party.VerifyAssertion(user, assertion, challenge, origin)
	if e != nil || (!recorded && cred.Authenticator.CloneWarning) {
		return ev, ErrDenied
	}
	return ev, nil
}
