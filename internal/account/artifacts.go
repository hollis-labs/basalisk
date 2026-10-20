package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/hollis-labs/libs/security/pairing"
)

type artifactContext struct {
	SchemaVersion   int      `json:"schemaVersion"`
	Origin          string   `json:"origin"`
	DeviceID        string   `json:"deviceID"`
	SessionID       string   `json:"sessionID"`
	ContextID       string   `json:"contextID"`
	ContextAsOf     string   `json:"contextAsOf"`
	PublishedAt     string   `json:"publishedAt"`
	ExpiresAt       string   `json:"expiresAt"`
	Project         string   `json:"project"`
	Workstream      string   `json:"workstream"`
	PrecedingApp    *string  `json:"precedingApp,omitempty"`
	PrecedingBundle *string  `json:"precedingBundle,omitempty"`
	HistoryRefs     []string `json:"historyRefs"`
}
type artifactReceipt struct {
	CaptureID          string           `json:"captureID"`
	DraftAt            string           `json:"draftAt"`
	SavedAt            string           `json:"savedAt"`
	FragmentID         string           `json:"fragmentID"`
	FragmentRevisionID *string          `json:"fragmentRevisionID,omitempty"`
	FragmentOrigin     string           `json:"fragmentOrigin"`
	Draft              artifactContext  `json:"draft"`
	SaveContext        *artifactContext `json:"saveContext,omitempty"`
}

func validArtifactContext(ctx context.Context, tx *sql.Tx, device pairing.Device, c artifactContext, at time.Time) bool {
	asof, e1 := time.Parse(time.RFC3339Nano, c.ContextAsOf)
	published, e2 := time.Parse(time.RFC3339Nano, c.PublishedAt)
	expires, e3 := time.Parse(time.RFC3339Nano, c.ExpiresAt)
	origin, e4 := pairing.Origin(c.Origin)
	if c.SchemaVersion != 1 || c.DeviceID != device.ID || e1 != nil || e2 != nil || e3 != nil || e4 != nil || origin != c.Origin || asof.After(at) || published.After(at) || expires.Before(at) || at.Sub(asof) > 30*time.Second || len(c.HistoryRefs) > 50 || !metadataOK(c.Project, 200) || !metadataOK(c.Workstream, 200) {
		return false
	}
	if c.PrecedingApp != nil && !metadataOK(*c.PrecedingApp, 200) {
		return false
	}
	if c.PrecedingBundle != nil && !metadataOK(*c.PrecedingBundle, 255) {
		return false
	}
	for _, ref := range c.HistoryRefs {
		if len(ref) > 250 || !metadataOK(ref, 250) || !strings.HasPrefix(ref, device.ID+":") {
			return false
		}
	}
	var owner, project, workstream string
	if tx.QueryRowContext(ctx, `SELECT s.user_id,c.project,c.workstream FROM activity_sessions s JOIN activity_events e ON e.session_id=s.id JOIN activity_contexts c ON c.id=e.context_id WHERE s.id=? AND c.id=? LIMIT 1`, c.SessionID, c.ContextID).Scan(&owner, &project, &workstream) != nil || owner != device.OwnerID || project != c.Project || workstream != c.Workstream {
		return false
	}
	id, phase, err := activityAt(ctx, tx, c.SessionID, at)
	return err == nil && id == c.ContextID && (phase == "started" || phase == "resumed")
}

func (a *API) ingestArtifact(w http.ResponseWriter, r *http.Request) {
	d, token, ok := a.device(w, r, scopeObserve)
	if !ok {
		return
	}
	var receipt artifactReceipt
	if !decode(w, r, &receipt) {
		return
	}
	if strings.HasPrefix(receipt.CaptureID, "broker:") {
		fail(w, 400, "Reserved capture identity.")
		return
	}
	draft, e1 := time.Parse(time.RFC3339Nano, receipt.DraftAt)
	saved, e2 := time.Parse(time.RFC3339Nano, receipt.SavedAt)
	origin, e3 := pairing.Origin(receipt.FragmentOrigin)
	if e1 != nil || e2 != nil || e3 != nil || origin != receipt.FragmentOrigin || draft.After(saved) || saved.After(time.Now().Add(30*time.Second)) || len(receipt.CaptureID) < 16 || !metadataOK(receipt.CaptureID, 100) || receipt.FragmentID == "" || !metadataOK(receipt.FragmentID, 200) || (receipt.FragmentRevisionID != nil && !metadataOK(*receipt.FragmentRevisionID, 200)) {
		fail(w, 400, "Invalid artifact receipt.")
		return
	}
	raw, _ := json.Marshal(receipt)
	hash := digest(string(raw))
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "Artifact storage unavailable.")
		return
	}
	defer tx.Rollback()
	verified, err := a.store.pairing.DeviceInTransaction(r.Context(), tx, token)
	if err != nil || verified.ID != d.ID || !slices.Contains(verified.Scopes, scopeObserve) {
		fail(w, 401, "Device authorization changed.")
		return
	}
	var old string
	err = tx.QueryRowContext(r.Context(), "SELECT payload_hash FROM companion_artifacts WHERE device_id=? AND capture_id=?", d.ID, receipt.CaptureID).Scan(&old)
	if err == nil {
		if old != hash {
			fail(w, 409, "Capture ID already has a different receipt.")
			return
		}
		reply(w, 200, map[string]bool{"saved": true})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		fail(w, 503, "Artifact storage unavailable.")
		return
	}
	if !validArtifactContext(r.Context(), tx, d, receipt.Draft, draft) {
		fail(w, 400, "Draft context could not be verified.")
		return
	}
	// Save-time context is optional evidence. A stale snapshot never replaces the
	// independently validated draft context or prevents recording that binding.
	if receipt.SaveContext != nil && !validArtifactContext(r.Context(), tx, d, *receipt.SaveContext, saved) {
		receipt.SaveContext = nil
	}
	stored, _ := json.Marshal(receipt)
	_, err = tx.ExecContext(r.Context(), "INSERT INTO companion_artifacts(device_id,capture_id,session_id,payload_hash,payload,received_at) VALUES(?,?,?,?,?,?)", d.ID, receipt.CaptureID, receipt.Draft.SessionID, hash, string(stored), time.Now().UTC().Format(timestampFormat))
	if err != nil || tx.Commit() != nil {
		fail(w, 503, "Artifact storage unavailable.")
		return
	}
	reply(w, 200, map[string]bool{"saved": true})
}

func (a *API) artifacts(w http.ResponseWriter, r *http.Request) {
	before, ok := beforeCursor(w, r)
	if !ok {
		return
	}
	if before == 0 {
		before = 1 << 62
	}
	rows, err := a.store.db.QueryContext(r.Context(), "SELECT sequence,payload,received_at,source FROM companion_artifacts WHERE session_id=? AND sequence<? ORDER BY sequence DESC LIMIT 51", r.PathValue("id"), before)
	if err != nil {
		fail(w, 503, "Could not read artifact links.")
		return
	}
	defer rows.Close()
	type item struct {
		Sequence   int64           `json:"sequence"`
		Receipt    artifactReceipt `json:"receipt"`
		ReceivedAt string          `json:"receivedAt"`
		Source     string          `json:"source"`
	}
	items := []item{}
	var next int64
	for rows.Next() {
		var it item
		var raw string
		if rows.Scan(&it.Sequence, &raw, &it.ReceivedAt, &it.Source) != nil || json.Unmarshal([]byte(raw), &it.Receipt) != nil {
			fail(w, 503, "Could not read artifact links.")
			return
		}
		if len(items) == 50 {
			next = items[len(items)-1].Sequence
			break
		}
		items = append(items, it)
	}
	if rows.Err() != nil {
		fail(w, 503, "Could not read artifact links.")
		return
	}
	reply(w, 200, map[string]any{"items": items, "nextBefore": next})
}
