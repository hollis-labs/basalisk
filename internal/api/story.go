package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/hollis-labs/basalisk/internal/story"
)

// Story (internal/story, docs/story.md): video scripts and storyboards, kept in Caster's
// "story" namespace with a working copy here.

// EnableStory turns the story routes on.
func (a *API) EnableStory(store *story.Store) { a.story = store }

func (a *API) storyRoutes() []route {
	if a.story == nil {
		return nil
	}
	r, w := ScopeRead, ScopeWrite
	return []route{
		{"GET", "/api/story", r, a.storyBoard},
		{"GET", "/api/story/scripts/{key}", r, a.storyScript},
		{"GET", "/api/story/item", r, a.storyItem},
		{"GET", "/api/story/log", r, a.storyLog},
		{"POST", "/api/story/scripts", w, a.storyNew},
		{"PUT", "/api/story/scripts/{key}/stage", w, a.storyStage},
		{"PUT", "/api/story/scripts/{key}/order", w, a.storyOrder},
		{"POST", "/api/story/scripts/{key}/scenes", w, a.storyAddScene},
		{"PUT", "/api/story/scripts/{key}/scenes/{id}", w, a.storyEditScene},
		{"POST", "/api/story/scripts/{key}/scenes/{id}/cut", w, a.storyCut},
		{"POST", "/api/story/scripts/{key}/scenes/{id}/restore", w, a.storyRestore},
	}
}

// storyFail answers a store error: no gateway, no such script, a conflict, or Caster's.
func storyFail(w http.ResponseWriter, err error) {
	var c *story.Conflict
	switch {
	case errors.As(err, &c):
		reply(w, 409, map[string]any{"message": c.Error(), "conflict": true, "rev": c.Rev, "suggestions": c.Suggestions})
	case errors.Is(err, story.ErrNotFound):
		fail(w, 404, err.Error())
	case errors.Is(err, story.ErrNoCaster):
		fail(w, 503, err.Error())
	default:
		fail(w, 502, err.Error())
	}
}

func (a *API) storyBoard(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	_, rerr := a.story.Refresh(r.Context(), r.URL.Query().Get("refresh") == "1")
	cards, err := a.story.Board(r.Context())
	if err != nil {
		fail(w, 500, "The scripts could not be read.")
		return
	}
	out := map[string]any{"scripts": cards, "stages": story.Stages}
	if rerr != nil {
		// The copy is shown; say Caster could not be reached.
		out["problem"] = rerr.Error()
	}
	reply(w, 200, out)
}

func (a *API) storyScript(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	key := r.PathValue("key")
	_, rerr := a.story.Refresh(r.Context(), r.URL.Query().Get("refresh") == "1")
	sc, ok, err := a.story.Script(r.Context(), key)
	if err != nil {
		fail(w, 500, "The script could not be read.")
		return
	}
	if !ok {
		fail(w, 404, "There is no such script.")
		return
	}
	drafts, _ := a.story.Drafts(r.Context(), key)
	out := map[string]any{"script": sc, "drafts": drafts, "sections": story.Sections}
	if rerr != nil {
		out["problem"] = rerr.Error()
	}
	reply(w, 200, out)
}

// storyItem is one item at a revision (for comparing a scene with an earlier one).
func (a *API) storyItem(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	name := r.URL.Query().Get("name")
	if story.ParseName(name).Kind == story.KindOther {
		fail(w, 400, "That is not a story item.")
		return
	}
	rev, _ := strconv.Atoi(r.URL.Query().Get("rev"))
	it, ok, err := a.story.At(r.Context(), name, rev)
	if err != nil {
		storyFail(w, err)
		return
	}
	if !ok {
		fail(w, 404, "There is no such revision.")
		return
	}
	reply(w, 200, map[string]any{"name": it.Name, "rev": it.Rev, "content": it.Content})
}

func (a *API) storyLog(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	name := r.URL.Query().Get("name")
	if story.ParseName(name).Kind == story.KindOther {
		fail(w, 400, "That is not a story item.")
		return
	}
	revs, err := a.story.Log(r.Context(), name)
	if err != nil {
		storyFail(w, err)
		return
	}
	reply(w, 200, map[string]any{"revisions": revs})
}

func (a *API) storyNew(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	var in struct{ Project, Script, Title string }
	if !decodeLimit(w, r, &in, 16<<10) {
		return
	}
	if in.Title == "" {
		in.Title = in.Script
	}
	key, err := a.story.NewScript(r.Context(), in.Project, in.Script, in.Title)
	if err != nil {
		if errors.Is(err, story.ErrNoCaster) {
			storyFail(w, err)
			return
		}
		fail(w, 400, err.Error())
		return
	}
	reply(w, 201, map[string]any{"key": key})
}

func (a *API) storyStage(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	var in struct{ Stage string }
	if !decodeLimit(w, r, &in, 4<<10) {
		return
	}
	if in.Stage == "approved" {
		fail(w, 400, "A script is approved by signing it off.")
		return
	}
	if err := a.story.SetStage(r.Context(), r.PathValue("key"), in.Stage); err != nil {
		storyFail(w, err)
		return
	}
	reply(w, 200, map[string]any{"ok": true})
}

func (a *API) storyOrder(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	var in struct{ Scenes []string }
	if !decodeLimit(w, r, &in, 64<<10) {
		return
	}
	if err := a.story.SetOrder(r.Context(), r.PathValue("key"), in.Scenes); err != nil {
		storyFail(w, err)
		return
	}
	reply(w, 200, map[string]any{"ok": true})
}

func (a *API) storyAddScene(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	var in struct{ After, From, Title string }
	if !decodeLimit(w, r, &in, 16<<10) {
		return
	}
	id, err := a.story.AddScene(r.Context(), r.PathValue("key"), in.After, in.From, in.Title)
	if err != nil {
		storyFail(w, err)
		return
	}
	reply(w, 201, map[string]any{"id": id})
}

func (a *API) storyEditScene(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	var in story.SceneEdit
	if !decodeLimit(w, r, &in, 256<<10) {
		return
	}
	rev, err := a.story.EditScene(r.Context(), r.PathValue("key"), r.PathValue("id"), in)
	if err != nil {
		storyFail(w, err)
		return
	}
	reply(w, 200, map[string]any{"rev": rev})
}

func (a *API) storyCut(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	if err := a.story.CutScene(r.Context(), r.PathValue("key"), r.PathValue("id")); err != nil {
		storyFail(w, err)
		return
	}
	reply(w, 200, map[string]any{"ok": true})
}

func (a *API) storyRestore(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	if err := a.story.RestoreScene(r.Context(), r.PathValue("key"), r.PathValue("id")); err != nil {
		storyFail(w, err)
		return
	}
	reply(w, 200, map[string]any{"ok": true})
}
