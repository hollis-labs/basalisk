package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
)

// The desktop's settings (theme, layout, shortcuts): one JSON document per person, owned by
// the frontend. The server stores and returns it and does not read inside it, so the
// desktop can add a setting without a schema change. Files, not database rows, because the
// schema is one migration edited in place and an existing database would not get a new table.

// SetDeskDir names the directory the documents are kept in. Without it the settings are
// not kept (GET answers null, PUT is refused).
func (a *API) SetDeskDir(dir string) { a.deskDir = dir }

func (a *API) deskRoutes() []route {
	return []route{
		{"GET", "/api/desk/settings", ScopeRead, a.deskSettings},
		{"PUT", "/api/desk/settings", ScopeWrite, a.setDeskSettings},
	}
}

var deskFileName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,96}$`)

// deskFile is the caller's document. The owner is the verified subject, never a request field.
func (a *API) deskFile(r *http.Request) (string, error) {
	if a.deskDir == "" {
		return "", errors.New("desk settings are not kept by this server")
	}
	owner, err := a.ownerID(r)
	if err != nil {
		return "", err
	}
	if !deskFileName.MatchString(owner) {
		sum := sha256.Sum256([]byte(owner))
		owner = hex.EncodeToString(sum[:16])
	}
	return filepath.Join(a.deskDir, owner+".json"), nil
}

func (a *API) deskSettings(w http.ResponseWriter, r *http.Request) {
	settings := json.RawMessage("null")
	if path, err := a.deskFile(r); err == nil {
		switch data, err := os.ReadFile(path); {
		case err == nil && json.Valid(data):
			settings = data
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			fail(w, 500, "The desk settings could not be read.")
			return
		}
	}
	reply(w, 200, map[string]json.RawMessage{"settings": settings})
}

func (a *API) setDeskSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Settings map[string]json.RawMessage `json:"settings"`
	}
	if !decodeLimit(w, r, &in, 256<<10) {
		return
	}
	if in.Settings == nil {
		fail(w, 400, "The settings must be an object.")
		return
	}
	path, err := a.deskFile(r)
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	data, _ := json.Marshal(in.Settings)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fail(w, 500, "The desk settings could not be saved.")
		return
	}
	// Write beside the file and rename, so a crash never leaves half a document.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil || os.Rename(tmp, path) != nil {
		_ = os.Remove(tmp)
		fail(w, 500, "The desk settings could not be saved.")
		return
	}
	w.WriteHeader(204)
}
