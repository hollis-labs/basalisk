package account

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/hollis-labs/libs/security/pairing"
)

type captureContextBinding struct {
	SchemaVersion int              `json:"schemaVersion"`
	BindingID     string           `json:"bindingID"`
	CaptureID     string           `json:"captureID"`
	DeviceID      string           `json:"deviceID"`
	Origin        string           `json:"origin"`
	DraftAt       string           `json:"draftAt"`
	State         string           `json:"state"` // active | paused | none
	Draft         *artifactContext `json:"draft"`
}

func captureRequestOrigin(r *http.Request) (string, error) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return pairing.Origin(scheme + "://" + r.Host)
}

func (a *API) captureCapabilities(w http.ResponseWriter, r *http.Request) {
	d, _, ok := a.device(w, r, scopeCapture)
	if !ok {
		return
	}
	reply(w, 200, map[string]any{
		"schemaVersion": 1, "deviceID": d.ID, "scopes": d.Scopes,
		"pairingProtocol": pairing.Version, "captureMetadataVersions": []int{1},
		"captureContext":   map[string]any{"version": 1, "path": "/companion/capture-context", "allowed": slices.Contains(d.Scopes, scopeContext), "nativeHistory": "unavailable"},
		"captureRecovery":  map[string]any{"version": 1, "pageSize": 50, "scope": "own-device", "stopDeletesUpstream": false},
		"captureReadiness": map[string]any{"version": 1, "path": "/companion/capture-readiness", "scope": "own-device"},
		"paths":            map[string]string{"connection": "/companion/fragment-connection", "submit": "/companion/fragments", "status": "/companion/fragments/{captureID}", "list": "/companion/fragments", "retry": "/companion/fragments/{captureID}/retry", "stop": "/companion/fragments/{captureID}/stop"},
		"limits":           map[string]int{"requestBytes": 128 * 1024, "contentBytes": 64 * 1024, "metadataBytes": 16 * 1024, "tags": 32, "references": 32},
		"confirmation":     "saved-with-fragment-id", "deliveryReadiness": "local-evidence-only",
	})
}

func (a *API) captureContext(w http.ResponseWriter, r *http.Request) {
	d, credential, ok := a.device(w, r, scopeCapture)
	if !ok {
		return
	}
	if !slices.Contains(d.Scopes, scopeContext) {
		fail(w, 403, "Activity context permission required.")
		return
	}
	var in struct {
		CaptureID string `json:"captureID"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !validCaptureID(in.CaptureID) {
		fail(w, 400, "Invalid capture ID.")
		return
	}
	origin, err := captureRequestOrigin(r)
	if err != nil {
		fail(w, 400, "Use HTTPS or the configured loopback tunnel origin.")
		return
	}
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "Capture context unavailable.")
		return
	}
	defer tx.Rollback()
	verified, err := a.store.pairing.DeviceInTransaction(r.Context(), tx, credential)
	if err != nil || verified.ID != d.ID || !slices.Contains(verified.Scopes, scopeCapture) || !slices.Contains(verified.Scopes, scopeContext) {
		fail(w, 401, "Application authorization changed.")
		return
	}
	var raw, priorOrigin string
	err = tx.QueryRowContext(r.Context(), `SELECT payload,origin FROM capture_context_bindings WHERE device_id=? AND capture_id=?`, d.ID, in.CaptureID).Scan(&raw, &priorOrigin)
	if err == nil {
		if priorOrigin != origin {
			fail(w, 409, "Capture belongs to a different origin.")
			return
		}
		reply(w, 200, json.RawMessage(raw))
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		fail(w, 503, "Capture context unavailable.")
		return
	}
	var submissions int
	if err = tx.QueryRowContext(r.Context(), `SELECT count(*) FROM fragment_submissions WHERE device_id=? AND capture_id=?`, d.ID, in.CaptureID).Scan(&submissions); err != nil {
		fail(w, 503, "Capture context unavailable.")
		return
	}
	if submissions != 0 {
		fail(w, 409, "Cannot attach context after submission.")
		return
	}
	var total, deviceTotal int
	if err = tx.QueryRowContext(r.Context(), `SELECT count(*),coalesce(sum(device_id=?),0) FROM capture_context_bindings`, d.ID).Scan(&total, &deviceTotal); err != nil {
		fail(w, 503, "Capture context unavailable.")
		return
	}
	if total >= 10000 || deviceTotal >= 1000 {
		fail(w, 409, "Capture context storage is full. Keep your local draft.")
		return
	}
	now := time.Now().UTC()
	stamp := now.Format(timestampFormat)
	result := captureContextBinding{SchemaVersion: 1, BindingID: "ctx_" + token(), CaptureID: in.CaptureID, DeviceID: d.ID, Origin: origin, DraftAt: stamp, State: "none"}
	var sessionID, contextID, project, workstream, status string
	err = tx.QueryRowContext(r.Context(), `SELECT s.id,s.context_id,c.project,c.workstream,s.status FROM activity_sessions s JOIN activity_contexts c ON c.id=s.context_id WHERE s.user_id=? AND s.status!='ended'`, d.OwnerID).Scan(&sessionID, &contextID, &project, &workstream, &status)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		fail(w, 503, "Capture context unavailable.")
		return
	}
	if err == nil {
		result.State = status
		if status == "active" {
			result.Draft = &artifactContext{SchemaVersion: 1, Origin: origin, DeviceID: d.ID, SessionID: sessionID, ContextID: contextID, ContextAsOf: stamp, PublishedAt: stamp, ExpiresAt: now.Add(30 * time.Second).Format(timestampFormat), Project: project, Workstream: workstream, HistoryRefs: []string{}}
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		fail(w, 503, "Capture context unavailable.")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO capture_context_bindings(id,device_id,capture_id,origin,draft_at,payload) VALUES(?,?,?,?,?,?)`, result.BindingID, d.ID, in.CaptureID, origin, stamp, string(encoded))
	if err != nil || tx.Commit() != nil {
		fail(w, 503, "Capture context unavailable.")
		return
	}
	reply(w, 201, result)
}
