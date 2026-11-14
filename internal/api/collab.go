package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/hollis-labs/basalisk/internal/collab"
)

// Collab (internal/collab, docs/collab.md): the drafts, artifacts and diagrams the owner and
// the agent work on together, kept in Caster with their marks.

// EnableCollab turns the collab routes on.
func (a *API) EnableCollab(store *collab.Store) { a.collab = store }

func (a *API) collabRoutes() []route {
	if a.collab == nil {
		return nil
	}
	r, w := ScopeRead, ScopeWrite
	return []route{
		{"GET", "/api/collab", r, a.collabList},
		{"POST", "/api/collab", w, a.collabCreate},
		{"GET", "/api/collab/{name}", r, a.collabGet},
		{"PUT", "/api/collab/{name}", w, a.collabSave},
		{"GET", "/api/collab/{name}/log", r, a.collabLog},
		{"POST", "/api/collab/{name}/marks", w, a.collabMark},
	}
}

func collabFail(w http.ResponseWriter, err error) {
	var c *collab.Conflict
	switch {
	case errors.Is(err, collab.ErrNotFound):
		fail(w, 404, "There is no such document.")
	case errors.As(err, &c):
		reply(w, 409, map[string]any{"message": "It changed meanwhile (now revision " + strconv.Itoa(c.Rev) + "). Look at the newer one before saving over it.", "conflict": true, "rev": c.Rev})
	default:
		fail(w, 502, "Caster could not be reached or refused: "+err.Error())
	}
}

func (a *API) collabOwner(w http.ResponseWriter, r *http.Request) bool {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return false
	}
	return true
}

func (a *API) collabList(w http.ResponseWriter, r *http.Request) {
	if !a.collabOwner(w, r) {
		return
	}
	docs, err := a.collab.List(r.Context(), r.URL.Query().Get("kind"))
	if err != nil {
		collabFail(w, err)
		return
	}
	reply(w, 200, map[string]any{"docs": docs})
}

func (a *API) collabCreate(w http.ResponseWriter, r *http.Request) {
	if !a.collabOwner(w, r) {
		return
	}
	var in struct {
		collab.Doc
		By string `json:"by"`
	}
	if !decodeLimit(w, r, &in, 4<<20) {
		return
	}
	d, err := a.collab.Create(r.Context(), in.Doc, in.By)
	if err != nil {
		collabFail(w, err)
		return
	}
	reply(w, 200, map[string]any{"doc": d, "marks": []collab.Mark{}})
}

func (a *API) collabGet(w http.ResponseWriter, r *http.Request) {
	if !a.collabOwner(w, r) {
		return
	}
	rev, _ := strconv.Atoi(r.URL.Query().Get("rev"))
	name := r.PathValue("name")
	d, err := a.collab.Get(r.Context(), name, rev)
	if err != nil {
		collabFail(w, err)
		return
	}
	marks, err := a.collab.Marks(r.Context(), name)
	if err != nil {
		collabFail(w, err)
		return
	}
	reply(w, 200, map[string]any{"doc": d, "marks": marks})
}

func (a *API) collabSave(w http.ResponseWriter, r *http.Request) {
	if !a.collabOwner(w, r) {
		return
	}
	var in struct {
		Title   *string `json:"title"`
		Body    string  `json:"body"`
		BaseRev int     `json:"base_rev"`
		By      string  `json:"by"`
	}
	if !decodeLimit(w, r, &in, 4<<20) {
		return
	}
	d, err := a.collab.Save(r.Context(), r.PathValue("name"), in.BaseRev, in.Title, in.Body, in.By)
	if err != nil {
		collabFail(w, err)
		return
	}
	reply(w, 200, map[string]any{"doc": d})
}

func (a *API) collabLog(w http.ResponseWriter, r *http.Request) {
	if !a.collabOwner(w, r) {
		return
	}
	revs, err := a.collab.Log(r.Context(), r.PathValue("name"))
	if err != nil {
		collabFail(w, err)
		return
	}
	reply(w, 200, map[string]any{"revisions": revs})
}

func (a *API) collabMark(w http.ResponseWriter, r *http.Request) {
	if !a.collabOwner(w, r) {
		return
	}
	var in struct {
		Op   string      `json:"op"`
		Mark collab.Mark `json:"mark"`
	}
	if !decodeLimit(w, r, &in, 256<<10) {
		return
	}
	name := r.PathValue("name")
	if !collab.ValidName(name) {
		fail(w, 404, "There is no such document.")
		return
	}
	marks, err := a.collab.MarkOp(r.Context(), name, in.Op, in.Mark)
	if err != nil {
		if errors.Is(err, collab.ErrNotFound) {
			collabFail(w, err)
			return
		}
		fail(w, 422, err.Error())
		return
	}
	reply(w, 200, map[string]any{"marks": marks})
}
