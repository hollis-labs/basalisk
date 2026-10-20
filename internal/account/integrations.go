package account

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/libs/security/secrets"
)

type Integration struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Provider          string `json:"provider"`
	BaseURL           string `json:"baseUrl"`
	UpdatedAt         string `json:"updatedAt"`
	CredentialPresent bool   `json:"credentialPresent"`
}

func (a *API) integrations(w http.ResponseWriter, r *http.Request) {
	rows, e := a.store.db.Query("SELECT id,name,provider,base_url,secret_ref,updated_at FROM integrations ORDER BY name")
	if e != nil {
		fail(w, 500, "Could not load integrations.")
		return
	}
	defer rows.Close()
	list := []Integration{}
	for rows.Next() {
		var i Integration
		var ref string
		if rows.Scan(&i.ID, &i.Name, &i.Provider, &i.BaseURL, &ref, &i.UpdatedAt) != nil {
			fail(w, 500, "Could not load integrations.")
			return
		}
		_, e = a.store.secrets.Resolve(r.Context(), ref)
		if e != nil && !errors.Is(e, secrets.ErrNotFound) {
			fail(w, 503, "Credential storage is unavailable.")
			return
		}
		i.CredentialPresent = e == nil
		list = append(list, i)
	}
	if rows.Err() != nil {
		fail(w, 500, "Could not load integrations.")
		return
	}
	reply(w, 200, list)
}
func validCredential(s string) bool { return strings.TrimSpace(s) != "" && len(s) <= 8192 }
func (a *API) createIntegration(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name       string `json:"name"`
		Provider   string `json:"provider"`
		BaseURL    string `json:"baseUrl"`
		Credential string `json:"credential"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Provider = strings.TrimSpace(in.Provider)
	in.BaseURL = strings.TrimSpace(in.BaseURL)
	if in.Name == "" || len(in.Name) > 100 || in.Provider == "" || len(in.Provider) > 100 || !validBaseURL(in.BaseURL) || !validCredential(in.Credential) {
		fail(w, 400, "Enter a name, provider, API key, and an optional HTTPS URL without credentials or query parameters.")
		return
	}
	id := token()
	ref := "file://basalisk/" + id
	if e := a.store.secrets.Set(r.Context(), ref, strings.NewReader(in.Credential)); e != nil {
		fail(w, 503, "Could not store credential.")
		return
	}
	i := Integration{ID: id, Name: in.Name, Provider: in.Provider, BaseURL: in.BaseURL, UpdatedAt: time.Now().UTC().Format(time.RFC3339), CredentialPresent: true}
	_, e := a.store.db.Exec("INSERT INTO integrations(id,name,provider,base_url,secret_ref,updated_at) VALUES(?,?,?,?,?,?)", i.ID, i.Name, i.Provider, i.BaseURL, ref, i.UpdatedAt)
	if e != nil {
		_ = a.store.secrets.Delete(r.Context(), ref)
		fail(w, 500, "Could not save integration.")
		return
	}
	reply(w, 201, i)
}
func (a *API) integrationRef(w http.ResponseWriter, r *http.Request) string {
	var ref string
	e := a.store.db.QueryRow("SELECT secret_ref FROM integrations WHERE id=?", r.PathValue("id")).Scan(&ref)
	if errors.Is(e, sql.ErrNoRows) {
		fail(w, 404, "Integration not found.")
		return ""
	}
	if e != nil {
		fail(w, 500, "Could not load integration.")
		return ""
	}
	return ref
}
func (a *API) rotateIntegration(w http.ResponseWriter, r *http.Request) {
	// Serialize credential replacement/removal so a concurrent delete cannot leave
	// a newly written key orphaned after its metadata is removed.
	a.mu.Lock()
	defer a.mu.Unlock()
	var in struct {
		Credential string `json:"credential"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !validCredential(in.Credential) {
		fail(w, 400, "Enter an API key of at most 8192 bytes.")
		return
	}
	ref := a.integrationRef(w, r)
	if ref == "" {
		return
	}
	if e := a.store.secrets.Set(r.Context(), ref, strings.NewReader(in.Credential)); e != nil {
		fail(w, 503, "Could not replace credential.")
		return
	}
	if _, e := a.store.db.Exec("UPDATE integrations SET updated_at=? WHERE id=?", time.Now().UTC().Format(time.RFC3339), r.PathValue("id")); e != nil {
		fail(w, 500, "Credential was replaced, but metadata could not be updated. Reload before continuing.")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
func (a *API) deleteIntegration(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ref := a.integrationRef(w, r)
	if ref == "" {
		return
	}
	if e := a.store.secrets.Delete(r.Context(), ref); e != nil && !errors.Is(e, secrets.ErrNotFound) {
		fail(w, 503, "Could not remove credential.")
		return
	}
	if _, e := a.store.db.Exec("DELETE FROM integrations WHERE id=?", r.PathValue("id")); e != nil {
		fail(w, 500, "Credential was removed, but metadata remains. Reload and try removing the record again.")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
