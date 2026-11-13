package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hollis-labs/basalisk/internal/fecapture"
)

// Frag: the owner's quick captures, written to the fragment submissions queue on this server
// first (so a note is never lost and capturing works offline) and delivered by the same
// worker as a paired device's notes. A capture's row has device_id "owner:<user>" and, until
// the worker binds it, no integration: it goes to the default Fragments Engine integration.
//
// The context frozen with a capture (work session, focused window) stays here, in the row's
// payload; FE receives the content and tags unaltered.

const ownerDevicePrefix = "owner:"

func isOwnerCapture(device string) bool { return strings.HasPrefix(device, ownerDevicePrefix) }

// ownerCapture is what an owner capture's row stores: the submission plus the context.
type ownerCapture struct {
	fragmentSubmission
	OwnerContext json.RawMessage `json:"ownerContext,omitempty"`
}

func (a *API) fragRoutes() []route {
	return []route{
		{"GET", "/api/frag", ScopeRead, a.fragState},
		{"POST", "/api/frag/captures", ScopeWrite, a.fragCapture},
		{"GET", "/api/frag/captures", ScopeRead, a.fragCaptures},
	}
}

func (s *Service) setFragMode(mode string) {
	s.deliveryMu.Lock()
	s.fragMode = mode
	s.deliveryMu.Unlock()
}

// ownerIntegration is the integration owner captures go to: the configured one, or the
// only Fragments Engine integration there is. ok is false when there is none or several.
func (s *Service) ownerIntegration(ctx context.Context, configured string) (string, bool) {
	if configured != "" {
		return configured, true
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM integrations WHERE provider='fragments-engine' ORDER BY id LIMIT 2`)
	if err != nil {
		return "", false
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) != 1 {
		return "", false
	}
	return ids[0], true
}

func (w *FragmentWorker) personal() bool { return w.client == nil }

func (w *FragmentWorker) issuer() string {
	if w.personal() {
		return personalIssuer
	}
	return w.config.IssuerURL
}

func (w *FragmentWorker) subject() string {
	if w.personal() {
		return personalPrincipal
	}
	return w.config.Subject
}

// bindOwner gives an owner capture queued without an integration the default one.
func (w *FragmentWorker) bindOwner(ctx context.Context, j *deliveryJob) error {
	if !isOwnerCapture(j.device) || j.integration != "" {
		return nil
	}
	id, ok := w.svc.ownerIntegration(ctx, w.config.IntegrationID)
	if !ok {
		return &fecapture.Failure{Code: "delivery_unavailable", Retryable: true}
	}
	j.integration = id
	return w.update(ctx, *j, `integration_id=?`, id)
}

// ownerAuthorized checks an owner capture against its integration: no device, no connection.
func (w *FragmentWorker) ownerAuthorized(ctx context.Context, j deliveryJob) (string, error) {
	if !w.personal() && j.integration != w.config.IntegrationID {
		return "", &fecapture.Failure{Code: "connection_changed"}
	}
	var provider, origin string
	err := w.svc.db.QueryRowContext(ctx, `SELECT provider,base_url FROM integrations WHERE id=?`, j.integration).Scan(&provider, &origin)
	if errors.Is(err, sql.ErrNoRows) {
		return "", &fecapture.Failure{Code: "connection_changed"}
	}
	if err != nil {
		return "", err
	}
	if provider != "fragments-engine" || !fragmentOrigin(origin) {
		return "", &fecapture.Failure{Code: "connection_changed"}
	}
	origin = strings.TrimRight(origin, "/")
	if j.manifest != "" && (j.origin != origin || j.issuer != w.issuer() || j.subject != w.subject()) {
		return "", &fecapture.Failure{Code: "connection_changed"}
	}
	return origin, nil
}

// bearer is the credential for one delivery: the exchanged token, or in personal mode the
// integration's own stored key. It is never logged or stored.
func (w *FragmentWorker) bearer(ctx context.Context, j deliveryJob) (string, error) {
	if !w.personal() {
		return w.client.Exchange(ctx)
	}
	var ref string
	if err := w.svc.db.QueryRowContext(ctx, `SELECT secret_ref FROM integrations WHERE id=?`, j.integration).Scan(&ref); err != nil {
		return "", &fecapture.Failure{Code: "connection_changed"}
	}
	res, err := w.svc.secrets.Resolve(ctx, ref)
	if err != nil {
		return "", &fecapture.Failure{Code: "credential_unavailable"}
	}
	return res.Secret.Reveal(), nil
}

// fragState answers GET /api/frag: how captures are delivered, and to which integration.
func (a *API) fragState(w http.ResponseWriter, r *http.Request) {
	a.svc.deliveryMu.RLock()
	mode := a.svc.fragMode
	configured := a.svc.delivery.IntegrationID
	a.svc.deliveryMu.RUnlock()
	if mode != "exchange" {
		configured = "" // personal mode always goes to the only FE integration
	}
	if mode == "" {
		mode = "off" // no delivery worker in this process
	}
	out := map[string]any{"mode": mode, "integration": nil}
	if id, ok := a.svc.ownerIntegration(r.Context(), configured); ok && mode != "off" {
		var name string
		if a.svc.db.QueryRowContext(r.Context(), `SELECT name FROM integrations WHERE id=?`, id).Scan(&name) == nil {
			out["integration"] = map[string]string{"id": id, "name": name}
		}
	}
	reply(w, 200, out)
}

// fragContext is what the capture box knew when the draft began and when it was saved: the
// work session (as the box read it) and the window that had focus.
type fragContext struct {
	Draft *fragMoment `json:"draft,omitempty"`
	Save  *fragMoment `json:"save,omitempty"`
}
type fragMoment struct {
	Session *fragSession    `json:"session,omitempty"`
	Window  json.RawMessage `json:"window,omitempty"`
}
type fragSession struct {
	ID         string `json:"id"`
	Title      string `json:"title,omitempty"`
	Project    string `json:"project,omitempty"`
	Workstream string `json:"workstream,omitempty"`
	ContextID  string `json:"contextId,omitempty"`
}

// freezeOwnerContext checks the context the box sent and completes it on the server: a draft
// session is kept only if it is the owner's, and the save-time session is read here, not
// taken from the request. The sessions also go in the submission's draft and save context,
// which link the saved fragment to the session (they are not sent to FE for owner captures).
func (a *API) freezeOwnerContext(ctx context.Context, owner, device string, in fragCaptureIn, sub *fragmentSubmission) json.RawMessage {
	var c fragContext
	if len(in.Context) > 0 {
		_ = json.Unmarshal(in.Context, &c)
	}
	if c.Draft != nil && c.Draft.Session != nil && (c.Draft.Session.ID == "" || a.svc.activity.Owns(ctx, owner, c.Draft.Session.ID) != nil) {
		c.Draft.Session = nil
	}
	if c.Save == nil {
		c.Save = &fragMoment{}
	}
	c.Save.Session = nil
	if st, err := a.svc.activity.State(ctx, owner); err == nil && st.Current != nil && st.Current.Status != "ended" {
		cur := st.Current
		c.Save.Session = &fragSession{ID: cur.ID, Title: cur.Title, Project: cur.Context.Project, Workstream: cur.Context.Workstream, ContextID: cur.Context.ID}
	}
	moment := func(m *fragMoment, at string) *artifactContext {
		if m == nil || m.Session == nil {
			return nil
		}
		return &artifactContext{SchemaVersion: 1, Origin: "basalisk", DeviceID: device, SessionID: m.Session.ID, ContextID: m.Session.ContextID,
			ContextAsOf: at, PublishedAt: at, ExpiresAt: at, Project: m.Session.Project, Workstream: m.Session.Workstream, HistoryRefs: []string{}}
	}
	sub.Draft = moment(c.Draft, in.DraftAt)
	if sub.Draft == nil {
		sub.Draft = moment(c.Save, in.DraftAt) // the session the note was saved in
	}
	sub.SaveContext = moment(c.Save, in.SubmittedAt)
	raw, _ := json.Marshal(c)
	return raw
}

type fragCaptureIn struct {
	CaptureID   string          `json:"captureId"`
	Content     string          `json:"content"`
	Tags        []string        `json:"tags"`
	DraftAt     string          `json:"draftAt"`
	SubmittedAt string          `json:"submittedAt"`
	Context     json.RawMessage `json:"context,omitempty"`
}

// fragCapture queues one capture. The same captureId with the same content and tags answers
// with the existing row (a second ⌘↩ is harmless); with different ones it is refused.
func (a *API) fragCapture(w http.ResponseWriter, r *http.Request) {
	owner, err := a.ownerID(r)
	if err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	var in fragCaptureIn
	if !decodeLimit(w, r, &in, 128*1024) {
		return
	}
	draft, e1 := time.Parse(time.RFC3339Nano, in.DraftAt)
	submitted, e2 := time.Parse(time.RFC3339Nano, in.SubmittedAt)
	if !validCaptureID(in.CaptureID) || !utf8.ValidString(in.Content) || strings.TrimSpace(in.Content) == "" || len(in.Content) > 64*1024 || strings.ContainsRune(in.Content, 0) || len(in.Tags) > 32 || e1 != nil || e2 != nil || draft.After(submitted) || submitted.After(time.Now().Add(30*time.Second)) {
		fail(w, 400, "Invalid capture (maximum 64 KiB and 32 tags).")
		return
	}
	for _, tag := range in.Tags {
		if strings.TrimSpace(tag) == "" || !metadataOK(tag, 100) {
			fail(w, 400, "Invalid tag.")
			return
		}
	}
	if len(in.Context) > 16*1024 || (len(in.Context) > 0 && !json.Valid(in.Context)) {
		fail(w, 400, "Invalid capture context (maximum 16 KiB of JSON).")
		return
	}
	device := ownerDevicePrefix + owner
	// The hash covers what FE receives; the context is ours and may be read again at save.
	sub := fragmentSubmission{CaptureID: in.CaptureID, Content: in.Content, Tags: in.Tags, DraftAt: in.DraftAt, SubmittedAt: in.SubmittedAt}
	ownerCtx := a.freezeOwnerContext(r.Context(), owner, device, in, &sub)
	hashed, _ := json.Marshal(struct {
		Content string   `json:"content"`
		Tags    []string `json:"tags"`
	}{sub.Content, sub.Tags})
	hash := digest(string(hashed))
	stored, _ := json.Marshal(ownerCapture{sub, ownerCtx})

	tx, err := a.svc.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	defer func() { _ = tx.Rollback() }()
	var oldHash string
	err = tx.QueryRowContext(r.Context(), `SELECT payload_hash FROM fragment_submissions WHERE device_id=? AND capture_id=?`, device, in.CaptureID).Scan(&oldHash)
	if err == nil {
		if oldHash != hash {
			fail(w, 409, "This capture was already queued with different text. Keep your draft and try again.")
			return
		}
		old, e := readSubmission(tx.QueryRowContext(r.Context(), `SELECT `+submissionColumns+` FROM fragment_submissions WHERE device_id=? AND capture_id=?`, device, in.CaptureID))
		if e != nil {
			fail(w, 503, "Capture storage unavailable.")
			return
		}
		reply(w, 200, old)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	var total, pending, mine int
	err = tx.QueryRowContext(r.Context(), `SELECT count(*),coalesce(sum(state NOT IN ('saved','discarded')),0),coalesce(sum(device_id=? AND state NOT IN ('saved','discarded')),0) FROM fragment_submissions`, device).Scan(&total, &pending, &mine)
	if err != nil {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	if total >= 10000 || pending >= 500 || mine >= 100 {
		fail(w, 409, "Capture storage is full. Keep your draft and review the outbox.")
		return
	}
	now := time.Now().UTC().Format(timestampFormat)
	result, err := tx.ExecContext(r.Context(), `INSERT INTO fragment_submissions(device_id,capture_id,integration_id,payload_hash,payload,state,code,created_at,updated_at) VALUES(?,?,'',?,?,'queued','delivery_unavailable',?,?)`, device, in.CaptureID, hash, string(stored), now, now)
	if err != nil {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	seq, err := result.LastInsertId()
	if err != nil || tx.Commit() != nil {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	reply(w, 202, fragmentSubmissionStatus{Sequence: seq, CaptureID: in.CaptureID, DeviceID: device, State: "queued", Code: "delivery_unavailable", CreatedAt: now, UpdatedAt: now})
}

// fragCaptures lists the owner's recent captures, newest first (?limit=, at most 50).
func (a *API) fragCaptures(w http.ResponseWriter, r *http.Request) {
	owner, err := a.ownerID(r)
	if err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	rows, err := a.svc.db.QueryContext(r.Context(), `SELECT `+submissionColumns+`,payload FROM fragment_submissions WHERE device_id=? ORDER BY sequence DESC LIMIT 50`, ownerDevicePrefix+owner)
	if err != nil {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	defer func() { _ = rows.Close() }()
	type item struct {
		fragmentSubmissionStatus
		Content string          `json:"content"`
		Tags    []string        `json:"tags"`
		Context json.RawMessage `json:"context,omitempty"`
	}
	list := []item{}
	for rows.Next() {
		var payload, origin string
		var leaseUntil int64
		var it item
		err := rows.Scan(&it.Sequence, &it.CaptureID, &it.DeviceID, &it.IntegrationID, &it.State, &it.Code, &it.CreatedAt, &it.UpdatedAt, &it.FragmentID, &it.FragmentRevisionID, &origin, &it.NextAttempt, &it.MayHaveSent, &leaseUntil, &payload)
		if err != nil {
			fail(w, 503, "Capture storage unavailable.")
			return
		}
		it.Delivering = leaseUntil > time.Now().Unix()
		if it.State == "saved" && it.FragmentID != "" {
			it.FragmentURL = origin + "/sysop/reader/" + it.FragmentID
		}
		var oc ownerCapture
		if json.Unmarshal([]byte(payload), &oc) == nil { // a discarded row's payload is cleared
			it.Content, it.Tags, it.Context = oc.Content, oc.Tags, oc.OwnerContext
		}
		list = append(list, it)
	}
	reply(w, 200, map[string]any{"items": list})
}
