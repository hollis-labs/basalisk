package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/basalisk/internal/scratch"
)

// Scratch (internal/scratch, docs/scratch.md): the owner's quick notes, commands,
// follow-ups and ideas as small items, and the views over them.

// EnableScratch turns the scratch routes on.
func (a *API) EnableScratch(store *scratch.Store, syncer *scratch.Syncer) {
	a.scratch, a.scratchSync = store, syncer
}

func (a *API) scratchRoutes() []route {
	if a.scratch == nil {
		return nil
	}
	r, w := ScopeRead, ScopeWrite
	return []route{
		{"GET", "/api/scratch", r, a.scratchList},
		{"GET", "/api/scratch/facets", r, a.scratchFacets},
		{"GET", "/api/scratch/page", r, a.scratchPage},
		{"POST", "/api/scratch", w, a.scratchAdd},
		{"PUT", "/api/scratch/{id}", w, a.scratchUpdate},
	}
}

// scratchQuery reads a view from the query string: day, kind, rel + value, open, q, archived.
func scratchQuery(r *http.Request) scratch.Query {
	v := r.URL.Query()
	return scratch.Query{
		Day: v.Get("day"), Kind: v.Get("kind"), Rel: v.Get("rel"), Value: v.Get("value"),
		Open: v.Get("open") == "1", Text: v.Get("q"), Archived: v.Get("archived") == "1",
	}
}

func (a *API) scratchList(w http.ResponseWriter, r *http.Request) {
	owner, err := a.ownerID(r)
	if err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	items, err := a.scratch.List(r.Context(), owner, scratchQuery(r))
	if err != nil {
		fail(w, 500, "The scratch could not be read.")
		return
	}
	reply(w, 200, map[string]any{"items": items, "today": time.Now().Format("2006-01-02")})
}

func (a *API) scratchFacets(w http.ResponseWriter, r *http.Request) {
	owner, err := a.ownerID(r)
	if err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	f, err := a.scratch.Facets(r.Context(), owner)
	if err != nil {
		fail(w, 500, "The scratch could not be read.")
		return
	}
	reply(w, 200, map[string]any{"facets": f, "today": time.Now().Format("2006-01-02")})
}

// scratchPage is a view as one markdown page: what the view is when it is read or saved as
// a file.
func (a *API) scratchPage(w http.ResponseWriter, r *http.Request) {
	owner, err := a.ownerID(r)
	if err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	items, err := a.scratch.List(r.Context(), owner, scratchQuery(r))
	if err != nil {
		fail(w, 500, "The scratch could not be read.")
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = w.Write([]byte(scratch.Markdown(strings.TrimSpace(r.URL.Query().Get("title")), items)))
}

func (a *API) scratchAdd(w http.ResponseWriter, r *http.Request) {
	owner, err := a.ownerID(r)
	if err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	var in struct {
		Text    string          `json:"text"`
		Context json.RawMessage `json:"context"`
	}
	if !decodeLimit(w, r, &in, 256<<10) {
		return
	}
	if len(in.Context) > 16<<10 {
		in.Context = nil
	}
	items, err := a.scratch.Add(r.Context(), owner, in.Text, "owner", in.Context, time.Now())
	if err != nil {
		fail(w, 400, "There is nothing to keep.")
		return
	}
	a.scratchSync.Kick()
	reply(w, 200, map[string]any{"items": items})
}

func (a *API) scratchUpdate(w http.ResponseWriter, r *http.Request) {
	owner, err := a.ownerID(r)
	if err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	var in struct {
		Text     *string `json:"text"`
		Done     *bool   `json:"done"`
		Archived *bool   `json:"archived"`
	}
	if !decodeLimit(w, r, &in, 256<<10) {
		return
	}
	it, err := a.scratch.Update(r.Context(), owner, r.PathValue("id"), scratch.Change{Text: in.Text, Done: in.Done, Archived: in.Archived}, time.Now())
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	a.scratchSync.Kick()
	reply(w, 200, map[string]any{"item": it})
}
