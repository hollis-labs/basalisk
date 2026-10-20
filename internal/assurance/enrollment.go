package assurance

import (
	"context"
	"encoding/json"
	"time"

	proof "github.com/hollis-labs/libs/security/webauthn"
)

// EnrollmentPolicy is host-owned authorization for first-key bootstrap or
// existing-key-authorized enrollment. A password session alone is insufficient.
// The host must check the SAME authorization at begin and finish. There is no
// permissive default, transport caller, or browser flag that grants this policy.
type EnrollmentPolicy interface {
	AuthorizeEnrollment(context.Context, Session, string) error
}

func (s *Service) BeginEnrollment(ctx context.Context, handle, origin string, policy EnrollmentPolicy) (string, json.RawMessage, error) {
	u, e := s.session(ctx, handle)
	if e != nil {
		return "", nil, e
	}
	if policy == nil || !contains(s.cfg.Origins, origin) || policy.AuthorizeEnrollment(ctx, u, "") != nil {
		return "", nil, ErrDenied
	}
	st, e := s.store.Load(ctx, u.Subject)
	if e != nil {
		return "", nil, e
	}
	if st.Version == 0 {
		st.User = proof.User{ID: []byte(u.Subject), Name: u.Subject, DisplayName: "Basalisk owner"}
		if e = s.store.CompareAndSwap(ctx, u.Subject, 0, st); e != nil {
			return "", nil, e
		}
	}
	live := 0
	for _, c := range st.Pending {
		if s.now().Before(c.ExpiresAt) {
			live++
		}
	}
	if live >= 10 || len(st.User.Credentials) >= 10 {
		return "", nil, ErrDenied
	}
	end := s.now().Add(5 * time.Minute)
	if u.ExpiresAt.Before(end) {
		end = u.ExpiresAt
	}
	return s.ceremonies.BeginRegistration(ctx, u.Subject, u.Binding, origin, end)
}
func (s *Service) FinishEnrollment(ctx context.Context, handle, id string, raw json.RawMessage, policy EnrollmentPolicy) (string, error) {
	u, e := s.session(ctx, handle)
	if e != nil {
		return "", e
	}
	if policy == nil || policy.AuthorizeEnrollment(ctx, u, id) != nil || len(raw) > 32<<10 || boundedJSON(raw) != nil {
		return "", ErrDenied
	}
	credential, e := s.ceremonies.FinishRegistration(ctx, u.Subject, u.Binding, id, raw)
	if e != nil {
		return "", e
	}
	ref := keyRef(credential.ID)
	if _, e = s.activeUser(ctx, u.Subject, ref); e != nil {
		return "", e
	}
	return ref, nil
}
