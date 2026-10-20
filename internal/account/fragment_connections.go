package account

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"time"
)

// This is an explicit owner grant for one operation, never an HTTP proxy.
// FE authorization is separate: this binding does not mint an FE credential.
type fragmentConnection struct {
	DeviceID      string `json:"deviceId"`
	IntegrationID string `json:"integrationId"`
	Name          string `json:"name"`
	UpdatedAt     string `json:"updatedAt"`
}

func fragmentOrigin(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && validBaseURL(raw) && raw != "" && (u.Path == "" || u.Path == "/")
}

func (a *API) fragmentConnections(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.db.QueryContext(r.Context(), `SELECT c.device_id,c.integration_id,i.name,c.updated_at FROM fragment_connections c JOIN integrations i ON i.id=c.integration_id ORDER BY c.device_id`)
	if err != nil {
		fail(w, 503, "Could not read fragment connections.")
		return
	}
	defer rows.Close()
	items := []fragmentConnection{}
	for rows.Next() {
		var item fragmentConnection
		if rows.Scan(&item.DeviceID, &item.IntegrationID, &item.Name, &item.UpdatedAt) != nil {
			fail(w, 503, "Could not read fragment connections.")
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		fail(w, 503, "Could not read fragment connections.")
		return
	}
	reply(w, 200, items)
}

func (a *API) setFragmentConnection(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IntegrationID string `json:"integrationId"`
	}
	if !decode(w, r, &in) {
		return
	}
	owner, err := a.ownerID(r)
	if err != nil {
		fail(w, 503, "Could not load owner.")
		return
	}
	devices, err := a.store.pairing.List(r.Context(), owner)
	if err != nil {
		fail(w, 503, "Could not read device authorization.")
		return
	}
	allowed := false
	for _, d := range devices {
		if d.ID == r.PathValue("id") && slices.Contains(d.Scopes, scopeCapture) && d.RevokedAt == nil && d.ExpiresAt.After(time.Now()) {
			allowed = true
		}
	}
	if !allowed {
		fail(w, 403, "Pair this device with permission to create fragments first.")
		return
	}
	// Provider and origin are immutable integration metadata. The foreign key
	// also prevents a grant surviving concurrent integration deletion.
	var provider, origin string
	err = a.store.db.QueryRowContext(r.Context(), "SELECT provider,base_url FROM integrations WHERE id=?", in.IntegrationID).Scan(&provider, &origin)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "Integration not found.")
		return
	}
	if err != nil {
		fail(w, 503, "Could not read integration.")
		return
	}
	if provider != "fragments-engine" || !fragmentOrigin(origin) {
		fail(w, 400, "Choose a fragments-engine integration with an HTTPS server origin.")
		return
	}
	updated := time.Now().UTC().Format(timestampFormat)
	_, err = a.store.db.ExecContext(r.Context(), `INSERT INTO fragment_connections(device_id,integration_id,updated_at) VALUES(?,?,?) ON CONFLICT(device_id) DO UPDATE SET integration_id=excluded.integration_id,updated_at=excluded.updated_at`, r.PathValue("id"), in.IntegrationID, updated)
	if err != nil {
		fail(w, 503, "Could not save fragment connection.")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}

func (a *API) removeFragmentConnection(w http.ResponseWriter, r *http.Request) {
	if _, err := a.store.db.ExecContext(r.Context(), "DELETE FROM fragment_connections WHERE device_id=?", r.PathValue("id")); err != nil {
		fail(w, 503, "Could not remove fragment connection.")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}

func (a *API) fragmentConnection(w http.ResponseWriter, r *http.Request) {
	d, _, ok := a.device(w, r, scopeCapture)
	if !ok {
		return
	}
	var item fragmentConnection
	err := a.store.db.QueryRowContext(r.Context(), `SELECT c.device_id,c.integration_id,i.name,c.updated_at FROM fragment_connections c JOIN integrations i ON i.id=c.integration_id WHERE c.device_id=?`, d.ID).Scan(&item.DeviceID, &item.IntegrationID, &item.Name, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 409, "Choose a fragment connection for this Mac in Basalisk.")
		return
	}
	if err != nil {
		fail(w, 503, "Could not read fragment connection.")
		return
	}
	// No URL, secret reference, key or owner-session credential crosses this API.
	reply(w, 200, item)
}
