// Package genui binds the reviewed GenUI gateway to Basalisk's current users,
// durable SQLite journal and the existing account preference CAS. It is not an
// issuer or an approval service, and grants no legacy agent standing authority.
package genui

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/basalisk/internal/preferences"
	libauth "github.com/hollis-labs/libs/security/auth"
	contract "github.com/hollis-labs/substrate/genui"
	"github.com/hollis-labs/substrate/genui/gateway"
	"github.com/hollis-labs/substrate/modules/identity"
)

const Namespace = "basalisk.preferences"
const ScopePrefix = "basalisk.genui"

type Users interface {
	User(context.Context, string) (identity.PublicUser, error)
}

// ProducerEnrollment is constructor-only host configuration. No caller may
// choose its owner or mutate enrollment through a request. The default is none.
type ProducerEnrollment struct{ Subject, OwnerID string }
type Host struct {
	repo      *repository
	prefs     *preferences.Store
	users     Users
	producers map[string]string
	Service   *gateway.Service
}

func New(prefs *preferences.Store, users Users, producers []ProducerEnrollment) (*Host, error) {
	if prefs == nil || prefs.DB == nil || users == nil {
		return nil, gateway.ErrUnavailable
	}
	h := &Host{repo: &repository{db: prefs.DB}, prefs: prefs, users: users, producers: map[string]string{}}
	for _, p := range producers {
		if p.Subject == "" || !strings.HasPrefix(p.OwnerID, "user:") || h.producers[p.Subject] != "" {
			return nil, gateway.ErrInvalid
		}
		h.producers[p.Subject] = p.OwnerID
	}
	var e error
	h.Service, e = gateway.New(gateway.Ports{Catalog: h, Surfaces: h.repo, Journal: h.repo, Policy: h, Executor: h})
	return h, e
}
func scope(actor string) gateway.Scope { return gateway.Scope{Namespace: Namespace, Actor: actor} }
func has(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want || s == "*" || (strings.HasSuffix(s, ".*") && strings.HasPrefix(want, strings.TrimSuffix(s, "*"))) {
			return true
		}
	}
	return false
}
func (h *Host) current(ctx context.Context, actor, operation string) error {
	if !strings.HasPrefix(actor, "user:") || len(actor) <= 5 {
		return gateway.ErrForbidden
	}
	u, e := h.users.User(ctx, strings.TrimPrefix(actor, "user:"))
	if e != nil || u.Disabled || u.ID != strings.TrimPrefix(actor, "user:") || !has(u.Scopes, ScopePrefix+"."+operation) {
		return gateway.ErrForbidden
	}
	base := "basalisk.read"
	if operation == "act" || operation == "confirm" {
		base = "basalisk.write"
	}
	if !has(u.Scopes, base) {
		return gateway.ErrForbidden
	}
	return nil
}
func validPrincipal(p libauth.Principal) bool {
	return p.Verified && p.ID != "" && p.Audience == "basalisk" && (p.ExpiresAt.IsZero() || time.Now().Before(p.ExpiresAt))
}
func (h *Host) Resolve(ctx context.Context, p libauth.Principal) (gateway.Scope, error) {
	if !validPrincipal(p) || p.Kind != "user" {
		return gateway.Scope{}, gateway.ErrForbidden
	}
	if e := h.current(ctx, p.ID, "read"); e != nil {
		return gateway.Scope{}, e
	}
	return scope(p.ID), nil
}
func (h *Host) Authorize(ctx context.Context, p libauth.Principal, operation string) (gateway.Scope, error) {
	s, e := h.Resolve(ctx, p)
	if e != nil || !p.HasScope(ScopePrefix+"."+operation) {
		return gateway.Scope{}, gateway.ErrForbidden
	}
	if e = h.current(ctx, p.ID, operation); e != nil {
		return gateway.Scope{}, e
	}
	return s, nil
}
func randomID() string {
	var b [24]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func basis(v preferences.Value) contract.Basis {
	return contract.Basis{Revision: strconv.Itoa(v.Revision)}
}
func choice(label, id, actor string, b contract.Basis, input map[string]any) contract.Choice {
	return contract.Choice{Label: label, CallFunction: contract.FunctionCall{Name: contract.FunctionName, CatalogID: contract.CatalogID, Args: contract.ActionArgs{Action: contract.Action{ID: id, Subject: contract.Subject{Kind: "preferences", ID: actor}, Basis: b, Risk: contract.RiskLow}, Input: input}}}
}
func (h *Host) surface(s gateway.Scope, pattern contract.Pattern, props any, data contract.DataModel, ops []string) (gateway.Surface, error) {
	e, err := contract.NewSurface(randomID(), pattern, props, data)
	if err != nil {
		return gateway.Surface{}, err
	}
	switch p := props.(type) {
	case contract.FormProps:
		e.Title = p.Title
	case contract.ApprovalProps:
		e.Title = p.Title
	case contract.ListDetailProps:
		e.Title = p.Title
	}
	out := gateway.Surface{Scope: s, Envelope: e, Generation: randomID(), ExpiresAt: time.Now().Add(15 * time.Minute)}
	choices, err := contract.Choices(e)
	if err != nil || len(choices) != len(ops) {
		return gateway.Surface{}, gateway.ErrInvalid
	}
	remaining := ops
	for _, c := range choices {
		if len(remaining) == 0 {
			return gateway.Surface{}, gateway.ErrInvalid
		}
		operation := remaining[0]
		remaining = remaining[1:]
		d, err := gateway.ChoiceDigest(c.CallFunction)
		if err != nil {
			return out, err
		}
		out.Operations = append(out.Operations, gateway.OperationBinding{ChoiceDigest: d, Operation: operation})
	}
	return out, nil
}
func (h *Host) Materialize(ctx context.Context, s gateway.Scope, q gateway.ShowRequest) (gateway.Surface, error) {
	if s.Namespace != Namespace || q.Pattern != contract.PatternForm || string(q.Params) != "{}" {
		return gateway.Surface{}, gateway.ErrInvalid
	}
	if e := h.current(ctx, s.Actor, "show"); e != nil {
		return gateway.Surface{}, e
	}
	v, e := h.prefs.Read(ctx, strings.TrimPrefix(s.Actor, "user:"))
	if e != nil {
		return gateway.Surface{}, e
	}
	maxLength := 100
	p := contract.FormProps{Title: "Review preferences", State: contract.ViewReady, Fields: []contract.FormField{{Key: "theme", Label: "Theme", Type: "text", Required: true, Help: "dark, light or system", MaxLength: &maxLength}, {Key: "timezone", Label: "Timezone", Type: "text", Required: true, Help: "IANA timezone, such as UTC", MaxLength: &maxLength}}, Submit: choice("Review changes", randomID(), s.Actor, basis(v), map[string]any{})}
	return h.surface(s, contract.PatternForm, p, contract.DataModel{Draft: map[string]any{"theme": v.Theme, "timezone": v.Timezone}}, []string{"preferences.prepare"})
}
func (h *Host) ShowAgent(ctx context.Context, owner string, raw []byte) (contract.Envelope, error) {
	if e := h.current(ctx, owner, "show"); e != nil {
		return contract.Envelope{}, e
	}
	q, e := decodeShow(raw)
	if e != nil {
		return contract.Envelope{}, e
	}
	return h.Service.Show(ctx, scope(owner), q)
}

// The inner JSON is preserved as text through the agent's map-based tool API.
func decodeShow(raw []byte) (gateway.ShowRequest, error) {
	b, e := contract.CanonicalJSON(raw)
	if e != nil || len(b) > contract.MaxActionBytes {
		return gateway.ShowRequest{}, gateway.ErrInvalid
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil || len(m) != 2 || m["pattern"] == nil || m["params"] == nil {
		return gateway.ShowRequest{}, gateway.ErrInvalid
	}
	var q gateway.ShowRequest
	if json.Unmarshal(m["pattern"], &q.Pattern) != nil || q.Pattern != contract.PatternForm || string(m["params"]) != "{}" {
		return q, gateway.ErrInvalid
	}
	q.Params = m["params"]
	return q, nil
}
func (h *Host) ShowProducer(ctx context.Context, p libauth.Principal, raw []byte) (contract.Envelope, error) {
	owner := h.producers[p.ID]
	if !validPrincipal(p) || p.Kind != "service" || owner == "" || !p.HasScope(ScopePrefix+".show") {
		return contract.Envelope{}, gateway.ErrForbidden
	}
	return h.ShowAgent(ctx, owner, raw)
}
func (h *Host) List(ctx context.Context, s gateway.Scope) ([]SurfaceInfo, error) {
	if e := h.current(ctx, s.Actor, "read"); e != nil {
		return nil, e
	}
	return h.repo.list(ctx, s)
}
func (h *Host) Get(ctx context.Context, s gateway.Scope, id string) (contract.Envelope, error) {
	if e := h.current(ctx, s.Actor, "read"); e != nil {
		return contract.Envelope{}, e
	}
	v, e := h.repo.Get(ctx, s, id)
	return v.Envelope, e
}

type challenge struct {
	Digest, Generation, Nonce, ProofHash string
	ExpiresAt                            time.Time
}

func proofHash(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func deny(reason string) gateway.Decision {
	return gateway.Decision{Outcome: &contract.Outcome{Status: contract.StatusRefused, Reason: reason}}
}
func (h *Host) WithFence(ctx context.Context, r gateway.Request, fn func(context.Context, gateway.Decision) error) error {
	unlock := h.prefs.Lock(strings.TrimPrefix(r.Scope.Actor, "user:"))
	defer unlock()
	if e := h.current(ctx, r.Scope.Actor, "act"); e != nil {
		return fn(ctx, deny("absent-scope"))
	}
	if r.Scope.Namespace != Namespace || r.Call.CallAgentFunction.CallFunction.Args.Action.Subject != (contract.Subject{Kind: "preferences", ID: r.Scope.Actor}) {
		return fn(ctx, deny("unresolved-target"))
	}
	retained, e := h.repo.Get(ctx, r.Scope, r.Surface.Envelope.ID)
	if e != nil || retained.Superseded || retained.Generation != r.Surface.Generation {
		return gateway.ErrUnavailable
	}
	current, e := h.prefs.Read(ctx, strings.TrimPrefix(r.Scope.Actor, "user:"))
	if e != nil {
		return e
	}
	action := r.Call.CallAgentFunction.CallFunction.Args.Action
	if action.Basis != basis(current) {
		return fn(ctx, gateway.Decision{Outcome: &contract.Outcome{Status: contract.StatusStale, Current: ptrBasis(current)}})
	}
	if action.Risk == contract.RiskHigh {
		return fn(ctx, deny("denied"))
	}
	deadline := r.Surface.ExpiresAt
	risk := contract.RiskLow
	switch r.Operation {
	case "preferences.prepare", "preferences.cancel":
	case "preferences.update":
		risk = contract.RiskMedium
		var ch challenge
		if len(r.Previous.Challenge) == 0 {
			ch = challenge{Digest: r.Digest, Generation: r.Surface.Generation, Nonce: randomID(), ExpiresAt: time.Now().Add(2 * time.Minute)}
			if deadline.Before(ch.ExpiresAt) {
				ch.ExpiresAt = deadline
			}
		} else if json.Unmarshal(r.Previous.Challenge, &ch) != nil {
			return gateway.ErrUnavailable
		}
		if ch.Digest != r.Digest || ch.Generation != r.Surface.Generation || !time.Now().Before(ch.ExpiresAt) {
			return fn(ctx, gateway.Decision{Outcome: &contract.Outcome{Status: contract.StatusExpired}})
		}
		if action.Confirmation == "" {
			b, _ := json.Marshal(ch)
			return fn(ctx, gateway.Decision{Outcome: &contract.Outcome{Status: contract.StatusNeedsConfirmation, Confirm: &contract.Confirmation{What: "Apply these reviewed account preferences once. This acknowledgment is not reauthentication or MFA.", Scope: []string{"preferences.update"}, TTLMS: time.Until(ch.ExpiresAt).Milliseconds(), Who: r.Scope.Actor}}, Challenge: b})
		}
		if ch.ProofHash == "" || proofHash(action.Confirmation) != ch.ProofHash {
			return fn(ctx, deny("denied"))
		}
		deadline = ch.ExpiresAt
	default:
		return fn(ctx, deny("unregistered-route"))
	}
	bounded, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	return fn(bounded, gateway.Decision{Authority: &gateway.Authority{Generation: r.Surface.Generation, Deadline: deadline, Risk: risk}})
}
func ptrBasis(v preferences.Value) *contract.Basis { b := basis(v); return &b }
func (h *Host) Confirm(ctx context.Context, s gateway.Scope, raw []byte) (string, error) {
	call, e := contract.DecodeCall(raw)
	if e != nil || call.CallAgentFunction.CallFunction.Args.Action.Confirmation != "" {
		return "", gateway.ErrInvalid
	}
	unlock := h.prefs.Lock(strings.TrimPrefix(s.Actor, "user:"))
	defer unlock()
	if e = h.current(ctx, s.Actor, "confirm"); e != nil {
		return "", e
	}
	a := call.CallAgentFunction.CallFunction.Args.Action
	k := gateway.Key{Scope: s, ActionID: a.ID}
	v, e := h.repo.Load(ctx, k)
	if e != nil || v.Phase != gateway.PhaseWaiting || v.Claimed {
		return "", gateway.ErrForbidden
	}
	d, e := gateway.ChoiceDigest(call.CallAgentFunction.CallFunction)
	old, oe := gateway.ChoiceDigest(v.Request.CallAgentFunction.CallFunction)
	if e != nil || oe != nil || d != old || call.CallAgentFunction.SurfaceID != v.Request.CallAgentFunction.SurfaceID {
		return "", gateway.ErrForbidden
	}
	sf, e := h.repo.Get(ctx, s, call.CallAgentFunction.SurfaceID)
	if e != nil || sf.Superseded {
		return "", gateway.ErrForbidden
	}
	var ch challenge
	if json.Unmarshal(v.Challenge, &ch) != nil || ch.Digest != v.Digest || ch.Generation != sf.Generation || !time.Now().Before(ch.ExpiresAt) || !time.Now().Before(sf.ExpiresAt) {
		return "", gateway.ErrForbidden
	}
	current, e := h.prefs.Read(ctx, strings.TrimPrefix(s.Actor, "user:"))
	if e != nil || a.Basis != basis(current) {
		return "", gateway.ErrForbidden
	}
	proof := randomID()
	ch.ProofHash = proofHash(proof)
	v.Challenge, _ = json.Marshal(ch)
	_, ok, e := h.repo.CompareAndSwap(ctx, k, v.Revision, v)
	if e != nil {
		return "", e
	}
	if !ok {
		return "", gateway.ErrForbidden
	}
	return proof, nil
}
func (h *Host) Execute(ctx context.Context, e gateway.Execution) (contract.Outcome, error) {
	r := e.Request
	if err := h.current(ctx, r.Scope.Actor, "act"); err != nil {
		return contract.Outcome{}, err
	}
	if ctx.Err() != nil || !time.Now().Before(e.Authority.Deadline) {
		return contract.Outcome{}, gateway.ErrUnavailable
	}
	owner := strings.TrimPrefix(r.Scope.Actor, "user:")
	old, err := h.prefs.Read(ctx, owner)
	if err != nil {
		return contract.Outcome{}, err
	}
	if basis(old) != r.Call.CallAgentFunction.CallFunction.Args.Action.Basis {
		return contract.Outcome{Status: contract.StatusFailed, Code: "stale", Message: "Preferences changed before execution."}, nil
	}
	input := r.Call.CallAgentFunction.CallFunction.Args.Input
	switch r.Operation {
	case "preferences.cancel":
		return contract.Outcome{Status: contract.StatusCanceled, Cause: "user-canceled"}, nil
	case "preferences.prepare":
		draft, ok := input["draft"].(map[string]any)
		if !ok || len(draft) != 2 {
			return contract.Outcome{Status: contract.StatusFailed, Code: "invalid_preferences", Message: "Choose a valid theme and timezone."}, nil
		}
		theme, tok := draft["theme"].(string)
		tz, zok := draft["timezone"].(string)
		v := preferences.Value{Theme: theme, Timezone: tz, Revision: old.Revision}
		valid := preferences.Validate(v) == nil
		if !tok || !zok || !valid {
			return contract.Outcome{Status: contract.StatusFailed, Code: "invalid_preferences", Message: "Choose a valid theme and timezone."}, nil
		}
		id := randomID()
		p := contract.ApprovalProps{Title: "Apply preferences", Summary: "A reversible account change. Review the exact before and after values.", State: contract.ViewReady, Changes: []contract.Change{{Label: "Theme", Before: old.Theme, After: theme}, {Label: "Timezone", Before: old.Timezone, After: tz}}, Choices: []contract.Choice{choice("Apply once", id, r.Scope.Actor, basis(old), map[string]any{"theme": theme, "timezone": tz}), choice("Cancel", id, r.Scope.Actor, basis(old), map[string]any{"cancel": true})}}
		sf, surfaceErr := h.surface(r.Scope, contract.PatternApproval, p, contract.DataModel{}, []string{"preferences.update", "preferences.cancel"})
		if surfaceErr != nil {
			return contract.Outcome{}, surfaceErr
		}
		return h.keepResult(ctx, sf)
	case "preferences.update":
		theme, tok := input["theme"].(string)
		tz, zok := input["timezone"].(string)
		if !tok || !zok || len(input) != 2 {
			return contract.Outcome{}, gateway.ErrInvalid
		}
		v := preferences.Value{Theme: theme, Timezone: tz, Revision: old.Revision}
		if err = h.prefs.UpdateLocked(ctx, owner, v); err != nil {
			if errors.Is(err, preferences.ErrConflict) {
				return contract.Outcome{Status: contract.StatusFailed, Code: "stale", Message: "Preferences changed before execution."}, nil
			}
			return contract.Outcome{}, err
		}
		if ctx.Err() != nil || !time.Now().Before(e.Authority.Deadline) {
			return contract.Outcome{}, gateway.ErrUnavailable
		}
		v.Revision++
		p := contract.ListDetailProps{Title: "Preferences updated", State: contract.ViewReady, Items: []contract.ListItem{{ID: "preferences", Title: "Account preferences", Summary: v.Theme + " · " + v.Timezone}}, SelectedID: "preferences", Detail: &contract.Detail{Title: "Saved preferences", Body: "Theme: " + v.Theme + "\nTimezone: " + v.Timezone + "\nRevision: " + strconv.Itoa(v.Revision)}}
		sf, err := h.surface(r.Scope, contract.PatternListDetail, p, contract.DataModel{}, nil)
		if err != nil {
			return contract.Outcome{}, err
		}
		return h.keepResult(ctx, sf)
	}
	return contract.Outcome{}, gateway.ErrForbidden
}
func (h *Host) keepResult(ctx context.Context, sf gateway.Surface) (contract.Outcome, error) {
	if err := h.repo.Put(ctx, sf); err != nil {
		return contract.Outcome{}, err
	}
	b, _ := json.Marshal(map[string]string{"surfaceId": sf.Envelope.ID})
	return contract.Outcome{Status: contract.StatusOK, Result: b}, nil
}
