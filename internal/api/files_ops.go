package api

import (
	"errors"
	"net/http"

	"github.com/hollis-labs/basalisk/internal/files"
)

// File operations and places (internal/files/ops.go, places.go): create, move, copy, delete,
// search, and adding or removing a remote place. Every change needs the write scope; nothing
// is overwritten (a taken name is 409).

func (a *API) filesOpsRoutes() []route {
	r, w := ScopeRead, ScopeWrite
	return []route{
		{"POST", "/api/files/mkdir", w, a.filesMkdir},
		{"POST", "/api/files/create", w, a.filesCreate},
		{"POST", "/api/files/move", w, a.filesMove},
		{"POST", "/api/files/copy", w, a.filesCopy},
		{"POST", "/api/files/delete", w, a.filesDelete},
		{"GET", "/api/files/search", r, a.filesSearch},
		{"GET", "/api/files/grep", r, a.filesGrep},
		{"GET", "/api/files/notes-index", r, a.filesNotes},
		{"GET", "/api/files/hosts", r, a.filesHosts},
		{"POST", "/api/files/roots", w, a.filesAddRoot},
		{"DELETE", "/api/files/roots/{id}", w, a.filesRemoveRoot},
	}
}

func opsFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, files.ErrExists):
		fail(w, 409, "An item with that name already exists.")
	case errors.Is(err, files.ErrTooMany):
		fail(w, 413, "Too many items to do that at once.")
	default:
		filesFail(w, err)
	}
}

func (a *API) filesMkdir(w http.ResponseWriter, r *http.Request) {
	var in files.Ref
	if !decode(w, r, &in) {
		return
	}
	e, err := a.files.Mkdir(r.Context(), in.Root, in.Path)
	if err != nil {
		opsFail(w, err)
		return
	}
	reply(w, 201, e)
}

func (a *API) filesCreate(w http.ResponseWriter, r *http.Request) {
	var in files.Ref
	if !decode(w, r, &in) {
		return
	}
	e, err := a.files.Create(r.Context(), in.Root, in.Path)
	if err != nil {
		opsFail(w, err)
		return
	}
	reply(w, 201, e)
}

type fromTo struct {
	From files.Ref `json:"from"`
	To   files.Ref `json:"to"`
}

func (a *API) filesMove(w http.ResponseWriter, r *http.Request) {
	var in fromTo
	if !decode(w, r, &in) {
		return
	}
	e, err := a.files.Move(r.Context(), in.From, in.To)
	if err != nil {
		opsFail(w, err)
		return
	}
	reply(w, 200, e)
}

func (a *API) filesCopy(w http.ResponseWriter, r *http.Request) {
	var in fromTo
	if !decode(w, r, &in) {
		return
	}
	e, err := a.files.Copy(r.Context(), in.From, in.To)
	if err != nil {
		opsFail(w, err)
		return
	}
	reply(w, 201, e)
}

// filesDelete answers {trashed}: true when the item went to the Trash (local places), false
// when it is gone for good (remote places).
func (a *API) filesDelete(w http.ResponseWriter, r *http.Request) {
	var in files.Ref
	if !decode(w, r, &in) {
		return
	}
	trashed, err := a.files.Delete(r.Context(), in.Root, in.Path)
	if err != nil {
		opsFail(w, err)
		return
	}
	reply(w, 200, map[string]bool{"trashed": trashed})
}

func (a *API) filesSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := a.files.Search(r.Context(), q.Get("root"), q.Get("path"), q.Get("q"))
	if err != nil {
		opsFail(w, err)
		return
	}
	reply(w, 200, res)
}

// filesGrep searches file contents (ripgrep where the place has it).
func (a *API) filesGrep(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := a.files.Grep(r.Context(), q.Get("root"), q.Get("path"), q.Get("q"))
	if err != nil {
		if !errors.Is(err, files.ErrOutside) && !errors.Is(err, files.ErrNoRoot) && !errors.Is(err, files.ErrNotFound) && !errors.Is(err, files.ErrNotDir) {
			fail(w, 400, capitalize(err.Error())+".")
			return
		}
		opsFail(w, err)
		return
	}
	reply(w, 200, res)
}

// filesNotes is the notes index under a folder (internal/files/notes.go), for [[wiki links]].
func (a *API) filesNotes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := a.files.Notes(r.Context(), q.Get("root"), q.Get("path"))
	if err != nil {
		opsFail(w, err)
		return
	}
	reply(w, 200, res)
}

func (a *API) filesHosts(w http.ResponseWriter, _ *http.Request) {
	reply(w, 200, map[string]any{"hosts": a.files.Hosts()})
}

func (a *API) filesAddRoot(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Host  string `json:"host"`
		Path  string `json:"path"`
		Label string `json:"label"`
	}
	if !decode(w, r, &in) {
		return
	}
	root, err := a.files.AddRemote(files.RootConfig{Host: in.Host, Path: in.Path, Label: in.Label})
	switch {
	case errors.Is(err, files.ErrExists):
		fail(w, 409, "That place is already in the list.")
	case err != nil:
		fail(w, 400, capitalize(err.Error())+".")
	default:
		reply(w, 201, root)
	}
}

func (a *API) filesRemoveRoot(w http.ResponseWriter, r *http.Request) {
	err := a.files.RemoveRoot(r.PathValue("id"))
	switch {
	case errors.Is(err, files.ErrNoRoot):
		fail(w, 404, "No such place.")
	case err != nil:
		fail(w, 400, capitalize(err.Error())+".")
	default:
		w.WriteHeader(204)
	}
}
