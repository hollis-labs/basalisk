package account

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"slices"
	"time"
)

// Keep credential validation and the device-owned read/write in one snapshot.
// A capture grant permits recovery of that device's operations, never the
// owner's outbox or another installation's captures.
func (a *API) captureTransaction(w http.ResponseWriter, r *http.Request) (*sql.Tx, string, bool) {
	d, token, ok := a.device(w, r, scopeCapture)
	if !ok {
		return nil, "", false
	}
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "Capture storage unavailable.")
		return nil, "", false
	}
	verified, err := a.store.pairing.DeviceInTransaction(r.Context(), tx, token)
	if err != nil || verified.ID != d.ID || !slices.Contains(verified.Scopes, scopeCapture) {
		tx.Rollback()
		fail(w, 401, "Device authorization changed.")
		return nil, "", false
	}
	return tx, d.ID, true
}

func (a *API) captureSubmissions(w http.ResponseWriter, r *http.Request) {
	tx, deviceID, ok := a.captureTransaction(w, r)
	if !ok {
		return
	}
	defer tx.Rollback()
	before, ok := beforeCursor(w, r)
	if !ok {
		return
	}
	if before == 0 {
		before = 1 << 62
	}
	rows, err := tx.QueryContext(r.Context(), `SELECT `+submissionColumns+` FROM fragment_submissions WHERE device_id=? AND sequence<? ORDER BY sequence DESC LIMIT 51`, deviceID, before)
	if err != nil {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	defer rows.Close()
	items := []fragmentSubmissionStatus{}
	var next int64
	for rows.Next() {
		item, err := readSubmission(rows)
		if err != nil {
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

func (a *API) retryCapture(w http.ResponseWriter, r *http.Request) {
	a.recoverCapture(w, r, false)
}

func (a *API) stopCapture(w http.ResponseWriter, r *http.Request) {
	a.recoverCapture(w, r, true)
}

func (a *API) recoverCapture(w http.ResponseWriter, r *http.Request, stop bool) {
	// Read the bounded action body before taking the single database connection.
	// A slow client must not hold a transaction while uploading a request.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1))
	if err != nil || len(body) != 0 {
		fail(w, 400, "Recovery actions require an empty body.")
		return
	}
	tx, deviceID, ok := a.captureTransaction(w, r)
	if !ok {
		return
	}
	defer tx.Rollback()
	if !validCaptureID(r.PathValue("id")) {
		fail(w, 400, "Invalid capture ID.")
		return
	}
	status, err := readSubmission(tx.QueryRowContext(r.Context(), `SELECT `+submissionColumns+` FROM fragment_submissions WHERE device_id=? AND capture_id=?`, deviceID, r.PathValue("id")))
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "Capture not found.")
		return
	}
	if err != nil {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	if stop && status.State == "discarded" {
		reply(w, 200, status)
		return
	}
	if status.Delivering || (status.State != "queued" && status.State != "needs_attention") {
		fail(w, 409, "Capture is active, saved, or discarded. Refresh its status before trying again.")
		return
	}
	// Invalidate expired leases too: a stale worker must not overwrite recovery.
	set := `state='queued',code='retry_requested',next_attempt=0,lease_token='',lease_until=0,updated_at=?`
	if stop {
		// Retain hash/identity and may_have_sent so replay cannot resurrect this
		// operation. Stopping delivery is not deletion of an upstream FE record.
		set = `state='discarded',code=CASE WHEN may_have_sent=1 THEN 'capture_stopped_delivery' ELSE 'capture_discarded' END,payload='',manifest='',receipt='',next_attempt=0,lease_token='',lease_until=0,updated_at=?`
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE fragment_submissions SET `+set+` WHERE sequence=? AND device_id=?`, time.Now().UTC().Format(timestampFormat), status.Sequence, deviceID)
	if err != nil {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	status, err = readSubmission(tx.QueryRowContext(r.Context(), `SELECT `+submissionColumns+` FROM fragment_submissions WHERE sequence=? AND device_id=?`, status.Sequence, deviceID))
	if err != nil || tx.Commit() != nil {
		fail(w, 503, "Capture storage unavailable.")
		return
	}
	reply(w, 200, status)
}
