package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/basalisk/internal/scratch"
)

// Scratch (internal/scratch, docs/scratch.md): the owner's quick notes, commands,
// follow-ups and ideas as small items, and the views over them.

// EnableScratch turns the scratch routes on.
func (a *API) EnableScratch(store *scratch.Store, syncer *scratch.Syncer, link scratch.Putter) {
	a.scratch, a.scratchSync, a.scratchLink = store, syncer, link
}

// ScratchAuto is what a scratch item written now is tied to without being told: the
// owner's work session that is going, and its project.
func (a *API) ScratchAuto(ctx context.Context, owner string) scratch.Auto {
	state, err := a.svc.activity.State(ctx, owner)
	if err != nil || state.Current == nil || state.Current.Status == "ended" {
		return scratch.Auto{}
	}
	return scratch.Auto{SessionID: state.Current.ID, SessionTitle: state.Current.Title, Project: state.Current.Context.Project}
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
		{"POST", "/api/scratch/{id}/task", w, a.scratchToTask},
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
	reply(w, 200, map[string]any{"items": items, "today": time.Now().Format("2006-01-02"), "labels": a.scratch.Labels(r.Context())})
}

func (a *API) scratchFacets(w http.ResponseWriter, r *http.Request) {
	owner, err := a.ownerID(r)
	if err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	a.scratchImport(owner)
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
	items, err := a.scratch.Add(r.Context(), owner, in.Text, "owner", in.Context, time.Now(), a.ScratchAuto(r.Context(), owner))
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
		Text     *string       `json:"text"`
		Done     *bool         `json:"done"`
		Archived *bool         `json:"archived"`
		Link     *scratch.Link `json:"link"`
	}
	if !decodeLimit(w, r, &in, 256<<10) {
		return
	}
	it, err := a.scratch.Update(r.Context(), owner, r.PathValue("id"), scratch.Change{Text: in.Text, Done: in.Done, Archived: in.Archived, Link: in.Link}, time.Now())
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	a.scratchSync.Kick()
	reply(w, 200, map[string]any{"item": it})
}

var (
	scratchImportMu   sync.Mutex
	scratchImportedAt time.Time
)

// scratchImport looks in Caster, now and then and behind the request, for scratch items
// that were put there by something else (an agent, another machine).
func (a *API) scratchImport(owner string) {
	scratchImportMu.Lock()
	due := time.Since(scratchImportedAt) > 2*time.Minute
	if due {
		scratchImportedAt = time.Now()
	}
	scratchImportMu.Unlock()
	if !due || a.scratchSync == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if n, err := a.scratchSync.Import(ctx, owner); err != nil {
			slog.Warn("scratch: Caster could not be read for new items", "err", err)
		} else if n > 0 {
			slog.Info("scratch: items read back from Caster", "count", n)
		}
	}()
}

// scratchToTask makes a Torque task of an item: its first line the title, the rest the
// description, in the Torque project named like the item's project when there is one.
// The item is then tied to the task.
func (a *API) scratchToTask(w http.ResponseWriter, r *http.Request) {
	owner, err := a.ownerID(r)
	if err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	if a.scratchLink == nil || !a.scratchLink.Configured() {
		fail(w, 503, "No Tether gateway is configured, so a task cannot be made from here.")
		return
	}
	it, ok := a.scratch.Get(r.Context(), owner, r.PathValue("id"))
	if !ok {
		fail(w, 404, "There is no such item.")
		return
	}
	for _, l := range it.Links {
		if l.Rel == scratch.RelBecame && strings.HasPrefix(l.Value, "task:") {
			fail(w, 409, "This item is already the task "+strings.TrimPrefix(l.Value, "task:")+".")
			return
		}
	}
	title, rest, _ := strings.Cut(strings.TrimSpace(it.Text), "\n")
	args := map[string]any{
		"title":       clipText(title, 200),
		"description": strings.TrimSpace(rest + "\n\nFrom scratch, " + it.Day + " (" + scratch.Ref(it.ID) + ")."),
		"tags":        `["scratch"]`,
		"source_type": "user",
		"priority":    "3",
	}
	project := ""
	for _, l := range it.Links {
		if l.Rel == scratch.RelProject {
			project = l.Value
			break
		}
	}
	if project != "" {
		if id := a.torqueProject(r.Context(), project); id != "" {
			args["project_id"] = id
		}
	}
	raw, err := a.scratchLink.Call(r.Context(), "torque_task_create", args)
	if err != nil {
		fail(w, 502, "Torque did not take the task: "+clipText(err.Error(), 200))
		return
	}
	var made struct {
		Data struct {
			ID string `json:"ID"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &made) != nil || made.Data.ID == "" {
		fail(w, 502, "Torque answered, but not with a task.")
		return
	}
	id := made.Data.ID
	_, _ = a.scratch.Update(r.Context(), owner, it.ID, scratch.Change{Link: &scratch.Link{Rel: scratch.RelTask, Value: id}}, time.Now())
	out, _ := a.scratch.Update(r.Context(), owner, it.ID, scratch.Change{Link: &scratch.Link{Rel: scratch.RelBecame, Value: "task:" + id}}, time.Now())
	a.scratchSync.Kick()
	reply(w, 200, map[string]any{"task_id": id, "item": out, "in_project": args["project_id"] != nil})
}

// torqueProject is the id of the Torque project with this name ("" when there is not
// exactly one).
func (a *API) torqueProject(ctx context.Context, name string) string {
	raw, err := a.scratchLink.Call(ctx, "torque_project_list", map[string]any{"search": name, "limit": "50"})
	if err != nil {
		return ""
	}
	var list struct {
		Data struct {
			Items []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"items"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return ""
	}
	found := ""
	for _, p := range list.Data.Items {
		if strings.EqualFold(strings.ReplaceAll(p.Name, " ", "-"), name) || strings.EqualFold(p.Name, name) {
			if found != "" {
				return ""
			}
			found = p.ID
		}
	}
	return found
}

func clipText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
