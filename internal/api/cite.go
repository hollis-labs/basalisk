package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/basalisk/internal/tetherlink"
)

// Cite: turning a reference the frontend recognised in some text (a Torque task id, a
// Tesseract item) into something to show. The frontend owns detection (frontend/src/cite);
// this file owns resolution, through the Tether gateway.
//
//	GET /api/cite                          {enabled, kinds}
//	GET /api/cite/resolve?kind=K&ids=a,b   {results: {id: entity | null}}   previews, cached briefly
//	GET /api/cite/record?kind=K&id=a       entity with its full body, for a viewer window
//
// The entity is the shape the preview card draws (title, status, a few fields); every kind
// is projected onto it here, so the frontend has one card and one viewer for all of them.

// EnableCite connects the Tether gateway the references resolve through. Fragments resolve
// without it (frag_cite.go), so the routes exist when either can answer.
func (a *API) EnableCite(link *tetherlink.Client) {
	if link.Configured() || fragReader.Load() != nil {
		a.cite = &citeResolver{link: link, cache: map[string]citeCached{}}
	}
}

// citeAvailable reports whether a kind can be resolved in this process: fragments through
// the fragment reader, the rest through the gateway.
func (a *API) citeAvailable(kind string) bool {
	if a.cite == nil {
		return false
	}
	if kind == "fragments.fragment" {
		return fragReader.Load() != nil
	}
	return a.cite.link.Configured()
}

func (a *API) citeRoutes() []route {
	rts := []route{{"GET", "/api/cite", ScopeRead, a.citeState}}
	if a.cite == nil {
		return rts
	}
	return append(rts,
		route{"GET", "/api/cite/resolve", ScopeRead, a.citeResolve},
		route{"GET", "/api/cite/record", ScopeRead, a.citeRecord},
	)
}

type citeEntity struct {
	Title       string      `json:"title"`
	Subtitle    string      `json:"subtitle,omitempty"`
	Description string      `json:"description,omitempty"`
	Status      *citeStatus `json:"status,omitempty"`
	Fields      []citeField `json:"fields,omitempty"`
	UpdatedAt   string      `json:"updatedAt,omitempty"`
	ChipLabel   string      `json:"chipLabel,omitempty"`
	// Body is the record's full text as markdown. Only /api/cite/record fills it.
	Body string `json:"body,omitempty"`
}

type citeStatus struct {
	Label string `json:"label"`
	Tone  string `json:"tone"` // neutral | info | success | warning | danger
}

type citeField struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Mono  bool   `json:"mono,omitempty"`
}

type citeCached struct {
	entity  *citeEntity // nil: not found
	expires time.Time
}

type citeResolver struct {
	link  *tetherlink.Client
	mu    sync.Mutex
	cache map[string]citeCached
}

// citeKind is how one kind of reference is looked up and shown.
type citeKind struct {
	id      *regexp.Regexp
	resolve func(ctx context.Context, link *tetherlink.Client, id string, full bool) (*citeEntity, error)
}

var citeKinds = map[string]citeKind{
	"torque.task":        {regexp.MustCompile(`^CW-\d{8}-\d{4}$`), torqueTask},
	"torque.epic":        {regexp.MustCompile(`^EP-\d{8}-\d{4}$`), torqueContainer("torque_epic_get", "Epic")},
	"torque.project":     {regexp.MustCompile(`^PRJ-\d{8}-\d{4}$`), torqueContainer("torque_project_get", "Project")},
	"torque.sprint":      {regexp.MustCompile(`^SP-\d{8}-\d{4}$`), torqueContainer("torque_sprint_get", "Sprint")},
	"torque.plan":        {regexp.MustCompile(`^PL-\d{8}-\d{4}$`), torqueContainer("torque_plan_get", "Plan")},
	"tesseract.item":     {regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Z]{25}$`), tesseractItem},
	"tesseract.key":      {regexp.MustCompile(`^([a-z]{3,16}@)?[a-z][A-Za-z0-9_./-]{0,400}#[A-Za-z0-9_.:-]{1,160}$`), tesseractKey},
	"fragments.fragment": {regexp.MustCompile(`^[0-9a-f]{64}$`), fragmentCite}, // frag_cite.go
}

func (a *API) citeState(w http.ResponseWriter, _ *http.Request) {
	kinds := []string{}
	for k := range citeKinds {
		if a.citeAvailable(k) {
			kinds = append(kinds, k)
		}
	}
	reply(w, 200, map[string]any{"enabled": len(kinds) > 0, "kinds": kinds})
}

// notFound reports whether a tool error says the record does not exist (or may not be read,
// which the tools do not distinguish).
func notFound(err error) bool {
	var te *tetherlink.ToolError
	return errors.Is(err, errFragmentMissing) || errors.As(err, &te) && (strings.Contains(te.Message, "not_found") || strings.Contains(te.Message, "not found") || strings.Contains(te.Message, `"deleted"`))
}

func (c *citeResolver) one(ctx context.Context, kind, id string) (*citeEntity, error) {
	key := kind + "\x00" + id
	c.mu.Lock()
	hit, ok := c.cache[key]
	c.mu.Unlock()
	if ok && time.Now().Before(hit.expires) {
		return hit.entity, nil
	}
	entity, err := citeKinds[kind].resolve(ctx, c.link, id, false)
	ttl := 45 * time.Second
	if notFound(err) {
		entity, err, ttl = nil, nil, 15*time.Second
	}
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if len(c.cache) > 2000 {
		c.cache = map[string]citeCached{}
	}
	c.cache[key] = citeCached{entity: entity, expires: time.Now().Add(ttl)}
	c.mu.Unlock()
	return entity, nil
}

func (a *API) citeResolve(w http.ResponseWriter, r *http.Request) {
	kind, ok := citeKinds[r.URL.Query().Get("kind")]
	if !ok || !a.citeAvailable(r.URL.Query().Get("kind")) {
		fail(w, 400, "Unknown reference kind.")
		return
	}
	ids := strings.Split(r.URL.Query().Get("ids"), ",")
	if len(ids) == 0 || len(ids) > 50 {
		fail(w, 400, "Give 1 to 50 ids.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		results = map[string]*citeEntity{}
		failed  error
		sem     = make(chan struct{}, 6)
	)
	for _, id := range ids {
		if !kind.id.MatchString(id) {
			continue // not this kind's id: left out, which the frontend reads as not found
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			entity, err := a.cite.one(ctx, r.URL.Query().Get("kind"), id)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = err
				return
			}
			results[id] = entity
		}(id)
	}
	wg.Wait()
	// Nothing resolved and something failed: the gateway is down, not the records missing.
	if failed != nil && len(results) == 0 {
		fail(w, 502, "The reference could not be looked up: "+failed.Error())
		return
	}
	reply(w, 200, map[string]any{"results": results})
}

func (a *API) citeRecord(w http.ResponseWriter, r *http.Request) {
	name, id := r.URL.Query().Get("kind"), r.URL.Query().Get("id")
	kind, ok := citeKinds[name]
	if !ok || !a.citeAvailable(name) || !kind.id.MatchString(id) {
		fail(w, 400, "Unknown reference.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	entity, err := kind.resolve(ctx, a.cite.link, id, true)
	switch {
	case notFound(err):
		fail(w, 404, "That record was not found. It may have been deleted, or it may not be readable.")
	case err != nil:
		fail(w, 502, "The record could not be read: "+err.Error())
	default:
		reply(w, 200, entity)
	}
}

// ---- Projections ----

func excerpt(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndexAny(s[:n], " \n")
	if cut < n/2 {
		cut = n
	}
	return strings.TrimSpace(s[:cut]) + "…"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(strings.TrimLeft(s, "# "))
}

func statusTone(status string) string {
	switch strings.ToLower(status) {
	case "done", "completed", "merged", "active", "approved":
		return "success"
	case "doing", "in_progress", "running", "queued", "started":
		return "info"
	case "review", "paused", "waiting", "draft":
		return "warning"
	case "blocked", "failed", "cancelled", "canceled":
		return "danger"
	}
	return "neutral"
}

func addField(fields []citeField, label, value string, mono bool) []citeField {
	if value == "" {
		return fields
	}
	return append(fields, citeField{Label: label, Value: value, Mono: mono})
}

func torqueTask(ctx context.Context, link *tetherlink.Client, id string, full bool) (*citeEntity, error) {
	args := map[string]any{"id": id, "format": "typed", "comments": "false"}
	if full {
		args["comments"], args["comments_limit"] = "true", "25"
	}
	raw, err := link.Call(ctx, "torque_task_get", args)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Status      string `json:"status"`
			Priority    int    `json:"priority"`
			Description string `json:"description"`
			ProjectID   string `json:"project_id"`
			EpicID      string `json:"epic_id"`
			SprintID    string `json:"sprint_id"`
			Kind        string `json:"kind"`
			UpdatedAt   string `json:"updated_at"`
			Tags        []struct {
				Name string `json:"name"`
			} `json:"tags"`
			Comments []struct {
				Author    string `json:"author"`
				Content   string `json:"content"`
				CreatedAt string `json:"created_at"`
			} `json:"comments"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("unreadable task: %w", err)
	}
	t := out.Data
	tags := make([]string, len(t.Tags))
	for i, tag := range t.Tags {
		tags[i] = tag.Name
	}
	e := &citeEntity{
		Title:       t.Title,
		Subtitle:    "Torque task",
		Description: excerpt(t.Description, 320),
		Status:      &citeStatus{Label: t.Status, Tone: statusTone(t.Status)},
		UpdatedAt:   t.UpdatedAt,
		ChipLabel:   "Task: " + t.Title,
	}
	e.Fields = addField(e.Fields, "Priority", fmt.Sprintf("P%d", t.Priority), false)
	e.Fields = addField(e.Fields, "Project", t.ProjectID, true)
	e.Fields = addField(e.Fields, "Epic", t.EpicID, true)
	e.Fields = addField(e.Fields, "Sprint", t.SprintID, true)
	e.Fields = addField(e.Fields, "Kind", t.Kind, false)
	e.Fields = addField(e.Fields, "Tags", strings.Join(tags, ", "), false)
	if full {
		var b strings.Builder
		b.WriteString(strings.TrimSpace(t.Description))
		if len(t.Comments) > 0 {
			b.WriteString("\n\n---\n\n## Comments\n")
			for _, c := range t.Comments {
				fmt.Fprintf(&b, "\n**%s** · %s\n\n%s\n", c.Author, c.CreatedAt, strings.TrimSpace(c.Content))
			}
		}
		e.Body = b.String()
	}
	return e, nil
}

// pick returns the first non-empty string among the keys, whichever case the tool used.
func pick(m map[string]any, keys ...string) string {
	for _, k := range keys {
		for _, name := range []string{k, strings.ToUpper(k[:1]) + k[1:]} {
			switch v := m[name].(type) {
			case string:
				if v != "" {
					return v
				}
			case map[string]any: // a sql.Null wrapper: {String, Valid}
				if s, _ := v["String"].(string); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

func torqueContainer(tool, label string) func(context.Context, *tetherlink.Client, string, bool) (*citeEntity, error) {
	return func(ctx context.Context, link *tetherlink.Client, id string, full bool) (*citeEntity, error) {
		raw, err := link.Call(ctx, tool, map[string]any{"id": id})
		if err != nil {
			return nil, err
		}
		var out struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(raw, &out); err != nil || out.Data == nil {
			return nil, fmt.Errorf("unreadable %s", strings.ToLower(label))
		}
		title := pick(out.Data, "title", "name")
		if title == "" {
			title = id
		}
		description := pick(out.Data, "description", "goal", "summary")
		e := &citeEntity{
			Title:       title,
			Subtitle:    "Torque " + strings.ToLower(label),
			Description: excerpt(description, 320),
			UpdatedAt:   pick(out.Data, "updated_at", "updatedAt"),
			ChipLabel:   label + ": " + title,
		}
		if status := pick(out.Data, "status"); status != "" {
			e.Status = &citeStatus{Label: status, Tone: statusTone(status)}
		}
		e.Fields = addField(e.Fields, "Project", pick(out.Data, "project_id", "projectID"), true)
		if full {
			e.Body = description
		}
		return e, nil
	}
}

type tesseractRecord struct {
	ItemID    string `json:"item_id"`
	MemoryID  string `json:"memory_id"`
	Domain    string `json:"domain"`
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	MemoryKey string `json:"memory_key"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updated_at"`
	CreatedAt string `json:"created_at"`
	Tags      []string
	Author    struct {
		AgentID string `json:"agent_id"`
	} `json:"author"`
	Payload struct {
		Summary string `json:"summary"`
		Body    string `json:"body"`
	} `json:"payload"`
}

// tesseractType names the kind of note from where it lives: ".../workspace/design" is a
// Design, ".../memory/handoffs" a Handoff.
func tesseractType(namespace string) string {
	seg := namespace
	if i := strings.LastIndexByte(namespace, '/'); i >= 0 {
		seg = namespace[i+1:]
	}
	seg = strings.TrimSuffix(seg, "s")
	if seg == "" {
		return "Note"
	}
	return strings.ToUpper(seg[:1]) + seg[1:]
}

func tesseractEntity(raw []byte, full bool) (*citeEntity, error) {
	var r tesseractRecord
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("unreadable Tesseract item: %w", err)
	}
	key := r.Key
	if key == "" {
		key = r.MemoryKey
	}
	title := firstLine(r.Payload.Body)
	if title == "" || len(title) > 140 {
		title = excerpt(r.Payload.Summary, 140)
	}
	if title == "" {
		title = key
	}
	kind := tesseractType(r.Namespace)
	updated := r.UpdatedAt
	if updated == "" {
		updated = r.CreatedAt
	}
	e := &citeEntity{
		Title:       title,
		Subtitle:    "Tesseract " + r.Domain,
		Description: excerpt(r.Payload.Summary, 320),
		UpdatedAt:   updated,
		ChipLabel:   kind + ": " + title,
	}
	if r.Status != "" {
		e.Status = &citeStatus{Label: r.Status, Tone: statusTone(r.Status)}
	}
	e.Fields = addField(e.Fields, "Namespace", r.Namespace, true)
	e.Fields = addField(e.Fields, "Key", key, true)
	e.Fields = addField(e.Fields, "Tags", strings.Join(r.Tags, ", "), false)
	e.Fields = addField(e.Fields, "Author", r.Author.AgentID, true)
	if full {
		e.Body = r.Payload.Body
		if e.Body == "" {
			e.Body = r.Payload.Summary
		}
	}
	return e, nil
}

func tesseractItem(ctx context.Context, link *tetherlink.Client, id string, full bool) (*citeEntity, error) {
	raw, err := link.Call(ctx, "tesseract_get", map[string]any{"item_id": id})
	if err != nil {
		return nil, err
	}
	return tesseractEntity(raw, full)
}

// tesseractKey resolves "[domain@]namespace#key". Without a domain the curated ones are
// tried in turn.
func tesseractKey(ctx context.Context, link *tetherlink.Client, ref string, full bool) (*citeEntity, error) {
	domains := []string{"knowledge", "memory", "workspace"}
	if at := strings.IndexByte(ref, '@'); at > 0 && at < strings.IndexByte(ref, '#') {
		domains, ref = []string{ref[:at]}, ref[at+1:]
	}
	namespace, key, _ := strings.Cut(ref, "#")
	var last error
	for _, domain := range domains {
		raw, err := link.Call(ctx, "tesseract_get", map[string]any{"domain": domain, "namespace": namespace, "key": key})
		if err == nil {
			return tesseractEntity(raw, full)
		}
		last = err
		if !notFound(err) {
			// A namespace that does not hold this domain answers a validation error; keep looking.
			var te *tetherlink.ToolError
			if !errors.As(err, &te) {
				return nil, err
			}
		}
	}
	if last != nil && !notFound(last) {
		return nil, &tetherlink.ToolError{Message: "not_found: " + last.Error()}
	}
	return nil, last
}
