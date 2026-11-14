package story

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Caster is where the story items live (namespace "story"). The store keeps a working copy
// and writes through this; tests give it a stand-in.
type Caster interface {
	// List is every item in the namespace, latest revision each.
	List(ctx context.Context) ([]Head, error)
	// Get is an item's text at a revision (0 = latest). ok is false when there is no such item.
	Get(ctx context.Context, name string, rev int) (it Item, ok bool, err error)
	// Put makes a new revision of name (none when the content is the same) and returns it.
	Put(ctx context.Context, name, content, title string) (rev int, err error)
	// Log is an item's revisions, newest first.
	Log(ctx context.Context, name string) ([]Revision, error)
}

// Head is a listed item: its name in the namespace and latest revision.
type Head struct {
	Name    string
	Rev     int
	Title   string
	Updated string
}

// Item is an item's text at a revision.
type Item struct {
	Name    string
	Rev     int
	Title   string
	Content string
}

// Revision is one entry of an item's history.
type Revision struct {
	Rev   int    `json:"rev"`
	At    string `json:"at"`
	Title string `json:"title"`
}

// Caller is what the gateway client offers (internal/tetherlink).
type Caller interface {
	Configured() bool
	Call(ctx context.Context, tool string, args map[string]any) ([]byte, error)
}

// GatewayCaster reaches Caster through the Tether gateway.
type GatewayCaster struct{ Link Caller }

const namespace = "story"

// maxItem is the largest item read: a scene or a manifest is a page, a round a few.
const maxItem = 512 << 10

func (g GatewayCaster) List(ctx context.Context) ([]Head, error) {
	raw, err := g.Link.Call(ctx, "caster_ls", map[string]any{"ns": namespace})
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
	heads := make([]Head, 0, len(out.Items))
	for _, it := range out.Items {
		name, ok := strings.CutPrefix(it.Ref, "caster://"+namespace+"/")
		if !ok || name == "" {
			continue
		}
		heads = append(heads, Head{Name: name, Rev: it.Rev, Title: it.Title, Updated: it.Updated})
	}
	return heads, nil
}

func (g GatewayCaster) Get(ctx context.Context, name string, rev int) (Item, bool, error) {
	ref := Ref(name)
	if rev > 0 {
		ref = RefAt(name, rev)
	}
	raw, err := g.Link.Call(ctx, "caster_get", map[string]any{"ref": ref, "max_bytes": maxItem})
	if err != nil {
		return Item{}, false, err
	}
	var out struct {
		Content   string `json:"content"`
		State     string `json:"state"`
		Revision  string `json:"revision"`
		Title     string `json:"title"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Item{}, false, fmt.Errorf("caster_get: %w", err)
	}
	if out.State == "not_found" {
		return Item{}, false, nil
	}
	if out.State != "resolved" {
		return Item{}, false, fmt.Errorf("caster_get %s: %s", ref, out.State)
	}
	if out.Truncated {
		return Item{}, false, fmt.Errorf("%s is larger than %d KB", ref, maxItem>>10)
	}
	r, _ := strconv.Atoi(out.Revision)
	return Item{Name: name, Rev: r, Title: out.Title, Content: out.Content}, true, nil
}

func (g GatewayCaster) Put(ctx context.Context, name, content, title string) (int, error) {
	raw, err := g.Link.Call(ctx, "caster_put", map[string]any{
		"name": namespace + "/" + name, "content": content, "mime": "text/markdown",
		"title": title, "source": "basalisk:owner",
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

func (g GatewayCaster) Log(ctx context.Context, name string) ([]Revision, error) {
	raw, err := g.Link.Call(ctx, "caster_log", map[string]any{"name": namespace + "/" + name})
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
