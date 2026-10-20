package assurance

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	proof "github.com/hollis-labs/libs/security/webauthn"
)

// Item is an authenticated authoritative Tachyon snapshot. The lookup adapter
// must resolve a server-configured address, not an evidence URL or browser JSON.
type Item struct {
	ID        string          `json:"item_id"`
	Revision  int64           `json:"revision"`
	Status    string          `json:"status"`
	ExpiresAt time.Time       `json:"expires_at"`
	Operation json.RawMessage `json:"operation"`
	Digest    string          `json:"op_digest"`
}
type ItemLookup interface {
	LookupItem(context.Context, string) (Item, error)
}

// Session is returned only by the account's current authenticated session
// lookup. Browser and CLI handoffs use the same stable Subject. Binding is a
// server-side session hash and is never returned in public metadata.
type Session struct {
	Subject, Binding string
	ExpiresAt        time.Time
}
type SessionLookup interface {
	LookupSession(context.Context, string) (Session, error)
}
type Config struct {
	Authority, RPID    string
	Origins, Audiences []string
}
type Service struct {
	db         *sql.DB
	store      *retainedStore
	party      *proof.Party
	ceremonies *proof.Service
	cfg        Config
	items      ItemLookup
	sessions   SessionLookup
	now        func() time.Time
}

func New(ctx context.Context, db *sql.DB, cfg Config, items ItemLookup, sessions SessionLookup, now func() time.Time) (*Service, error) {
	if db == nil || !validID(cfg.Authority) || cfg.RPID == "" || len(cfg.Origins) == 0 || len(cfg.Audiences) == 0 || items == nil || sessions == nil {
		return nil, ErrUnavailable
	}
	cfg.Origins = append([]string(nil), cfg.Origins...)
	cfg.Audiences = append([]string(nil), cfg.Audiences...)
	sort.Strings(cfg.Origins)
	sort.Strings(cfg.Audiences)
	for i, o := range cfg.Origins {
		u, e := url.Parse(o)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.String() != o || (u.Hostname() != cfg.RPID && !strings.HasSuffix(u.Hostname(), "."+cfg.RPID)) || (i > 0 && o == cfg.Origins[i-1]) {
			return nil, ErrInvalid
		}
	}
	for i, a := range cfg.Audiences {
		if !validID(a) || a == "cerberus" || (i > 0 && a == cfg.Audiences[i-1]) {
			return nil, ErrInvalid
		}
	}
	party, e := proof.NewParty(cfg.RPID, "Basalisk", cfg.Origins)
	if e != nil {
		return nil, e
	}
	st, e := newStore(ctx, db, cfg.Authority)
	if e != nil {
		return nil, e
	}
	// An RP/origin change is a reviewed trust migration, not silent key reuse.
	pin, _ := json.Marshal(struct {
		RPID    string
		Origins []string
	}{cfg.RPID, cfg.Origins})
	_, e = db.ExecContext(ctx, `INSERT INTO assurance_trust(authority,profile) VALUES(?,?) ON CONFLICT DO NOTHING`, cfg.Authority, pin)
	if e != nil {
		return nil, e
	}
	var saved []byte
	if e = db.QueryRowContext(ctx, `SELECT profile FROM assurance_trust WHERE authority=?`, cfg.Authority).Scan(&saved); e != nil || !bytes.Equal(saved, pin) {
		return nil, ErrConflict
	}
	if now == nil {
		now = time.Now
	}
	ceremonies, e := proof.NewService(party, st, now)
	if e != nil {
		return nil, e
	}
	return &Service{db, st, party, ceremonies, cfg, items, sessions, now}, nil
}
func keyRef(id []byte) string { h := sha256.Sum256(id); return "key_" + hex.EncodeToString(h[:]) }
func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
func (s *Service) session(ctx context.Context, handle string) (Session, error) {
	u, e := s.sessions.LookupSession(ctx, handle)
	if e != nil || !validID(u.Subject) || u.Binding == "" || !s.now().Before(u.ExpiresAt) {
		return Session{}, ErrDenied
	}
	return u, nil
}
func (s *Service) activeUser(ctx context.Context, subject, key string) (proof.User, error) {
	var revoked int
	e := s.db.QueryRowContext(ctx, `SELECT 1 FROM assurance_revoked_keys WHERE authority=? AND key_ref=?`, s.cfg.Authority, key).Scan(&revoked)
	if e == nil {
		return proof.User{}, ErrDenied
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return proof.User{}, e
	}
	st, e := s.store.Load(ctx, subject)
	if e != nil {
		return proof.User{}, e
	}
	u := st.User
	u.Credentials = nil
	for _, c := range st.User.Credentials {
		if keyRef(c.ID) == key {
			u.Credentials = append(u.Credentials, c)
		}
	}
	if string(u.ID) != subject || len(u.Credentials) != 1 {
		return proof.User{}, ErrDenied
	}
	return u, nil
}
func (s *Service) item(ctx context.Context, id string, revision int64) (Item, Operation, error) {
	i, e := s.items.LookupItem(ctx, id)
	if e != nil {
		return i, Operation{}, ErrUnavailable
	}
	op, d, e := ParseOperation(i.Operation)
	if e != nil {
		return i, op, e
	}
	end, _ := seconds(op.ExpiresAt)
	if i.ID != id || i.Revision != revision || i.Status != "pending" || i.Digest != d || !s.now().Before(i.ExpiresAt) || !s.now().Before(end) || !contains(s.cfg.Audiences, op.Audience) {
		return i, op, ErrDenied
	}
	return i, op, nil
}

type PrepareCommand struct {
	AttemptID        string `json:"attempt_id"`
	ItemID           string `json:"item_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	KeyRef           string `json:"key_ref"`
}
type Prepared struct {
	CompletionID string          `json:"completion_id"`
	Binding      Binding         `json:"binding"`
	Operation    json.RawMessage `json:"operation"`
	Options      json.RawMessage `json:"options"`
}
type attempt struct {
	Prepared
	Subject, SessionHash, Origin, CeremonyID string
	Revision                                 int64
	ApprovalExpiry                           time.Time
	Revoked                                  bool
}

func (s *Service) loadAttempt(ctx context.Context, id string) (attempt, error) {
	var a attempt
	var binding, op, options []byte
	var expiry int64
	var revoked int
	e := s.db.QueryRowContext(ctx, `SELECT subject,session_hash,interaction_revision,operation,approval_expiry,binding,origin,ceremony_id,options,revoked FROM assurance_attempts WHERE authority=? AND id=?`, s.cfg.Authority, id).Scan(&a.Subject, &a.SessionHash, &a.Revision, &op, &expiry, &binding, &a.Origin, &a.CeremonyID, &options, &revoked)
	if e != nil {
		return a, e
	}
	if json.Unmarshal(binding, &a.Binding) != nil {
		return a, ErrUnavailable
	}
	a.CompletionID = id
	a.Operation = op
	a.Options = options
	a.ApprovalExpiry = time.Unix(expiry, 0).UTC()
	a.Revoked = revoked != 0
	return a, nil
}

// Prepare persists both the signed binding and browser options before success.
// A crash between the shared begin and retaining options leaves an unusable
// attempt: start an explicit new attempt with a fresh nonce, never reuse challenge.
func (s *Service) Prepare(ctx context.Context, handle, origin string, c PrepareCommand) (Prepared, error) {
	if !validID(c.AttemptID) || !validID(c.ItemID) || c.ExpectedRevision < 1 || !contains(s.cfg.Origins, origin) {
		return Prepared{}, ErrInvalid
	}
	u, e := s.session(ctx, handle)
	if e != nil {
		return Prepared{}, e
	}
	i, op, e := s.item(ctx, c.ItemID, c.ExpectedRevision)
	if e != nil {
		return Prepared{}, e
	}
	selected, e := s.activeUser(ctx, u.Subject, c.KeyRef)
	if e != nil {
		return Prepared{}, e
	}
	old, e := s.loadAttempt(ctx, c.AttemptID)
	if e == nil {
		if old.SessionHash != u.Binding || old.Subject != u.Subject || old.Binding.ItemID != c.ItemID || old.Revision != c.ExpectedRevision || old.Binding.KeyRef != c.KeyRef || old.Origin != origin || old.CeremonyID == "" || len(old.Options) == 0 || old.Revoked || !bytes.Equal(old.Operation, i.Operation) {
			return Prepared{}, ErrConflict
		}
		if e = old.Binding.Validate(op, i.Digest, s.cfg.Authority, u.Subject, c.KeyRef, c.ItemID, op.Audience, s.now(), i.ExpiresAt); e != nil {
			return Prepared{}, e
		}
		return old.Prepared, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return Prepared{}, e
	}
	var recent int
	e = s.db.QueryRowContext(ctx, `SELECT count(*) FROM assurance_attempts WHERE authority=? AND subject=? AND CAST(json_extract(binding,'$.issued_at') AS INTEGER)>?`, s.cfg.Authority, u.Subject, s.now().Add(-time.Minute).Unix()).Scan(&recent)
	if e != nil {
		return Prepared{}, e
	}
	if recent >= 10 {
		return Prepared{}, ErrDenied
	}
	start := s.now().UTC().Truncate(time.Second)
	end := start.Add(5 * time.Minute)
	opEnd, _ := seconds(op.ExpiresAt)
	for _, t := range []time.Time{i.ExpiresAt, opEnd, u.ExpiresAt} {
		if t.Before(end) {
			end = t.Truncate(time.Second)
		}
	}
	nonce := make([]byte, 32)
	if _, e = rand.Read(nonce); e != nil {
		return Prepared{}, e
	}
	b := Binding{EvidenceProfile, s.cfg.Authority, u.Subject, c.KeyRef, op.Audience, i.Digest, op.OperationID, op.Revision, i.ID, "approved", "proof_" + rand.Text(), base64.RawURLEncoding.EncodeToString(nonce), strconv.FormatInt(start.Unix(), 10), strconv.FormatInt(end.Unix(), 10)}
	if e = b.Validate(op, i.Digest, s.cfg.Authority, u.Subject, c.KeyRef, i.ID, op.Audience, s.now(), i.ExpiresAt); e != nil {
		return Prepared{}, e
	}
	raw, _ := json.Marshal(b)
	inserted, e := s.db.ExecContext(ctx, `INSERT INTO assurance_attempts(authority,id,subject,session_hash,item_id,interaction_revision,operation,approval_expiry,binding,origin)
 SELECT ?,?,?,?,?,?,?,?,?,? WHERE (SELECT count(*) FROM assurance_attempts WHERE authority=? AND subject=? AND CAST(json_extract(binding,'$.issued_at') AS INTEGER)>?)<10`, s.cfg.Authority, c.AttemptID, u.Subject, u.Binding, i.ID, i.Revision, []byte(i.Operation), i.ExpiresAt.Unix(), raw, origin, s.cfg.Authority, u.Subject, s.now().Add(-time.Minute).Unix())
	if e != nil {
		return Prepared{}, ErrConflict
	}
	if n, err := inserted.RowsAffected(); err != nil || n != 1 {
		return Prepared{}, ErrDenied
	}
	challenge, _ := b.Challenge()
	id, _, e := s.ceremonies.BeginAssertion(ctx, u.Subject, u.Binding, challenge, origin, end)
	if e != nil {
		return Prepared{}, e
	}
	options, e := s.party.BeginAssertion(selected, challenge, origin)
	if e != nil {
		return Prepared{}, e
	}
	_, e = s.db.ExecContext(ctx, `UPDATE assurance_attempts SET ceremony_id=?,options=? WHERE authority=? AND id=? AND ceremony_id=''`, id, []byte(options), s.cfg.Authority, c.AttemptID)
	if e != nil {
		return Prepared{}, e
	}
	// Restrict browser selection to the signed key. Finish also verifies this.
	return Prepared{c.AttemptID, b, i.Operation, options}, nil
}

type Completion struct {
	ID               string    `json:"completion_id"`
	ItemID           string    `json:"item_id"`
	ExpectedRevision int64     `json:"expected_revision"`
	AuthTime         time.Time `json:"auth_time"`
	Evidence         Evidence  `json:"evidence"`
}

func (s *Service) retained(ctx context.Context, a attempt) (Completion, error) {
	if a.Revoked || a.CeremonyID == "" {
		return Completion{}, ErrDenied
	}
	p, e := s.store.receipt(ctx, a.Subject, a.CeremonyID)
	if e != nil {
		return Completion{}, e
	}
	assertion, e := assertionFromRaw(p.Assertion)
	if e != nil {
		return Completion{}, e
	}
	challenge, _ := a.Binding.Challenge()
	end, _ := seconds(a.Binding.ExpiresAt)
	if !bytes.Equal(challenge, p.Challenge) || keyRef(p.CredentialID) != a.Binding.KeyRef || p.Origin != a.Origin || !p.ExpiresAt.Equal(end) {
		return Completion{}, ErrDenied
	}
	return Completion{a.CompletionID, a.Binding.ItemID, a.Revision, p.VerifiedAt, Evidence{a.Binding, assertion}}, nil
}

// Finish never returns an uncommitted proof. Exact retries recover the original
// receipt; a different assertion cannot overwrite it or advance counters twice.
func (s *Service) Finish(ctx context.Context, handle, origin, id string, raw json.RawMessage) (Completion, error) {
	u, e := s.session(ctx, handle)
	if e != nil {
		return Completion{}, e
	}
	a, e := s.loadAttempt(ctx, id)
	if e != nil {
		return Completion{}, ErrDenied
	}
	if a.Subject != u.Subject || a.SessionHash != u.Binding || a.Origin != origin || a.Revoked {
		return Completion{}, ErrDenied
	}
	assertion, e := assertionFromRaw(raw)
	if e != nil {
		return Completion{}, e
	}
	uKey, e := s.activeUser(ctx, u.Subject, a.Binding.KeyRef)
	if e != nil {
		return Completion{}, e
	}
	if base64.RawURLEncoding.EncodeToString(uKey.Credentials[0].ID) != assertion.CredentialID {
		return Completion{}, ErrDenied
	}
	old, e := s.retained(ctx, a)
	if e == nil {
		if old.Evidence.Assertion != assertion {
			return Completion{}, ErrConflict
		}
		return s.LookupCompletion(ctx, id)
	}
	if !errors.Is(e, ErrDenied) {
		return Completion{}, e
	}
	i, op, e := s.item(ctx, a.Binding.ItemID, a.Revision)
	if e != nil {
		return Completion{}, e
	}
	if !bytes.Equal(i.Operation, a.Operation) {
		return Completion{}, ErrDenied
	}
	if e = a.Binding.Validate(op, i.Digest, s.cfg.Authority, u.Subject, a.Binding.KeyRef, i.ID, op.Audience, s.now(), i.ExpiresAt); e != nil {
		return Completion{}, e
	}
	_, e = s.ceremonies.FinishAssertion(ctx, u.Subject, u.Binding, a.CeremonyID, raw)
	if e != nil {
		// A concurrent identical finish may have committed. Recovery reads that
		// receipt; it never repeats FinishAssertion or counter advancement.
		old, re := s.retained(ctx, a)
		if re != nil || old.Evidence.Assertion != assertion {
			return Completion{}, e
		}
	}
	return s.LookupCompletion(ctx, id)
}

// LookupCompletion is a current local status read, not a cached-good decision.
// It does not call Tachyon (which may be calling this method to decide). The
// consumer must independently check its authoritative item and execution state.
func (s *Service) LookupCompletion(ctx context.Context, id string) (Completion, error) {
	a, e := s.loadAttempt(ctx, id)
	if e != nil {
		return Completion{}, ErrDenied
	}
	c, e := s.retained(ctx, a)
	if e != nil {
		return Completion{}, e
	}
	u, e := s.activeUser(ctx, a.Subject, a.Binding.KeyRef)
	if e != nil {
		return Completion{}, e
	}
	// Logout/password change revokes eligibility, including retained receipts.
	session, e := s.session(ctx, a.SessionHash)
	if e != nil || session.Subject != a.Subject || session.Binding != a.SessionHash {
		return Completion{}, ErrDenied
	}
	raw, _ := json.Marshal(c.Evidence)
	if _, e = VerifyEvidence(s.party, u, raw, a.Binding, a.Operation, a.Origin, s.now(), a.ApprovalExpiry, true); e != nil {
		return Completion{}, e
	}
	return c, nil
}

// RevokeKey is a host-only lifecycle primitive, never a transport capability.
// Tombstones are permanent: reenrolling the same ID cannot revive old evidence.
func (s *Service) RevokeKey(ctx context.Context, subject, key string) error {
	if _, e := s.activeUser(ctx, subject, key); e != nil {
		return e
	}
	_, e := s.db.ExecContext(ctx, `INSERT INTO assurance_revoked_keys(authority,key_ref,revoked_at) VALUES(?,?,?) ON CONFLICT DO NOTHING`, s.cfg.Authority, key, s.now().Unix())
	return e
}
func (s *Service) RevokeCompletion(ctx context.Context, id string) error {
	r, e := s.db.ExecContext(ctx, `UPDATE assurance_attempts SET revoked=1 WHERE authority=? AND id=?`, s.cfg.Authority, id)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrDenied
	}
	return nil
}

// Cancel abandons this owner's ceremony/completion only. It is not a Tachyon
// denial or withdrawal; those remain authoritative inbox/producer operations.
func (s *Service) Cancel(ctx context.Context, handle, id string) error {
	u, e := s.session(ctx, handle)
	if e != nil {
		return e
	}
	a, e := s.loadAttempt(ctx, id)
	if e != nil || a.Subject != u.Subject || a.SessionHash != u.Binding {
		return ErrDenied
	}
	return s.RevokeCompletion(ctx, id)
}
