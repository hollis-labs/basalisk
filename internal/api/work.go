package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/hollis-labs/basalisk/internal/work"
)

// Work (internal/work, docs/work.md): the engine under Content, Planner and Writer. Typed
// items, links and saved views kept in Caster (work/v1), with a working copy here.

// EnableWork turns the work routes on. tell (the Tether gateway) mails a sent round or a
// sign-off to the workspace's owners and pins approved revisions; it may be nil.
func (a *API) EnableWork(store *work.Store, tell *work.GatewayNotifier) {
	a.work, a.workTell = store, tell
}

func (a *API) workRoutes() []route {
	if a.work == nil {
		return nil
	}
	r, w := ScopeRead, ScopeWrite
	return []route{
		{"GET", "/api/work", r, a.workList},
		{"POST", "/api/work", w, a.workNew},
		{"GET", "/api/work/{ws}", r, a.workOpen},
		{"GET", "/api/work/{ws}/log", r, a.workLog},
		{"GET", "/api/work/{ws}/at", r, a.workAt},
		{"POST", "/api/work/{ws}/items", w, a.workMake},
		{"GET", "/api/work/{ws}/items/{id}", r, a.workDetail},
		{"PUT", "/api/work/{ws}/items/{id}", w, a.workEdit},
		{"PUT", "/api/work/{ws}/items/{id}/move", w, a.workMove},
		{"PUT", "/api/work/{ws}/items/{id}/place", w, a.workPlace},
		{"POST", "/api/work/{ws}/items/{id}/torque", w, a.workSendTorque},
		{"GET", "/api/work/{ws}/items/{id}/torque", r, a.workTorqueStatus},
		{"POST", "/api/work/{ws}/items/{id}/cut", w, a.workCut},
		{"POST", "/api/work/{ws}/items/{id}/restore", w, a.workRestore},
		{"DELETE", "/api/work/{ws}/notes/{id}", w, a.workDropNote},
		{"POST", "/api/work/{ws}/items/{id}/notes", w, a.workAddNote},
		{"PUT", "/api/work/{ws}/notes/{id}", w, a.workEditNote},
		{"POST", "/api/work/{ws}/items/{id}/round", w, a.workSendRound},
		{"POST", "/api/work/{ws}/items/{id}/signoff", w, a.workSignOff},
		{"DELETE", "/api/work/{ws}/items/{id}/signoff", w, a.workUnsign},
		{"PUT", "/api/work/{ws}/owners", w, a.workOwners},
		{"POST", "/api/work/{ws}/views", w, a.workView},
		{"PUT", "/api/work/{ws}/views/{id}", w, a.workView},
		{"PUT", "/api/work/{ws}/views/{id}/layout", w, a.workLayout},
	}
}

// workFail answers a store error: no gateway, no such thing, a conflict, or Caster's.
func workFail(w http.ResponseWriter, err error) {
	var c *work.Conflict
	switch {
	case errors.As(err, &c):
		reply(w, 409, map[string]any{"message": c.Error(), "conflict": true, "rev": c.Rev, "suggestions": c.Suggestions})
	case errors.Is(err, work.ErrNotFound):
		fail(w, 404, err.Error())
	case errors.Is(err, work.ErrNoCaster):
		fail(w, 503, err.Error())
	default:
		fail(w, 502, err.Error())
	}
}

func (a *API) workOwner(w http.ResponseWriter, r *http.Request) bool {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return false
	}
	return true
}

func (a *API) workList(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	_, rerr := a.work.Refresh(r.Context(), work.IndexNS, r.URL.Query().Get("refresh") == "1")
	list, err := a.work.Workspaces(r.Context(), r.URL.Query().Get("app"))
	if err != nil {
		fail(w, 500, "The workspaces could not be read.")
		return
	}
	presets := []work.Preset{}
	for _, p := range work.Presets() {
		presets = append(presets, p)
	}
	out := map[string]any{"workspaces": list, "presets": presets}
	if rerr != nil {
		out["problem"] = rerr.Error()
	}
	reply(w, 200, out)
}

func (a *API) workNew(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	var in struct{ Workspace, Title, Preset, About, Template string }
	if !decodeLimit(w, r, &in, 32<<10) {
		return
	}
	slug, err := a.work.NewWorkspace(r.Context(), in.Workspace, in.Title, in.Preset, in.About)
	if err != nil {
		if errors.Is(err, work.ErrNoCaster) {
			workFail(w, err)
			return
		}
		fail(w, 400, err.Error())
		return
	}
	// A starting tree (Planner templates): the workspace exists either way.
	if in.Template != "" {
		if err := a.work.ApplyTemplate(r.Context(), slug, in.Template); err != nil {
			reply(w, 201, map[string]any{"workspace": slug, "problem": "the template was not all made: " + err.Error()})
			return
		}
	}
	reply(w, 201, map[string]any{"workspace": slug})
}

func (a *API) workOpen(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	ws, force := r.PathValue("ws"), r.URL.Query().Get("refresh") == "1"
	var rerr error
	if ns := a.work.NSOf(r.Context(), ws); ns != "" {
		_, rerr = a.work.Refresh(r.Context(), ns, force)
	} else if _, rerr = a.work.Refresh(r.Context(), work.IndexNS, true); rerr == nil {
		if ns := a.work.NSOf(r.Context(), ws); ns != "" {
			_, rerr = a.work.Refresh(r.Context(), ns, true)
		}
	}
	sp, ok, err := a.work.Open(r.Context(), ws)
	if err != nil {
		fail(w, 500, "The workspace could not be read.")
		return
	}
	if !ok {
		fail(w, 404, "There is no such workspace.")
		return
	}
	out := map[string]any{"space": sp, "relations": work.Relations}
	if rerr != nil {
		out["problem"] = rerr.Error()
	}
	reply(w, 200, out)
}

func (a *API) workDetail(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	d, ok, err := a.work.Detail(r.Context(), r.PathValue("ws"), r.PathValue("id"))
	if err != nil {
		fail(w, 500, "The item could not be read.")
		return
	}
	if !ok {
		fail(w, 404, "There is no such item.")
		return
	}
	reply(w, 200, d)
}

func (a *API) workMake(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	var in work.NewItem
	if !decodeLimit(w, r, &in, 64<<10) {
		return
	}
	id, err := a.work.MakeItem(r.Context(), r.PathValue("ws"), in)
	if err != nil {
		if errors.Is(err, work.ErrNoCaster) || errors.Is(err, work.ErrNotFound) {
			workFail(w, err)
			return
		}
		fail(w, 400, err.Error())
		return
	}
	reply(w, 201, map[string]any{"id": id})
}

func (a *API) workEdit(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	var in work.Edit
	if !decodeLimit(w, r, &in, 4<<20) {
		return
	}
	rev, err := a.work.EditItem(r.Context(), r.PathValue("ws"), r.PathValue("id"), in)
	if err != nil {
		workFail(w, err)
		return
	}
	reply(w, 200, map[string]any{"rev": rev})
}

func (a *API) workMove(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	var in struct{ Key, Value string }
	if !decodeLimit(w, r, &in, 4<<10) {
		return
	}
	rev, err := a.work.Move(r.Context(), r.PathValue("ws"), r.PathValue("id"), in.Key, in.Value)
	if err != nil {
		if errors.Is(err, work.ErrNoCaster) || errors.Is(err, work.ErrNotFound) {
			workFail(w, err)
			return
		}
		fail(w, 400, err.Error())
		return
	}
	reply(w, 200, map[string]any{"rev": rev})
}

// workSendTorque makes the item's Torque task once ({task, created}).
func (a *API) workSendTorque(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	task, created, err := a.work.SendToTorque(r.Context(), r.PathValue("ws"), r.PathValue("id"))
	if err != nil {
		switch {
		case errors.Is(err, work.ErrNoCaster), errors.Is(err, work.ErrNotFound):
			workFail(w, err)
		case errors.Is(err, work.ErrNoTorque):
			fail(w, 503, err.Error())
		case task != "":
			reply(w, 502, map[string]any{"message": err.Error(), "task": task})
		default:
			fail(w, 400, err.Error())
		}
		return
	}
	reply(w, 200, map[string]any{"task": task, "created": created})
}

// workTorqueStatus is the item's Torque task as Torque has it now.
func (a *API) workTorqueStatus(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	d, ok, err := a.work.Detail(r.Context(), r.PathValue("ws"), r.PathValue("id"))
	if err != nil || !ok {
		workFail(w, work.ErrNotFound)
		return
	}
	if d.Task == "" {
		reply(w, 200, map[string]any{"task": nil})
		return
	}
	st, err := a.work.TorqueStatus(r.Context(), d.Task)
	if err != nil {
		reply(w, 200, map[string]any{"task": map[string]any{"id": d.Task}, "problem": err.Error()})
		return
	}
	reply(w, 200, map[string]any{"task": st})
}

// workPlace puts an item in the outline: {parent, before, view}.
func (a *API) workPlace(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	var in struct{ Parent, Before, View string }
	if !decodeLimit(w, r, &in, 4<<10) {
		return
	}
	if err := a.work.Place(r.Context(), r.PathValue("ws"), r.PathValue("id"), in.Parent, in.Before, in.View); err != nil {
		if errors.Is(err, work.ErrNoCaster) || errors.Is(err, work.ErrNotFound) {
			workFail(w, err)
			return
		}
		fail(w, 400, err.Error())
		return
	}
	reply(w, 200, map[string]any{"ok": true})
}

func (a *API) workCut(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	if err := a.work.Cut(r.Context(), r.PathValue("ws"), r.PathValue("id")); err != nil {
		workFail(w, err)
		return
	}
	reply(w, 200, map[string]any{"ok": true})
}

func (a *API) workRestore(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	if err := a.work.Restore(r.Context(), r.PathValue("ws"), r.PathValue("id")); err != nil {
		workFail(w, err)
		return
	}
	reply(w, 200, map[string]any{"ok": true})
}

func (a *API) workDropNote(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	if err := a.work.DropNote(r.Context(), r.PathValue("ws"), r.PathValue("id")); err != nil {
		fail(w, 500, err.Error())
		return
	}
	reply(w, 200, map[string]any{"ok": true})
}

// workView makes (POST) or changes (PUT …/{id}) a saved view: the keys sent are set, null
// takes one out, the rest stay.
func (a *API) workView(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	var in map[string]any
	if !decodeLimit(w, r, &in, 1<<20) {
		return
	}
	id := r.PathValue("id")
	if r.Method == "POST" {
		id = work.NewID()
	}
	rev, err := a.work.SaveView(r.Context(), r.PathValue("ws"), id, in)
	if err != nil {
		if errors.Is(err, work.ErrNoCaster) || errors.Is(err, work.ErrNotFound) {
			workFail(w, err)
			return
		}
		fail(w, 400, err.Error())
		return
	}
	reply(w, 200, map[string]any{"id": id, "rev": rev})
}

// workLayout changes a canvas's arrangement by id (cards, stacks, notes), applied to the
// view as it is now.
func (a *API) workLayout(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	var in work.LayoutPatch
	if !decodeLimit(w, r, &in, 1<<20) {
		return
	}
	rev, err := a.work.PatchLayout(r.Context(), r.PathValue("ws"), r.PathValue("id"), in)
	if err != nil {
		if errors.Is(err, work.ErrNoCaster) || errors.Is(err, work.ErrNotFound) {
			workFail(w, err)
			return
		}
		fail(w, 400, err.Error())
		return
	}
	reply(w, 200, map[string]any{"rev": rev})
}

// workLog is an item's revisions (name: i.<id>, v.<id> or kind.<kind>).
func (a *API) workLog(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	name := r.URL.Query().Get("name")
	if k, _ := work.ParseName(name); k == work.NameOther {
		fail(w, 400, "That is not a work item.")
		return
	}
	revs, err := a.work.Log(r.Context(), r.PathValue("ws"), name)
	if err != nil {
		workFail(w, err)
		return
	}
	reply(w, 200, map[string]any{"revisions": revs})
}

// workAt is an item at a revision, as its text and as the detail reads it.
func (a *API) workAt(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	name := r.URL.Query().Get("name")
	if k, _ := work.ParseName(name); k == work.NameOther {
		fail(w, 400, "That is not a work item.")
		return
	}
	rev, _ := strconv.Atoi(r.URL.Query().Get("rev"))
	it, ok, err := a.work.At(r.Context(), r.PathValue("ws"), name, rev)
	if err != nil {
		workFail(w, err)
		return
	}
	if !ok {
		fail(w, 404, "There is no such revision.")
		return
	}
	out := map[string]any{"name": it.Name, "rev": it.Rev, "content": it.Content}
	if d, err := work.ParseDoc(it.Content); err == nil {
		secs := map[string]string{}
		for _, s := range work.ParseBody(d.Body).Sections {
			secs[s.Heading] = s.Text
		}
		out["sections"] = secs
		out["title"] = d.Str("title")
		out["stage"] = d.Str("stage")
	}
	reply(w, 200, out)
}

// ---- Rounds and sign-off ----

func (a *API) workAddNote(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	var in work.Note
	if !decodeLimit(w, r, &in, 128<<10) {
		return
	}
	n, err := a.work.AddNote(r.Context(), r.PathValue("ws"), r.PathValue("id"), in)
	if err != nil {
		if errors.Is(err, work.ErrNotFound) {
			workFail(w, err)
			return
		}
		fail(w, 400, err.Error())
		return
	}
	reply(w, 200, n)
}

func (a *API) workEditNote(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	var in struct {
		Text    *string `json:"text"`
		Replace *string `json:"replace"`
	}
	if !decodeLimit(w, r, &in, 128<<10) {
		return
	}
	if err := a.work.EditNote(r.Context(), r.PathValue("ws"), r.PathValue("id"), in.Text, in.Replace); err != nil {
		fail(w, 400, err.Error())
		return
	}
	reply(w, 200, map[string]any{"ok": true})
}

// workSendRound sends the item's draft round. The agent has no tool for this: it is the
// owner's click.
func (a *API) workSendRound(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	var tell work.Notifier
	if a.workTell != nil {
		tell = a.workTell
	}
	sent, err := a.work.SendRound(r.Context(), r.PathValue("ws"), r.PathValue("id"), tell)
	if err != nil {
		if errors.Is(err, work.ErrNoCaster) || errors.Is(err, work.ErrNotFound) {
			workFail(w, err)
			return
		}
		fail(w, 400, err.Error())
		return
	}
	reply(w, 200, sent)
}

// workSignOff signs the item off ({"anyway": true} when the last round has open notes).
func (a *API) workSignOff(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	var in struct {
		Anyway bool `json:"anyway"`
	}
	if r.ContentLength != 0 && !decodeLimit(w, r, &in, 1<<10) {
		return
	}
	var tell work.Notifier
	var pin work.Pinner
	if a.workTell != nil {
		tell, pin = a.workTell, a.workTell
	}
	signed, err := a.work.SignOff(r.Context(), r.PathValue("ws"), r.PathValue("id"), in.Anyway, pin, tell)
	if err != nil {
		var open *work.ErrOpenNotes
		if errors.As(err, &open) {
			reply(w, 409, map[string]any{"message": err.Error(), "open": open.Open, "round": open.Round})
			return
		}
		workFail(w, err)
		return
	}
	reply(w, 200, signed)
}

func (a *API) workUnsign(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	if err := a.work.Unsign(r.Context(), r.PathValue("ws"), r.PathValue("id")); err != nil {
		workFail(w, err)
		return
	}
	reply(w, 200, map[string]any{"ok": true})
}

// workOwners sets who a workspace's rounds and sign-offs are mailed to.
func (a *API) workOwners(w http.ResponseWriter, r *http.Request) {
	if !a.workOwner(w, r) {
		return
	}
	var in struct {
		Owners []string `json:"owners"`
	}
	if !decodeLimit(w, r, &in, 16<<10) {
		return
	}
	if err := a.work.SetOwners(r.Context(), r.PathValue("ws"), in.Owners); err != nil {
		if errors.Is(err, work.ErrNoCaster) || errors.Is(err, work.ErrNotFound) {
			workFail(w, err)
			return
		}
		fail(w, 400, err.Error())
		return
	}
	reply(w, 200, map[string]any{"ok": true})
}
