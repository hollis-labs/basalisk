package account

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Immutable operation, not an upstream HTTP request. Unknown draft context stays
// unknown; a human activity session never grants capture authority.
type fragmentSubmission struct {
	CaptureID        string           `json:"captureID"`
	IntegrationID    string           `json:"integrationID"`
	Content          string           `json:"content"`
	Tags             []string         `json:"tags"`
	DraftAt          string           `json:"draftAt"`
	SubmittedAt      string           `json:"submittedAt"`
	Draft            *artifactContext `json:"draft,omitempty"`
	SaveContext      *artifactContext `json:"saveContext,omitempty"`
	Metadata         *captureMetadata `json:"metadata,omitempty"`
	ContextBindingID string           `json:"contextBindingID,omitempty"`
}
type fragmentSubmissionStatus struct {
	Sequence           int64  `json:"sequence"`
	CaptureID          string `json:"captureID"`
	DeviceID           string `json:"deviceID"`
	IntegrationID      string `json:"integrationID"`
	State              string `json:"state"`
	Code               string `json:"code"`
	CreatedAt          string `json:"createdAt"`
	UpdatedAt          string `json:"updatedAt"`
	FragmentID         string `json:"fragmentID,omitempty"`
	FragmentRevisionID string `json:"fragmentRevisionID,omitempty"`
	FragmentURL        string `json:"fragmentURL,omitempty"`
	NextAttempt        int64  `json:"nextAttempt,omitempty"`
	MayHaveSent        bool   `json:"mayHaveSent"`
	Delivering         bool   `json:"delivering"`
}

const submissionColumns = `sequence,capture_id,device_id,integration_id,state,code,created_at,updated_at,fragment_id,fragment_revision_id,delivery_origin,next_attempt,may_have_sent,lease_until`

func readSubmission(row interface{ Scan(...any) error }) (fragmentSubmissionStatus, error) {
	var s fragmentSubmissionStatus
	var origin string
	var leaseUntil int64
	err := row.Scan(&s.Sequence, &s.CaptureID, &s.DeviceID, &s.IntegrationID, &s.State, &s.Code, &s.CreatedAt, &s.UpdatedAt, &s.FragmentID, &s.FragmentRevisionID, &origin, &s.NextAttempt, &s.MayHaveSent, &leaseUntil)
	s.Delivering = leaseUntil > time.Now().Unix()
	if s.State == "saved" && s.FragmentID != "" {
		s.FragmentURL = origin + "/sysop/reader/" + s.FragmentID
	}
	return s, err
}
func validCaptureID(id string) bool {
	if len(id) < 16 || len(id) > 100 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func (a *API) enqueueFragment(w http.ResponseWriter, r *http.Request) {
	d, token, ok := a.device(w, r, scopeCapture)
	if !ok {
		return
	}
	var in fragmentSubmission
	if !decodeLimit(w, r, &in, 128*1024) {
		return
	}
	draft, e1 := time.Parse(time.RFC3339Nano, in.DraftAt)
	submitted, e2 := time.Parse(time.RFC3339Nano, in.SubmittedAt)
	if !validCaptureID(in.CaptureID) || in.IntegrationID == "" || len(in.IntegrationID) > 100 || !utf8.ValidString(in.Content) || strings.TrimSpace(in.Content) == "" || len(in.Content) > 64*1024 || strings.ContainsRune(in.Content, 0) || len(in.Tags) > 32 || e1 != nil || e2 != nil || draft.After(submitted) || submitted.After(time.Now().Add(30*time.Second)) {
		fail(w, 400, "Invalid fragment submission (maximum 64 KiB and 32 tags).")
		return
	}
	for _, tag := range in.Tags {
		if strings.TrimSpace(tag) == "" || !metadataOK(tag, 100) {
			fail(w, 400, "Invalid fragment tag.")
			return
		}
	}
	if !validCaptureMetadata(in.Metadata) {
		fail(w, 400, "Invalid capture metadata (version 1, maximum 16 KiB).")
		return
	}
	if (in.ContextBindingID != "" && (!validCaptureID(in.ContextBindingID) || in.Draft != nil || in.SaveContext != nil)) || (!slices.Contains(d.Scopes, scopeObserve) && (in.Draft != nil || in.SaveContext != nil)) {
		fail(w, 400, "Use a server-issued context binding or omit context.")
		return
	}
	raw, _ := json.Marshal(in)
	hash := digest(string(raw))
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	defer tx.Rollback()
	verified, err := a.store.pairing.DeviceInTransaction(r.Context(), tx, token)
	if err != nil || verified.ID != d.ID || !slices.Contains(verified.Scopes, scopeCapture) {
		fail(w, 401, "Device authorization changed.")
		return
	}
	var oldHash string
	err = tx.QueryRowContext(r.Context(), `SELECT payload_hash FROM fragment_submissions WHERE device_id=? AND capture_id=?`, d.ID, in.CaptureID).Scan(&oldHash)
	if err == nil {
		if oldHash != hash {
			fail(w, 409, "Capture ID already belongs to a different submission.")
			return
		}
		old, e := readSubmission(tx.QueryRowContext(r.Context(), `SELECT `+submissionColumns+` FROM fragment_submissions WHERE device_id=? AND capture_id=?`, d.ID, in.CaptureID))
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
	var bound, provider, origin string
	err = tx.QueryRowContext(r.Context(), `SELECT c.integration_id,i.provider,i.base_url FROM fragment_connections c JOIN integrations i ON i.id=c.integration_id WHERE c.device_id=?`, d.ID).Scan(&bound, &provider, &origin)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	if err != nil || bound != in.IntegrationID || provider != "fragments-engine" || !fragmentOrigin(origin) {
		fail(w, 409, "The selected fragment connection is no longer authorized. Your note has not been accepted.")
		return
	}
	if in.Draft != nil && !validArtifactContext(r.Context(), tx, d, *in.Draft, draft) {
		fail(w, 400, "Draft context could not be verified. Your note has not been accepted.")
		return
	}
	if in.ContextBindingID != "" {
		if !slices.Contains(verified.Scopes, scopeContext) {
			fail(w, 403, "Activity context permission required for this binding.")
			return
		}
		var frozen, boundAt, boundOrigin string
		err = tx.QueryRowContext(r.Context(), `SELECT payload,draft_at,origin FROM capture_context_bindings WHERE id=? AND device_id=? AND capture_id=?`, in.ContextBindingID, d.ID, in.CaptureID).Scan(&frozen, &boundAt, &boundOrigin)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			fail(w, 503, "Capture context unavailable.")
			return
		}
		requestOrigin, originErr := captureRequestOrigin(r)
		if err != nil || originErr != nil || boundAt != in.DraftAt || boundOrigin != requestOrigin {
			fail(w, 400, "Context binding does not match this capture and draft time.")
			return
		}
		var binding captureContextBinding
		if json.Unmarshal([]byte(frozen), &binding) != nil {
			fail(w, 503, "Capture context unavailable.")
			return
		}
		// The snapshot was issued transactionally to this device/capture. It is
		// evidence about draft initiation, not a fresh read or a bearer grant.
		in.Draft = binding.Draft
	}
	if in.SaveContext != nil && !validArtifactContext(r.Context(), tx, d, *in.SaveContext, submitted) {
		in.SaveContext = nil
	}
	var total, pending, devicePending int
	err = tx.QueryRowContext(r.Context(), `SELECT count(*),coalesce(sum(state NOT IN ('saved','discarded')),0),coalesce(sum(device_id=? AND state NOT IN ('saved','discarded')),0) FROM fragment_submissions`, d.ID).Scan(&total, &pending, &devicePending)
	if err != nil {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	if total >= 10000 || pending >= 500 || devicePending >= 100 {
		fail(w, 409, "Capture storage is full. Keep your draft and review the outbox.")
		return
	}
	stored, _ := json.Marshal(in)
	now := time.Now().UTC().Format(timestampFormat)
	// No FK cascade: deleting credentials or revoking a device must not delete
	// unsent notes. The separately configured worker resolves its dedicated
	// enrollment parent only at the outbound adapter boundary.
	result, err := tx.ExecContext(r.Context(), `INSERT INTO fragment_submissions(device_id,capture_id,integration_id,payload_hash,payload,state,code,created_at,updated_at) VALUES(?,?,?,?,?,'queued','delivery_unavailable',?,?)`, d.ID, in.CaptureID, in.IntegrationID, hash, string(stored), now, now)
	if err != nil {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	seq, err := result.LastInsertId()
	if err != nil || tx.Commit() != nil {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	reply(w, 202, fragmentSubmissionStatus{Sequence: seq, CaptureID: in.CaptureID, DeviceID: d.ID, IntegrationID: in.IntegrationID, State: "queued", Code: "delivery_unavailable", CreatedAt: now, UpdatedAt: now})
}
func (a *API) fragmentSubmissionStatus(w http.ResponseWriter, r *http.Request) {
	tx, deviceID, ok := a.captureTransaction(w, r)
	if !ok {
		return
	}
	defer tx.Rollback()
	status, err := readSubmission(tx.QueryRowContext(r.Context(), `SELECT `+submissionColumns+` FROM fragment_submissions WHERE device_id=? AND capture_id=?`, deviceID, r.PathValue("id")))
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "Capture not found.")
		return
	}
	if err != nil {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	reply(w, 200, status)
}
func (a *API) fragmentSubmissions(w http.ResponseWriter, r *http.Request) {
	before, ok := beforeCursor(w, r)
	if !ok {
		return
	}
	if before == 0 {
		before = 1 << 62
	}
	rows, err := a.store.db.QueryContext(r.Context(), `SELECT `+submissionColumns+` FROM fragment_submissions WHERE sequence<? ORDER BY sequence DESC LIMIT 51`, before)
	if err != nil {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	defer rows.Close()
	items := []fragmentSubmissionStatus{}
	var next int64
	for rows.Next() {
		item, e := readSubmission(rows)
		if e != nil {
			fail(w, 503, "Capture storage unavailable.")
			return
		}
		if len(items) == 50 {
			next = items[49].Sequence
			break
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	reply(w, 200, map[string]any{"items": items, "nextBefore": next})
}
func (a *API) fragmentSubmissionDetail(w http.ResponseWriter, r *http.Request) {
	var raw string
	err := a.store.db.QueryRowContext(r.Context(), `SELECT payload FROM fragment_submissions WHERE sequence=?`, r.PathValue("id")).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "Capture not found.")
		return
	}
	if err != nil {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	var submission *fragmentSubmission
	if raw != "" && json.Unmarshal([]byte(raw), &submission) != nil {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	reply(w, 200, map[string]any{"submission": submission})
}
func (a *API) discardFragmentSubmission(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		fail(w, 400, "Invalid capture.")
		return
	}
	// Keep identity/hash tombstone so retries cannot resurrect discarded notes.
	// This is logical deletion, not secure erasure of SQLite pages/backups.
	result, err := a.store.db.ExecContext(r.Context(), `UPDATE fragment_submissions SET state='discarded',code=CASE WHEN may_have_sent=1 THEN 'owner_stopped_delivery' ELSE 'owner_discarded' END,payload='',manifest='',receipt='',updated_at=? WHERE sequence=? AND state IN ('queued','needs_attention','discarded') AND lease_until<=?`, time.Now().UTC().Format(timestampFormat), id, time.Now().Unix())
	if err != nil {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		fail(w, 409, "Delivery is active or already saved. Refresh before trying again.")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}

func (a *API) retryFragmentSubmission(w http.ResponseWriter, r *http.Request) {
	result, err := a.store.db.ExecContext(r.Context(), `UPDATE fragment_submissions SET state='queued',code='retry_requested',next_attempt=0,lease_token='',lease_until=0,updated_at=? WHERE sequence=? AND state IN ('queued','needs_attention') AND lease_until<=?`, time.Now().UTC().Format(timestampFormat), r.PathValue("id"), time.Now().Unix())
	if err != nil {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		fail(w, 409, "Capture is active, saved, or discarded. Refresh before retrying.")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
