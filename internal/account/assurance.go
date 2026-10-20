package account

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/hollis-labs/basalisk/internal/assurance"
)

// LookupSession accepts only a server-side session hash. HTTP authentication
// hashes its cookie before calling this; CLI browser handoffs use the same path.
// No device, companion, username or session hash becomes the human subject.
func (s *Store) LookupSession(ctx context.Context, hash string) (assurance.Session, error) {
	var subject string
	var expiry int64
	err := s.db.QueryRowContext(ctx, `SELECT i.user_id,s.expires FROM sessions s CROSS JOIN activity_identity i CROSS JOIN owner o WHERE s.hash=? AND s.expires>? AND i.singleton=1 AND o.id=1`, hash, time.Now().Unix()).Scan(&subject, &expiry)
	if err != nil {
		return assurance.Session{}, err
	}
	return assurance.Session{Subject: subject, Binding: hash, ExpiresAt: time.Unix(expiry, 0)}, nil
}

// ConfigureAssurance is called at startup only after the host has installed an
// authenticated authoritative item adapter. It cannot choose a new authority:
// the persisted Basalisk installation ID is the trust pin for this database.
func (s *Store) ConfigureAssurance(ctx context.Context, cfg assurance.Config, items assurance.ItemLookup) (*assurance.Service, error) {
	var authority string
	if err := s.db.QueryRowContext(ctx, `SELECT installation_id FROM activity_identity WHERE singleton=1`).Scan(&authority); err != nil {
		return nil, err
	}
	if cfg.Authority != authority {
		return nil, assurance.ErrDenied
	}
	service, err := assurance.New(ctx, s.db, cfg, items, s, nil)
	if err != nil {
		return nil, err
	}
	s.assurance = service
	return service, nil
}

// ConfigureAssuranceHost installs all required adapters at startup. An absent
// configuration leaves assurance unavailable. It never enrolls credentials or
// keys; configured missing/rotated private files fail closed at request time.
func (s *Store) ConfigureAssuranceHost(ctx context.Context, cfg *assurance.HostConfig, client *http.Client) (http.Handler, error) {
	if cfg == nil {
		return nil, nil
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	current, err := assurance.NewCurrentTrustClient(cfg.CurrentTrust, client)
	if err != nil {
		return nil, err
	}
	authn, err := assurance.NewTetherStatusAuth(assurance.TetherStatusConfig{Issuer: cfg.Authentication.Issuer, JWKSURL: cfg.Authentication.JWKSURL, Callers: cfg.Authentication.Callers, Current: current, Client: client})
	if err != nil {
		return nil, err
	}
	exchange, err := assurance.NewWorkloadExchange(cfg.Tachyon.Exchange, client)
	if err != nil {
		return nil, err
	}
	tachyon, err := assurance.NewTachyonClient(cfg.Tachyon.Origin, exchange, client)
	if err != nil {
		return nil, err
	}
	service, err := s.ConfigureAssurance(ctx, assurance.Config{Authority: cfg.Authority, RPID: cfg.RPID, Origins: []string{cfg.Origin}, Audiences: cfg.Audiences}, tachyon)
	if err != nil {
		return nil, err
	}
	s.assuranceTachyon = tachyon
	return service.StatusHandler(authn), nil
}

func (a *API) deliverAssurance(w http.ResponseWriter, r *http.Request) {
	if a.store.assurance == nil || a.store.assuranceTachyon == nil {
		fail(w, 503, "Operation assurance is not configured.")
		return
	}
	var cmd struct {
		CompletionID string `json:"completion_id"`
	}
	if !decode(w, r, &cmd) {
		return
	}
	c, _ := r.Cookie(sessionCookie)
	out, err := a.store.assurance.Deliver(r.Context(), digest(c.Value), cmd.CompletionID, a.store.assuranceTachyon)
	if err != nil {
		assuranceFailure(w, err)
		return
	}
	reply(w, 200, map[string]any{"outcome": out})
}
func (a *API) recoverAssurance(w http.ResponseWriter, r *http.Request) {
	if a.store.assurance == nil || a.store.assuranceTachyon == nil {
		fail(w, 503, "Operation assurance is not configured.")
		return
	}
	var cmd struct {
		CompletionID string `json:"completion_id"`
	}
	if !decode(w, r, &cmd) {
		return
	}
	c, _ := r.Cookie(sessionCookie)
	out, err := a.store.assurance.RecoverOutcome(r.Context(), digest(c.Value), cmd.CompletionID, a.store.assuranceTachyon)
	if err != nil {
		assuranceFailure(w, err)
		return
	}
	reply(w, 200, out)
}
func (a *API) humanIdentity(w http.ResponseWriter, r *http.Request) {
	var authority, subject string
	err := a.store.db.QueryRowContext(r.Context(), `SELECT installation_id,user_id FROM activity_identity WHERE singleton=1`).Scan(&authority, &subject)
	if err != nil {
		fail(w, 503, "Identity unavailable.")
		return
	}
	reply(w, 200, map[string]string{"authority": authority, "human_subject": subject, "assurance": "password-session"})
}
func (a *API) prepareAssurance(w http.ResponseWriter, r *http.Request) {
	if a.store.assurance == nil {
		fail(w, 503, "Operation assurance is not configured.")
		return
	}
	var cmd assurance.PrepareCommand
	if !decode(w, r, &cmd) {
		return
	}
	c, _ := r.Cookie(sessionCookie)
	out, err := a.store.assurance.Prepare(r.Context(), digest(c.Value), r.Header.Get("Origin"), cmd)
	if err != nil {
		assuranceFailure(w, err)
		return
	}
	reply(w, 200, out)
}
func (a *API) finishAssurance(w http.ResponseWriter, r *http.Request) {
	if a.store.assurance == nil {
		fail(w, 503, "Operation assurance is not configured.")
		return
	}
	var cmd struct {
		CompletionID string          `json:"completion_id"`
		Assertion    json.RawMessage `json:"assertion"`
	}
	if !decodeLimit(w, r, &cmd, 40<<10) {
		return
	}
	c, _ := r.Cookie(sessionCookie)
	out, err := a.store.assurance.Finish(r.Context(), digest(c.Value), r.Header.Get("Origin"), cmd.CompletionID, cmd.Assertion)
	if err != nil {
		assuranceFailure(w, err)
		return
	}
	// The browser gets a reference and presentation only. The trusted Tachyon
	// adapter must retrieve and verify evidence over authenticated transport.
	reply(w, 200, map[string]any{"completion_id": out.ID, "item_id": out.ItemID, "expected_revision": out.ExpectedRevision, "auth_time": out.AuthTime})
}
func assuranceFailure(w http.ResponseWriter, err error) {
	status := 503
	if errors.Is(err, assurance.ErrInvalid) {
		status = 400
	}
	if errors.Is(err, assurance.ErrDenied) {
		status = 403
	}
	if errors.Is(err, assurance.ErrConflict) {
		status = 409
	}
	fail(w, status, "Operation assurance could not be completed.")
}

func (a *API) cancelAssurance(w http.ResponseWriter, r *http.Request) {
	if a.store.assurance == nil {
		fail(w, 503, "Operation assurance is not configured.")
		return
	}
	var cmd struct {
		CompletionID string `json:"completion_id"`
	}
	if !decode(w, r, &cmd) {
		return
	}
	c, _ := r.Cookie(sessionCookie)
	if err := a.store.assurance.Cancel(r.Context(), digest(c.Value), cmd.CompletionID); err != nil {
		assuranceFailure(w, err)
		return
	}
	reply(w, 200, map[string]bool{"canceled": true})
}
