// Package collab is what the owner and the agent work on together: drafts (code, content,
// plans), artifacts under review (an image, a video, a document, a mock) and diagrams. Each
// is a document in Caster's "collab" namespace, so every save is a revision and any two can
// be compared, with its marks beside it. A mark is something pointed at plus a note: a
// passage, a line range, a region, a time, a node. Contract collab/v1 (docs/collab.md).
package collab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Namespace is where the documents live in Caster.
const Namespace = "collab"

// Kinds of document.
const (
	KindDraft    = "draft"
	KindArtifact = "artifact"
	KindDiagram  = "diagram"
)

var nameRe = regexp.MustCompile(`^(draft|artifact|diagram)\.[a-z0-9][a-z0-9-]{0,80}$`)

// ValidName reports whether name is a document's name ("<kind>.<slug>").
func ValidName(name string) bool { return nameRe.MatchString(name) }

// Doc is one document at a revision.
type Doc struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	// Lang is what a draft is written in (markdown, go, typescript, text…).
	Lang    string `json:"lang,omitempty"`
	Project string `json:"project,omitempty"`
	// Ref is what an artifact points at: a file ("file:<root>:<path>"), a web address, a
	// fragment ("fragment:<id>").
	Ref     string `json:"ref,omitempty"`
	Created string `json:"created,omitempty"`
	Rev     int    `json:"rev"`
	Updated string `json:"updated,omitempty"`
	// Body is the text of a draft, the JSON of a diagram, the notes of an artifact.
	Body string `json:"body"`
}

// Anchor is what a mark points at. Which fields are set depends on the document.
type Anchor struct {
	// Text: the passage, with a little of what is around it, and the lines it was on.
	Quote     string `json:"quote,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	Suffix    string `json:"suffix,omitempty"`
	LineStart int    `json:"line_start,omitempty"`
	LineEnd   int    `json:"line_end,omitempty"`
	// A region of an image, a page or a mock, as fractions of its width and height.
	X    *float64 `json:"x,omitempty"`
	Y    *float64 `json:"y,omitempty"`
	W    *float64 `json:"w,omitempty"`
	H    *float64 `json:"h,omitempty"`
	Page int      `json:"page,omitempty"`
	// A time in a video or a recording, in seconds.
	Time *float64 `json:"time,omitempty"`
	// A node or an edge of a diagram, by id.
	Node string `json:"node,omitempty"`
	Edge string `json:"edge,omitempty"`
	// An element of a mock, by a CSS selector.
	Selector string `json:"selector,omitempty"`
}

// Mark is a note on something pointed at.
type Mark struct {
	ID string `json:"id"`
	// Rev is the document's revision the mark was made on.
	Rev    int    `json:"rev"`
	Anchor Anchor `json:"anchor"`
	// Kind: comment, suggestion (Replace is what to put instead of the quote), question.
	Kind    string `json:"kind"`
	Text    string `json:"text,omitempty"`
	Replace string `json:"replace,omitempty"`
	// By: "user" or "agent".
	By    string `json:"by"`
	State string `json:"state"` // open | done | declined
	At    string `json:"at"`
	Reply string `json:"reply,omitempty"`
}

// Caller is what the gateway client offers (internal/tetherlink).
type Caller interface {
	Configured() bool
	Call(ctx context.Context, tool string, args map[string]any) ([]byte, error)
	CallAll(ctx context.Context, tool string, args map[string]any) ([]byte, error)
}

// Store reads and writes the documents through the Tether gateway. Nothing is kept here.
type Store struct{ Link Caller }

// ErrNotFound means there is no such document; Conflict that it moved on since it was read.
var ErrNotFound = errors.New("no such document")

// Conflict is returned by Save when the document has a newer revision than the edit began on.
type Conflict struct{ Rev int }

func (c *Conflict) Error() string { return fmt.Sprintf("the document is now at revision %d", c.Rev) }

const maxDoc = 4 << 20

type front struct {
	Collab  string `yaml:"collab"`
	Kind    string `yaml:"kind"`
	Title   string `yaml:"title"`
	Lang    string `yaml:"lang,omitempty"`
	Project string `yaml:"project,omitempty"`
	Ref     string `yaml:"ref,omitempty"`
	Created string `yaml:"created,omitempty"`
}

func encode(d Doc) string {
	head, _ := yaml.Marshal(front{Collab: "v1", Kind: d.Kind, Title: d.Title, Lang: d.Lang, Project: d.Project, Ref: d.Ref, Created: d.Created})
	return "---\n" + string(head) + "---\n" + d.Body
}

func decode(name, content string) Doc {
	d := Doc{Name: name, Kind: strings.SplitN(name, ".", 2)[0], Body: content}
	rest, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		return d
	}
	head, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return d
	}
	var f front
	if yaml.Unmarshal([]byte(head), &f) != nil {
		return d
	}
	d.Title, d.Lang, d.Project, d.Ref, d.Created, d.Body = f.Title, f.Lang, f.Project, f.Ref, f.Created, body
	return d
}

func (s *Store) get(ctx context.Context, name string, rev int) (content, title string, at int, ok bool, err error) {
	ref := "caster://" + Namespace + "/" + name
	if rev > 0 {
		ref += "@" + strconv.Itoa(rev)
	}
	raw, err := s.Link.Call(ctx, "caster_get", map[string]any{"ref": ref, "max_bytes": maxDoc})
	if err != nil {
		return "", "", 0, false, err
	}
	var out struct {
		Content   string `json:"content"`
		State     string `json:"state"`
		Revision  string `json:"revision"`
		Title     string `json:"title"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", 0, false, fmt.Errorf("caster_get: %w", err)
	}
	switch {
	case out.State == "not_found":
		return "", "", 0, false, nil
	case out.State != "resolved":
		return "", "", 0, false, fmt.Errorf("caster_get %s: %s", ref, out.State)
	case out.Truncated:
		return "", "", 0, false, fmt.Errorf("%s is larger than %d MB", name, maxDoc>>20)
	}
	at, _ = strconv.Atoi(out.Revision)
	return out.Content, out.Title, at, true, nil
}

func (s *Store) put(ctx context.Context, name, content, title, mime, by string) (int, error) {
	if by != "agent" {
		by = "owner"
	}
	raw, err := s.Link.Call(ctx, "caster_put", map[string]any{
		"name": Namespace + "/" + name, "content": content, "mime": mime, "title": title, "source": "basalisk:" + by,
	})
	if err != nil {
		return 0, err
	}
	var out struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, fmt.Errorf("caster_put: %w", err)
	}
	_, at, ok := strings.Cut(out.Ref, "@")
	rev, err := strconv.Atoi(at)
	if !ok || err != nil {
		return 0, errors.New("caster_put did not say the new revision")
	}
	return rev, nil
}

// List is the documents of a kind ("" for all), most recently changed first, without bodies.
func (s *Store) List(ctx context.Context, kind string) ([]Doc, error) {
	raw, err := s.Link.CallAll(ctx, "caster_ls", map[string]any{"ns": Namespace})
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []struct {
			Ref     string `json:"ref"`
			Rev     int    `json:"rev"`
			Title   string `json:"title"`
			Updated string `json:"updated"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("caster_ls: %w", err)
	}
	docs := []Doc{}
	for _, it := range out.Items {
		name, ok := strings.CutPrefix(it.Ref, "caster://"+Namespace+"/")
		if !ok || !ValidName(name) {
			continue
		}
		k := strings.SplitN(name, ".", 2)[0]
		if kind != "" && k != kind {
			continue
		}
		docs = append(docs, Doc{Name: name, Kind: k, Title: it.Title, Rev: it.Rev, Updated: it.Updated})
	}
	sort.SliceStable(docs, func(i, j int) bool { return docs[i].Updated > docs[j].Updated })
	return docs, nil
}

// Get is a document at a revision (0 = latest).
func (s *Store) Get(ctx context.Context, name string, rev int) (Doc, error) {
	if !ValidName(name) {
		return Doc{}, ErrNotFound
	}
	content, title, at, ok, err := s.get(ctx, name, rev)
	if err != nil {
		return Doc{}, err
	}
	if !ok {
		return Doc{}, ErrNotFound
	}
	d := decode(name, content)
	if d.Title == "" {
		d.Title = title
	}
	d.Rev = at
	return d, nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Create makes a new document and returns it. The name is made from the title.
func (s *Store) Create(ctx context.Context, d Doc, by string) (Doc, error) {
	if d.Kind != KindDraft && d.Kind != KindArtifact && d.Kind != KindDiagram {
		return Doc{}, errors.New("a document is a draft, an artifact or a diagram")
	}
	d.Title = strings.TrimSpace(d.Title)
	if d.Title == "" {
		d.Title = "Untitled"
	}
	slug := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(d.Title), "-"), "-")
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "-")
	}
	if slug == "" {
		slug = d.Kind
	}
	d.Created = time.Now().UTC().Format(time.RFC3339)
	base := d.Kind + "." + slug
	d.Name = base
	for n := 2; n < 200; n++ {
		_, _, _, taken, err := s.get(ctx, d.Name, 0)
		if err != nil {
			return Doc{}, err
		}
		if !taken {
			break
		}
		d.Name = base + "-" + strconv.Itoa(n)
	}
	if !ValidName(d.Name) {
		return Doc{}, errors.New("that title does not make a usable name")
	}
	rev, err := s.put(ctx, d.Name, encode(d), d.Title, "text/markdown", by)
	if err != nil {
		return Doc{}, err
	}
	d.Rev = rev
	return d, nil
}

// Save writes a new revision of a document's body (and title, when given). base is the
// revision the edit began on: when the document has moved on since, nothing is written and
// a *Conflict says where it is now. base 0 skips the check.
func (s *Store) Save(ctx context.Context, name string, base int, title *string, body string, by string) (Doc, error) {
	cur, err := s.Get(ctx, name, 0)
	if err != nil {
		return Doc{}, err
	}
	if base > 0 && cur.Rev != base {
		return cur, &Conflict{Rev: cur.Rev}
	}
	if title != nil && strings.TrimSpace(*title) != "" {
		cur.Title = strings.TrimSpace(*title)
	}
	cur.Body = body
	rev, err := s.put(ctx, name, encode(cur), cur.Title, "text/markdown", by)
	if err != nil {
		return Doc{}, err
	}
	cur.Rev = rev
	return cur, nil
}

// Revision is one entry of a document's history.
type Revision struct {
	Rev   int    `json:"rev"`
	At    string `json:"at"`
	Title string `json:"title"`
}

// Log is a document's revisions, newest first.
func (s *Store) Log(ctx context.Context, name string) ([]Revision, error) {
	if !ValidName(name) {
		return nil, ErrNotFound
	}
	raw, err := s.Link.CallAll(ctx, "caster_log", map[string]any{"name": Namespace + "/" + name})
	if err != nil {
		return nil, err
	}
	var out struct {
		Revisions []Revision `json:"revisions"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("caster_log: %w", err)
	}
	return out.Revisions, nil
}

type markFile struct {
	Collab string `json:"collab"`
	Marks  []Mark `json:"marks"`
}

// Marks is a document's marks, oldest first.
func (s *Store) Marks(ctx context.Context, name string) ([]Mark, error) {
	if !ValidName(name) {
		return nil, ErrNotFound
	}
	content, _, _, ok, err := s.get(ctx, name+".marks", 0)
	if err != nil || !ok {
		return []Mark{}, err
	}
	var f markFile
	if json.Unmarshal([]byte(content), &f) != nil || f.Marks == nil {
		return []Mark{}, nil
	}
	return f.Marks, nil
}

// MarkOp changes a document's marks: "add" (the id and time are given here), "update" (the
// fields set in m replace those of the mark with its id) or "remove". The marks are read
// again immediately before writing, so two writers do not undo each other's.
func (s *Store) MarkOp(ctx context.Context, name, op string, m Mark) ([]Mark, error) {
	marks, err := s.Marks(ctx, name)
	if err != nil {
		return nil, err
	}
	switch op {
	case "add":
		if m.By != "agent" {
			m.By = "user"
		}
		switch m.Kind {
		case "comment", "suggestion", "question":
		default:
			m.Kind = "comment"
		}
		if strings.TrimSpace(m.Text) == "" && m.Kind != "suggestion" {
			return nil, errors.New("a mark needs a note")
		}
		m.ID = fmt.Sprintf("m%x", time.Now().UnixNano()/1000)
		m.At, m.State, m.Reply = time.Now().UTC().Format(time.RFC3339), "open", ""
		marks = append(marks, m)
	case "update":
		found := false
		for i := range marks {
			if marks[i].ID != m.ID {
				continue
			}
			found = true
			switch m.State {
			case "open", "done", "declined":
				marks[i].State = m.State
			}
			if m.Text != "" {
				marks[i].Text = m.Text
			}
			if m.Reply != "" {
				marks[i].Reply = m.Reply
			}
			if m.Replace != "" {
				marks[i].Replace = m.Replace
			}
		}
		if !found {
			return nil, ErrNotFound
		}
	case "remove":
		kept := marks[:0]
		for _, x := range marks {
			if x.ID != m.ID {
				kept = append(kept, x)
			}
		}
		marks = kept
	default:
		return nil, errors.New("a mark is added, updated or removed")
	}
	data, _ := json.MarshalIndent(markFile{Collab: "v1", Marks: marks}, "", " ")
	if _, err := s.put(ctx, name+".marks", string(data), "Marks on "+name, "application/json", m.By); err != nil {
		return nil, err
	}
	return marks, nil
}
