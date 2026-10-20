package account

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/hollis-labs/libs/security/pairing"
)

// Runtime observation belongs to this process. Never persist a running flag
// across restarts or infer remote FE health from it. No secret is resolved here.
type deliveryActivity struct {
	IntegrationID   string `json:"integrationID,omitempty"`
	State           string `json:"state"`
	ObservedAt      string `json:"observedAt,omitempty"`
	ValidForSeconds int    `json:"validForSeconds"`
}

func (s *Store) setDeliveryActivity(integrationID, state string) {
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	s.delivery = deliveryActivity{integrationID, state, time.Now().UTC().Format(timestampFormat), 60}
}

func (s *Store) deliveryActivityAt(now time.Time) deliveryActivity {
	s.deliveryMu.RLock()
	defer s.deliveryMu.RUnlock()
	x := s.delivery
	if x.State == "" {
		x.State = "not_configured"
	}
	if x.State == "running" {
		at, err := time.Parse(time.RFC3339Nano, x.ObservedAt)
		if err != nil || now.Before(at) || now.Sub(at) > time.Duration(x.ValidForSeconds)*time.Second {
			x.State = "stale"
		}
	}
	return x
}

type captureQueueCounts struct {
	Queued         int `json:"queued"`
	NeedsAttention int `json:"needsAttention"`
	Saved          int `json:"saved"`
	Stopped        int `json:"stopped"`
	Delivering     int `json:"delivering"` // subset of queued
}

type captureConnectionReadiness struct {
	State         string `json:"state"`
	IntegrationID string `json:"integrationID,omitempty"`
	Name          string `json:"name,omitempty"`
}

type captureAppReadiness struct {
	DeviceID       string                     `json:"deviceID"`
	Label          string                     `json:"label"`
	Authorization  string                     `json:"authorization"`
	ContextAllowed bool                       `json:"contextAllowed"`
	Connection     captureConnectionReadiness `json:"connection"`
	Worker         deliveryActivity           `json:"worker"`
	Queue          captureQueueCounts         `json:"queue"`
	LastConfirmed  *captureDeliveryEvidence   `json:"lastConfirmed,omitempty"`
	LatestPending  *captureDeliveryEvidence   `json:"latestPending,omitempty"`
}

// Status observations deliberately omit provider URLs, payloads and credentials.
type captureDeliveryEvidence struct {
	CaptureID          string `json:"captureID"`
	IntegrationID      string `json:"integrationID"`
	UpdatedAt          string `json:"updatedAt"`
	Code               string `json:"code"`
	FragmentID         string `json:"fragmentID,omitempty"`
	FragmentRevisionID string `json:"fragmentRevisionID,omitempty"`
}

type captureReadinessSnapshot struct {
	SchemaVersion   int                   `json:"schemaVersion"`
	GeneratedAt     string                `json:"generatedAt"`
	ValidForSeconds int                   `json:"validForSeconds"`
	Source          string                `json:"source"`
	UpstreamHealth  string                `json:"upstreamHealth"`
	PortfolioHealth string                `json:"portfolioHealth"`
	Apps            []captureAppReadiness `json:"apps"`
	NextAfter       string                `json:"nextAfter,omitempty"`
}

func newReadinessSnapshot(now time.Time) captureReadinessSnapshot {
	return captureReadinessSnapshot{SchemaVersion: 1, GeneratedAt: now.UTC().Format(timestampFormat), ValidForSeconds: 30, Source: "basalisk", UpstreamHealth: "not_probed", PortfolioHealth: "not_connected", Apps: []captureAppReadiness{}}
}

func (a *API) readCaptureReadiness(ctx context.Context, tx *sql.Tx, d pairing.Device, now time.Time) (captureAppReadiness, error) {
	x := captureAppReadiness{DeviceID: d.ID, Label: d.Label, Authorization: "active", ContextAllowed: slices.Contains(d.Scopes, scopeContext), Connection: captureConnectionReadiness{State: "missing"}}
	switch {
	case d.RevokedAt != nil:
		x.Authorization = "revoked"
	case !d.ExpiresAt.After(now):
		x.Authorization = "expired"
	case !slices.Contains(d.Scopes, scopeCapture):
		x.Authorization = "capture_not_granted"
	}
	if x.Authorization == "revoked" || x.Authorization == "expired" {
		x.ContextAllowed = false
	}
	var provider, origin string
	err := tx.QueryRowContext(ctx, `SELECT i.id,i.name,i.provider,i.base_url FROM fragment_connections c JOIN integrations i ON i.id=c.integration_id WHERE c.device_id=?`, d.ID).Scan(&x.Connection.IntegrationID, &x.Connection.Name, &provider, &origin)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return x, err
	}
	if err == nil {
		x.Connection.State = "bound"
		if provider != "fragments-engine" || !fragmentOrigin(origin) {
			x.Connection.State = "invalid"
		}
	}
	x.Worker = a.store.deliveryActivityAt(now)
	if x.Worker.State != "not_configured" && (x.Connection.State != "bound" || x.Worker.IntegrationID != x.Connection.IntegrationID) {
		x.Worker = deliveryActivity{State: "not_assigned"}
	}
	// A paired app may learn only whether the worker serves its own binding.
	x.Worker.IntegrationID = ""
	err = tx.QueryRowContext(ctx, `SELECT coalesce(sum(state='queued'),0),coalesce(sum(state='needs_attention'),0),coalesce(sum(state='saved'),0),coalesce(sum(state='discarded'),0),coalesce(sum(state='queued' AND lease_until>?),0) FROM fragment_submissions WHERE device_id=?`, now.Unix(), d.ID).Scan(&x.Queue.Queued, &x.Queue.NeedsAttention, &x.Queue.Saved, &x.Queue.Stopped, &x.Queue.Delivering)
	if err != nil {
		return x, err
	}
	for _, q := range []struct {
		where string
		dst   **captureDeliveryEvidence
	}{
		{"state='saved'", &x.LastConfirmed},
		{"state IN ('queued','needs_attention')", &x.LatestPending},
	} {
		st, err := readSubmission(tx.QueryRowContext(ctx, `SELECT `+submissionColumns+` FROM fragment_submissions WHERE device_id=? AND `+q.where+` ORDER BY updated_at DESC,sequence DESC LIMIT 1`, d.ID))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return x, err
		}
		if err == nil {
			*q.dst = &captureDeliveryEvidence{st.CaptureID, st.IntegrationID, st.UpdatedAt, st.Code, st.FragmentID, st.FragmentRevisionID}
		}
	}
	return x, nil
}

func (a *API) captureReadiness(w http.ResponseWriter, r *http.Request) {
	d, token, ok := a.device(w, r, scopeCapture)
	if !ok {
		return
	}
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "Capture status unavailable.")
		return
	}
	defer tx.Rollback()
	d, err = a.store.pairing.DeviceInTransaction(r.Context(), tx, token)
	if err != nil || !slices.Contains(d.Scopes, scopeCapture) {
		fail(w, 401, "Device authorization changed.")
		return
	}
	now := time.Now()
	snapshot := newReadinessSnapshot(now)
	item, err := a.readCaptureReadiness(r.Context(), tx, d, now)
	if err != nil {
		fail(w, 503, "Capture status unavailable.")
		return
	}
	snapshot.Apps = append(snapshot.Apps, item)
	reply(w, 200, snapshot)
}

func (a *API) ownerCaptureReadiness(w http.ResponseWriter, r *http.Request) {
	owner, err := a.ownerID(r)
	if err != nil {
		fail(w, 503, "Capture status unavailable.")
		return
	}
	devices, err := a.store.pairing.List(r.Context(), owner)
	if err != nil {
		fail(w, 503, "Capture status unavailable.")
		return
	}
	slices.SortFunc(devices, func(a, b pairing.Device) int { return strings.Compare(a.ID, b.ID) })
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "Capture status unavailable.")
		return
	}
	defer tx.Rollback()
	now := time.Now()
	snapshot := newReadinessSnapshot(now)
	for _, d := range devices {
		if d.ID <= r.URL.Query().Get("after") {
			continue
		}
		if len(snapshot.Apps) == 50 {
			snapshot.NextAfter = snapshot.Apps[49].DeviceID
			break
		}
		item, err := a.readCaptureReadiness(r.Context(), tx, d, now)
		if err != nil {
			fail(w, 503, "Capture status unavailable.")
			return
		}
		snapshot.Apps = append(snapshot.Apps, item)
	}
	reply(w, 200, snapshot)
}
