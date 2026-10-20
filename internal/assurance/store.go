package assurance

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	proof "github.com/hollis-labs/libs/security/webauthn"
	"github.com/hollis-labs/libs/security/webauthn/sqlitestore"
)

// retainedStore delegates every credential/challenge/counter transaction to the
// supported shared SQLite store. Before a CAS can prune a committed receipt it
// archives that receipt. If archiving fails, pruning cannot commit. If the CAS
// loses a race, the archive still contains only a previously committed receipt.
// Current revocation and ceremony state are checked independently on every read.
// This ordering also covers a crash immediately after FinishAssertion commits.
type retainedStore struct {
	base      *sqlitestore.Store
	db        *sql.DB
	authority string
}

func newStore(ctx context.Context, db *sql.DB, authority string) (*retainedStore, error) {
	base, e := sqlitestore.New(ctx, db, authority)
	if e != nil {
		return nil, e
	}
	_, e = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS assurance_attempts (
 authority TEXT NOT NULL, id TEXT NOT NULL, subject TEXT NOT NULL, session_hash TEXT NOT NULL,
 item_id TEXT NOT NULL, interaction_revision INTEGER NOT NULL, operation BLOB NOT NULL,
 approval_expiry INTEGER NOT NULL, binding BLOB NOT NULL, origin TEXT NOT NULL,
 ceremony_id TEXT NOT NULL DEFAULT '', options BLOB NOT NULL DEFAULT '', revoked INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(authority,id));
 CREATE UNIQUE INDEX IF NOT EXISTS assurance_ceremony ON assurance_attempts(authority,ceremony_id) WHERE ceremony_id!='';
 CREATE TABLE IF NOT EXISTS assurance_receipts (
 authority TEXT NOT NULL, subject TEXT NOT NULL, ceremony_id TEXT NOT NULL, receipt BLOB NOT NULL,
 PRIMARY KEY(authority,subject,ceremony_id));
 CREATE TABLE IF NOT EXISTS assurance_revoked_keys (
 authority TEXT NOT NULL, key_ref TEXT NOT NULL, revoked_at INTEGER NOT NULL,
 PRIMARY KEY(authority,key_ref));
 CREATE TABLE IF NOT EXISTS assurance_trust (
 authority TEXT PRIMARY KEY, profile BLOB NOT NULL);
 CREATE TRIGGER IF NOT EXISTS assurance_receipts_immutable BEFORE UPDATE ON assurance_receipts BEGIN SELECT RAISE(ABORT,'immutable assurance receipt'); END;
 CREATE TRIGGER IF NOT EXISTS assurance_attempts_immutable BEFORE UPDATE ON assurance_attempts
 WHEN OLD.subject!=NEW.subject OR OLD.session_hash!=NEW.session_hash OR OLD.item_id!=NEW.item_id
 OR OLD.interaction_revision!=NEW.interaction_revision OR OLD.operation!=NEW.operation OR OLD.approval_expiry!=NEW.approval_expiry
 OR OLD.binding!=NEW.binding OR OLD.origin!=NEW.origin OR (OLD.ceremony_id!='' AND OLD.ceremony_id!=NEW.ceremony_id)
 OR (length(OLD.options)>0 AND OLD.options!=NEW.options) OR OLD.revoked>NEW.revoked
 BEGIN SELECT RAISE(ABORT,'immutable assurance attempt'); END;`)
	if e != nil {
		return nil, e
	}
	return &retainedStore{base, db, authority}, nil
}
func (s *retainedStore) Load(ctx context.Context, subject string) (proof.Snapshot, error) {
	return s.base.Load(ctx, subject)
}
func (s *retainedStore) archive(ctx context.Context, subject, id string, p proof.Proof) error {
	raw, e := json.Marshal(p)
	if e != nil {
		return e
	}
	_, e = s.db.ExecContext(ctx, `INSERT INTO assurance_receipts(authority,subject,ceremony_id,receipt) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, s.authority, subject, id, raw)
	if e != nil {
		return e
	}
	var old []byte
	e = s.db.QueryRowContext(ctx, `SELECT receipt FROM assurance_receipts WHERE authority=? AND subject=? AND ceremony_id=?`, s.authority, subject, id).Scan(&old)
	if e == nil && !bytes.Equal(old, raw) {
		return ErrConflict
	}
	return e
}
func (s *retainedStore) CompareAndSwap(ctx context.Context, subject string, expected int64, next proof.Snapshot) error {
	old, e := s.base.Load(ctx, subject)
	if e != nil {
		return e
	}
	if old.Version != expected {
		return proof.ErrConflict
	}
	for id, p := range old.Proofs {
		if n, ok := next.Proofs[id]; ok {
			a, _ := json.Marshal(p)
			b, _ := json.Marshal(n)
			if !bytes.Equal(a, b) {
				return ErrConflict
			}
		}
		if e = s.archive(ctx, subject, id, p); e != nil {
			return e
		}
	}
	return s.base.CompareAndSwap(ctx, subject, expected, next)
}
func (s *retainedStore) receipt(ctx context.Context, subject, id string) (proof.Proof, error) {
	st, e := s.base.Load(ctx, subject)
	if e != nil {
		return proof.Proof{}, e
	}
	if p, ok := st.Proofs[id]; ok {
		return p, nil
	}
	var raw []byte
	e = s.db.QueryRowContext(ctx, `SELECT receipt FROM assurance_receipts WHERE authority=? AND subject=? AND ceremony_id=?`, s.authority, subject, id).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return proof.Proof{}, ErrDenied
	}
	if e != nil {
		return proof.Proof{}, e
	}
	var p proof.Proof
	e = json.Unmarshal(raw, &p)
	return p, e
}
