package assurance

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	proof "github.com/hollis-labs/libs/security/webauthn"
)

// StatusCaller is transport authority only. Audiences is the caller's exact
// server-configured executing-audience allowlist, never token-supplied grants.
type StatusCaller struct {
	Subject   string
	Audiences []string
}
type StatusAuthenticator interface {
	AuthenticateStatus(context.Context, *http.Request) (StatusCaller, error)
}
type CurrentStatus struct {
	QueryID    string     `json:"query_id"`
	Authority  string     `json:"authority"`
	RPID       string     `json:"rp_id"`
	Origin     string     `json:"origin"`
	CheckedAt  time.Time  `json:"checked_at"`
	Completion Completion `json:"completion"`
	Credential proof.User `json:"credential"`
}

// StatusHandler is a read-only completion/ceremony/key lookup. Authentication
// is required on EVERY request; cookies and opaque references grant no access.
// There are no credential-management or enrollment routes on this handler.
func (s *Service) StatusHandler(authn StatusAuthenticator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		refuse := func(status int) {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":"assurance_status_unavailable"}`)
		}
		if authn == nil {
			refuse(503)
			return
		}
		caller, e := authn.AuthenticateStatus(r.Context(), r)
		if e != nil || !validID(caller.Subject) {
			if errors.Is(e, ErrUnavailable) {
				refuse(503)
			} else {
				refuse(401)
			}
			return
		}
		if r.Method != "POST" || r.URL.Path != "/assurance/v1/status" {
			refuse(404)
			return
		}
		raw, e := io.ReadAll(io.LimitReader(r.Body, 1025))
		if e != nil || len(raw) > 1024 {
			refuse(400)
			return
		}
		var cmd struct {
			CompletionID string `json:"completion_id"`
			QueryID      string `json:"query_id"`
		}
		if exact(raw, 1024, &cmd) != nil || !validID(cmd.CompletionID) || !validID(cmd.QueryID) {
			refuse(400)
			return
		}
		c, e := s.LookupCompletion(r.Context(), cmd.CompletionID)
		if e != nil {
			refuse(404)
			return
		}
		if !contains(caller.Audiences, c.Evidence.Binding.Audience) {
			refuse(404)
			return
		}
		u, e := s.activeUser(r.Context(), c.Evidence.Binding.HumanSubject, c.Evidence.Binding.KeyRef)
		if e != nil {
			refuse(503)
			return
		}
		a, e := s.loadAttempt(r.Context(), cmd.CompletionID)
		if e != nil {
			refuse(503)
			return
		}
		// Public key material only, selected for this completion. No registry
		// snapshot, session identifier, unrelated credentials or pending challenges.
		_ = json.NewEncoder(w).Encode(CurrentStatus{cmd.QueryID, s.cfg.Authority, s.cfg.RPID, a.Origin, s.now().UTC(), c, u})
	})
}

type CompletionLookup struct {
	Authority, Audience, CompletionID, ItemID string
	ExpectedRevision                          int64
	RequestJSON                               []byte
}

// StatusLookup must make an authenticated current read with no cached-good
// fallback. A concrete network authenticator is deliberately host-supplied.
type StatusLookup interface {
	LookupStatus(context.Context, string) (CurrentStatus, error)
}
type CompletionVerifier struct {
	Authority, RPID string
	Origins         []string
	Lookup          StatusLookup
	Now             func() time.Time
}

// VerifyCompletion exercises the independent consumer boundary locally. This
// internal package is not an importable Tachyon/Tether SDK. Those applications
// must install their own adapters to the documented HTTP/profile contract and
// shared WebAuthn module, then apply requester authorization and continuation.
func (v CompletionVerifier) VerifyCompletion(ctx context.Context, in CompletionLookup) (json.RawMessage, error) {
	if v.Lookup == nil || v.Authority == "" || in.Authority != v.Authority || !validID(in.ItemID) || !validID(in.Audience) || in.ExpectedRevision < 1 {
		return nil, ErrDenied
	}
	if len(in.RequestJSON) > 64<<10 || boundedJSON(in.RequestJSON) != nil {
		return nil, ErrInvalid
	}
	// Enqueue is an extensible carrier; only the operation is an exact profile.
	var req struct {
		Kind      string          `json:"kind"`
		ExpiresAt time.Time       `json:"expires_at"`
		Class     string          `json:"class"`
		Digest    string          `json:"op_digest"`
		Operation json.RawMessage `json:"operation"`
	}
	if json.Unmarshal(in.RequestJSON, &req) != nil || req.Kind != "approval" {
		return nil, ErrInvalid
	}
	if req.Class != "high" {
		return nil, ErrInvalid
	}
	op, d, e := ParseOperation(req.Operation)
	if e != nil || d != req.Digest || op.Audience != in.Audience {
		return nil, ErrDenied
	}
	status, e := v.Lookup.LookupStatus(ctx, in.CompletionID)
	if e != nil {
		return nil, ErrUnavailable
	}
	if status.Authority != v.Authority || status.RPID != v.RPID || !contains(v.Origins, status.Origin) || status.Completion.ID != in.CompletionID || status.Completion.ItemID != in.ItemID || status.Completion.ExpectedRevision != in.ExpectedRevision {
		return nil, ErrDenied
	}
	now := time.Now()
	if v.Now != nil {
		now = v.Now()
	}
	// checked_at is diagnostic metadata, never permission to reuse this result.
	if status.CheckedAt.IsZero() || status.CheckedAt.After(now.Add(time.Second)) {
		return nil, ErrDenied
	}
	b := status.Completion.Evidence.Binding
	if b.Authority != v.Authority || b.Audience != in.Audience || b.ItemID != in.ItemID || len(status.Credential.Credentials) != 1 || keyRef(status.Credential.Credentials[0].ID) != b.KeyRef {
		return nil, ErrDenied
	}
	start, e := seconds(b.IssuedAt)
	if e != nil {
		return nil, e
	}
	end, e := seconds(b.ExpiresAt)
	if e != nil {
		return nil, e
	}
	if status.Completion.AuthTime.Before(start) || !status.Completion.AuthTime.Before(end) || status.Completion.AuthTime.After(now) {
		return nil, ErrDenied
	}
	party, e := proof.NewParty(v.RPID, "Basalisk", v.Origins)
	if e != nil {
		return nil, e
	}
	raw, _ := json.Marshal(status.Completion.Evidence)
	_, e = VerifyEvidence(party, status.Credential, raw, b, req.Operation, status.Origin, now, req.ExpiresAt, true)
	if e != nil {
		return nil, e
	}
	return raw, nil
}
