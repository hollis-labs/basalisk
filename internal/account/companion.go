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
	"unicode"

	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/libs/security/pairing"
)

const scopeContext = "basalisk.activity.read"
const scopeObserve = "basalisk.observations.write"
const scopeCapture = "basalisk.fragments.create"
const observationRetention = 5 * time.Minute
const timestampFormat = "2006-01-02T15:04:05.000000000Z"

func (a *API) ownerID(r *http.Request) (string, error) {
	state, e := a.store.activity.State(r.Context())
	return state.Identity.UserID, e
}
func (a *API) deviceGrant(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Label          string `json:"label"`
		AllowFragments bool   `json:"allowFragments"`
		Profile        string `json:"profile,omitempty"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Profile != "" && in.Profile != "companion" && in.Profile != "capture" {
		fail(w, 400, "Choose a companion or capture application.")
		return
	}
	owner, e := a.ownerID(r)
	if e != nil {
		fail(w, 500, "Could not load owner.")
		return
	}
	scopes := []string{scopeContext, scopeObserve}
	if in.Profile == "capture" {
		scopes = []string{scopeContext, scopeCapture}
	} else if in.AllowFragments {
		scopes = append(scopes, scopeCapture)
	}
	g, e := a.store.pairing.CreateGrant(r.Context(), owner, strings.TrimSpace(in.Label), scopes)
	if errors.Is(e, pairing.ErrInput) {
		fail(w, 400, "Enter a device name of at most 128 bytes.")
		return
	}
	if e != nil {
		fail(w, 503, "Could not create pairing grant.")
		return
	}
	reply(w, 201, g)
}
func (a *API) devices(w http.ResponseWriter, r *http.Request) {
	owner, e := a.ownerID(r)
	if e != nil {
		fail(w, 500, "Could not load owner.")
		return
	}
	devices, e := a.store.pairing.List(r.Context(), owner)
	if e != nil {
		fail(w, 503, "Could not list devices.")
		return
	}
	reply(w, 200, devices)
}
func (a *API) revokeDevice(w http.ResponseWriter, r *http.Request) {
	owner, e := a.ownerID(r)
	if e == nil {
		e = a.store.pairing.Revoke(r.Context(), owner, r.PathValue("id"))
	}
	if e != nil {
		fail(w, 400, "Could not revoke device.")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
func (a *API) revokeGrant(w http.ResponseWriter, r *http.Request) {
	owner, e := a.ownerID(r)
	if e == nil {
		e = a.store.pairing.RevokeGrant(r.Context(), owner, r.PathValue("id"))
	}
	if e != nil {
		fail(w, 503, "Could not revoke grant.")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}

// CompanionHandler deliberately has no cookie fallback. The app's root handler
// still requires a loopback Host; remote clients use an authenticated SSH tunnel.
func (s *Store) CompanionHandler() http.Handler {
	a := &API{store: s}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/pair/exchange", a.exchangeDevice)
	mux.HandleFunc("POST /auth/renew", a.renewDevice)
	mux.HandleFunc("GET /companion/context", a.companionContext)
	mux.HandleFunc("GET /companion/capture-capabilities", a.captureCapabilities)
	mux.HandleFunc("GET /companion/capture-readiness", a.captureReadiness)
	mux.HandleFunc("POST /companion/capture-context", a.captureContext)
	mux.HandleFunc("POST /companion/observations", a.ingestObservations)
	mux.HandleFunc("POST /companion/artifacts", a.ingestArtifact)
	mux.HandleFunc("GET /companion/fragment-connection", a.fragmentConnection)
	mux.HandleFunc("POST /companion/fragments", a.enqueueFragment)
	mux.HandleFunc("GET /companion/fragments", a.captureSubmissions)
	mux.HandleFunc("GET /companion/fragments/{id}", a.fragmentSubmissionStatus)
	mux.HandleFunc("POST /companion/fragments/{id}/retry", a.retryCapture)
	mux.HandleFunc("POST /companion/fragments/{id}/stop", a.stopCapture)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		origin := r.Header.Get("Origin")
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if (origin != "" && origin != scheme+"://"+r.Host) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			fail(w, 403, "Origin not accepted.")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/auth/") && r.Header.Get(pairing.ProtocolHeader) != pairing.Version {
			fail(w, 400, "Pairing protocol 1 is required.")
			return
		}
		if r.URL.Path == pairing.ExchangePath || r.URL.Path == pairing.RenewPath {
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
		}
		mux.ServeHTTP(w, r)
	})
}
func (a *API) exchangeDevice(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	if time.Since(a.pairWindow) > time.Minute {
		a.pairWindow = time.Now()
		a.pairAttempts = 0
	}
	a.pairAttempts++
	limited := a.pairAttempts > 10
	a.mu.Unlock()
	if limited {
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "Pairing rate limit. Try again in a minute.")
		return
	}
	var in pairing.ExchangeRequest
	if !decode(w, r, &in) {
		return
	}
	x, e := a.store.pairing.Exchange(r.Context(), in)
	if errors.Is(e, pairing.ErrGrant) {
		fail(w, 401, "Invalid or inactive pairing grant.")
		return
	}
	if e != nil {
		fail(w, 503, "Pairing is unavailable.")
		return
	}
	reply(w, 200, x)
}
func (a *API) device(w http.ResponseWriter, r *http.Request, scope string) (pairing.Device, string, bool) {
	token, state := auth.ParseBearer(r.Header.Values("Authorization"))
	if state != "" {
		fail(w, 401, "Device credential required.")
		return pairing.Device{}, "", false
	}
	d, e := a.store.pairing.Device(r.Context(), token)
	if errors.Is(e, auth.ErrInvalidToken) {
		fail(w, 401, "Device credential expired or revoked. Pair again.")
		return d, "", false
	}
	if e != nil {
		fail(w, 503, "Device verification unavailable.")
		return d, "", false
	}
	if scope != "" && !slices.Contains(d.Scopes, scope) {
		fail(w, 403, "Device scope is insufficient.")
		return d, "", false
	}
	return d, token, true
}
func (a *API) renewDevice(w http.ResponseWriter, r *http.Request) {
	_, token, ok := a.device(w, r, "")
	if !ok {
		return
	}
	var in struct {
		Scopes []string `json:"scopes,omitempty"`
	}
	if !decode(w, r, &in) {
		return
	}
	d, e := a.store.pairing.Renew(r.Context(), token, in.Scopes)
	if errors.Is(e, auth.ErrInvalidToken) {
		fail(w, 401, "Device credential cannot renew.")
		return
	}
	if errors.Is(e, pairing.ErrInput) {
		fail(w, 400, "Invalid scopes.")
		return
	}
	if e != nil {
		fail(w, 503, "Renewal unavailable.")
		return
	}
	reply(w, 200, d)
}
func (a *API) companionContext(w http.ResponseWriter, r *http.Request) {
	d, _, ok := a.device(w, r, scopeContext)
	if !ok {
		return
	}
	state, e := a.store.activity.State(r.Context())
	if e != nil {
		fail(w, 503, "Activity unavailable.")
		return
	}
	if d.OwnerID != state.Identity.UserID {
		fail(w, 403, "Device does not belong to this owner.")
		return
	}
	reply(w, 200, map[string]any{"current": state.Current, "asOf": time.Now().UTC().Format(timestampFormat), "retentionSeconds": int(observationRetention.Seconds())})
}

type observation struct {
	Sequence    int64  `json:"sequence"`
	OccurredAt  string `json:"occurredAt"`
	Kind        string `json:"kind"`
	BundleID    string `json:"bundleId"`
	AppName     string `json:"appName"`
	SessionID   string `json:"activitySessionId"`
	ContextID   string `json:"contextId"`
	ContextAsOf string `json:"contextAsOf"`
}
type observationBatch struct {
	Generation string        `json:"generation"`
	Events     []observation `json:"events"`
}

func metadataOK(s string, max int) bool {
	return len(s) <= max && !strings.ContainsFunc(s, unicode.IsControl)
}
func (a *API) ingestObservations(w http.ResponseWriter, r *http.Request) {
	d, credential, ok := a.device(w, r, scopeObserve)
	if !ok {
		return
	}
	var batch observationBatch
	if !decode(w, r, &batch) {
		return
	}
	if len(batch.Generation) < 16 || !metadataOK(batch.Generation, 100) || len(batch.Events) == 0 || len(batch.Events) > 20 {
		fail(w, 400, "Invalid observation batch.")
		return
	}
	now := time.Now().UTC()
	for _, e := range batch.Events {
		at, err := time.Parse(time.RFC3339Nano, e.OccurredAt)
		asOf, contextErr := time.Parse(time.RFC3339Nano, e.ContextAsOf)
		if err != nil || contextErr != nil || at.After(now.Add(30*time.Second)) || asOf.After(at) || at.Sub(asOf) > 30*time.Second || e.Sequence <= 0 || !metadataOK(e.BundleID, 255) || !metadataOK(e.AppName, 200) || len(e.SessionID) > 100 || len(e.ContextID) > 100 || !slices.Contains([]string{"app.activated", "system.sleep", "system.wake"}, e.Kind) {
			fail(w, 400, "Invalid observation or stale context.")
			return
		}
	}
	tx, err := a.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 503, "Observation storage unavailable.")
		return
	}
	defer tx.Rollback()
	verified, err := a.store.pairing.DeviceInTransaction(r.Context(), tx, credential)
	if err != nil || verified.ID != d.ID || !slices.Contains(verified.Scopes, scopeObserve) {
		fail(w, 401, "Device authorization changed.")
		return
	}
	accepted, ignored := 0, 0
	for _, event := range batch.Events {
		at, _ := time.Parse(time.RFC3339Nano, event.OccurredAt)
		if at.Before(now.Add(-observationRetention)) {
			ignored++
			continue
		}
		raw, _ := json.Marshal(event)
		hash := digest(string(raw))
		var previous string
		err = tx.QueryRowContext(r.Context(), "SELECT payload_hash FROM companion_observations WHERE device_id=? AND generation=? AND sequence=?", d.ID, batch.Generation, event.Sequence).Scan(&previous)
		if err == nil {
			if previous != hash {
				fail(w, 409, "Observation sequence was reused with different data.")
				return
			}
			accepted++
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			fail(w, 503, "Observation storage unavailable.")
			return
		}
		var owner string
		err = tx.QueryRowContext(r.Context(), "SELECT user_id FROM activity_sessions WHERE id=?", event.SessionID).Scan(&owner)
		if errors.Is(err, sql.ErrNoRows) {
			ignored++
			continue
		}
		if err != nil {
			fail(w, 503, "Activity unavailable.")
			return
		}
		if owner != d.OwnerID {
			fail(w, 403, "Session belongs to another owner.")
			return
		}
		contextID, phase, err := activityAt(r.Context(), tx, event.SessionID, at)
		if err != nil {
			fail(w, 503, "Activity unavailable.")
			return
		}
		if contextID != event.ContextID || (phase != "started" && phase != "resumed") {
			ignored++
			continue
		}
		_, err = tx.ExecContext(r.Context(), `INSERT INTO companion_observations VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, d.ID, batch.Generation, event.Sequence, hash, at.UTC().Format(timestampFormat), now.Format(timestampFormat), event.Kind, event.BundleID, event.AppName, event.SessionID, event.ContextID, event.ContextAsOf)
		if err != nil {
			fail(w, 503, "Could not save observations.")
			return
		}
		accepted++
	}
	if err = tx.Commit(); err != nil {
		fail(w, 503, "Could not save observations.")
		return
	}
	reply(w, 200, map[string]int{"accepted": accepted, "ignored": ignored})
}

// Parse timestamps rather than using SQLite's millisecond date arithmetic:
// focus and pause/context changes may happen within the same millisecond.
func activityAt(ctx context.Context, tx *sql.Tx, session string, at time.Time) (string, string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT context_id,kind,occurred_at FROM activity_events WHERE session_id=? ORDER BY sequence DESC", session)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	contextID, phase := "", ""
	for rows.Next() {
		var id, kind, raw string
		if err = rows.Scan(&id, &kind, &raw); err != nil {
			return "", "", err
		}
		occurred, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return "", "", err
		}
		if occurred.After(at) {
			continue
		}
		if contextID == "" {
			contextID = id
		}
		if kind != "context_changed" {
			phase = kind
			break
		}
	}
	return contextID, phase, rows.Err()
}

func (s *Store) PruneObservations(ctx context.Context) {
	prune := func() {
		_, _ = s.db.ExecContext(ctx, "DELETE FROM companion_observations WHERE occurred_at<?", time.Now().UTC().Add(-observationRetention).Format(timestampFormat))
	}
	prune()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}
func (a *API) observations(w http.ResponseWriter, r *http.Request) {
	rows, e := a.store.db.QueryContext(r.Context(), `SELECT device_id||':'||generation||':'||sequence,device_id,occurred_at,received_at,kind,bundle_id,app_name,session_id,context_id,context_as_of FROM companion_observations WHERE occurred_at>=? ORDER BY occurred_at DESC LIMIT 100`, time.Now().UTC().Add(-observationRetention).Format(timestampFormat))
	if e != nil {
		fail(w, 503, "Could not read observations.")
		return
	}
	defer rows.Close()
	items := []map[string]string{}
	for rows.Next() {
		var id, device, at, received, kind, bundle, name, session, ctx, asof string
		if e = rows.Scan(&id, &device, &at, &received, &kind, &bundle, &name, &session, &ctx, &asof); e != nil {
			fail(w, 503, "Could not read observations.")
			return
		}
		items = append(items, map[string]string{"id": id, "deviceId": device, "occurredAt": at, "receivedAt": received, "kind": kind, "bundleId": bundle, "appName": name, "activitySessionId": session, "contextId": ctx, "contextAsOf": asof, "source": "macos.workspace", "attribution": "device-reported"})
	}
	if e = rows.Err(); e != nil {
		fail(w, 503, "Could not read observations.")
		return
	}
	reply(w, 200, items)
}
