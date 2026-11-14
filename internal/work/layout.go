package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

// A canvas (and later a flow) keeps its hand-made arrangement in its view item's layout:
//
//	layout:
//	  cards:  {<item id>: {x, y}}              cards placed on the canvas
//	  stacks: [{id, title, x, y, items, bind}] titled piles of cards, in order
//	  notes:  [{id, x, y, text}]               free notes
//
// Other layout keys (an outline's order) are kept as they are. A card is either on the
// canvas or in one stack. A stack may be bound to a stage or a parent ({key: stage|parent,
// value}): dropping a card there also sets that value on the item (the app does that through
// Move and Place); unbound, a stack is pure arrangement.

// Pos is a place on the canvas.
type Pos struct {
	X float64 `json:"x" yaml:"x"`
	Y float64 `json:"y" yaml:"y"`
}

// Bind is what a stack sets on a card dropped into it.
type Bind struct {
	Key   string `json:"key" yaml:"key"` // stage | parent
	Value string `json:"value" yaml:"value"`
}

// Stack is a titled pile of cards.
type Stack struct {
	ID    string   `json:"id" yaml:"id"`
	Title string   `json:"title" yaml:"title"`
	X     float64  `json:"x" yaml:"x"`
	Y     float64  `json:"y" yaml:"y"`
	Items []string `json:"items" yaml:"items"`
	Bind  *Bind    `json:"bind,omitempty" yaml:"bind,omitempty"`
}

// CanvasNote is a free note on the canvas.
type CanvasNote struct {
	ID   string  `json:"id" yaml:"id"`
	X    float64 `json:"x" yaml:"x"`
	Y    float64 `json:"y" yaml:"y"`
	Text string  `json:"text" yaml:"text"`
}

// LayoutPatch changes a layout by id: a card's place, a stack or a note set (null takes it
// out). Stacks and notes keep their order; a new one goes at the end.
type LayoutPatch struct {
	Cards  map[string]*Pos   `json:"cards"`
	Stacks map[string]*Stack `json:"stacks"`
	Notes  map[string]*CanvasNote  `json:"notes"`
}

const (
	maxCoord    = 1e6
	maxOnCanvas = 2000
)

func okPos(x, y float64) bool {
	return !math.IsNaN(x) && !math.IsNaN(y) && math.Abs(x) <= maxCoord && math.Abs(y) <= maxCoord
}

func (p LayoutPatch) check() error {
	if len(p.Cards)+len(p.Stacks)+len(p.Notes) == 0 {
		return errors.New("the change is empty")
	}
	if len(p.Cards) > maxOnCanvas || len(p.Stacks) > maxOnCanvas || len(p.Notes) > maxOnCanvas {
		return errors.New("too many changes at once")
	}
	for id, pos := range p.Cards {
		if !ValidID(id) || (pos != nil && !okPos(pos.X, pos.Y)) {
			return fmt.Errorf("card %q: not an item id or not a place on the canvas", id)
		}
	}
	for id, st := range p.Stacks {
		if !ValidID(id) {
			return fmt.Errorf("stack %q: not an id", id)
		}
		if st == nil {
			continue
		}
		if st.ID != id || !okPos(st.X, st.Y) || len(st.Title) > 200 || strings.ContainsAny(st.Title, "\n\r") || len(st.Items) > maxOnCanvas {
			return fmt.Errorf("stack %s cannot be kept as given", id)
		}
		for _, it := range st.Items {
			if !ValidID(it) {
				return fmt.Errorf("stack %s: %q is not an item id", id, it)
			}
		}
		if b := st.Bind; b != nil {
			switch {
			case b.Key == "stage" && b.Value != "" && len(b.Value) <= 64:
			case b.Key == "parent" && ValidID(b.Value):
			default:
				return fmt.Errorf("stack %s: a stack is bound to a stage or a parent", id)
			}
		}
	}
	for id, n := range p.Notes {
		if !ValidID(id) {
			return fmt.Errorf("note %q: not an id", id)
		}
		if n != nil && (n.ID != id || !okPos(n.X, n.Y) || len(n.Text) > 4000) {
			return fmt.Errorf("note %s cannot be kept as given", id)
		}
	}
	return nil
}

// PatchLayout applies p to a view's layout as it is now: one new revision of the view.
func (s *Store) PatchLayout(ctx context.Context, ws, view string, p LayoutPatch) (int, error) {
	if s.caster == nil {
		return 0, ErrNoCaster
	}
	if !ValidID(view) {
		return 0, errors.New("that is not a view id")
	}
	if err := p.check(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, err := s.space(ctx, ws)
	if err != nil {
		return 0, err
	}
	it, ok, err := s.fresh(ctx, ns, ViewName(view))
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, ErrNotFound
	}
	d, err := ParsePlain(it.Content)
	if err != nil {
		return 0, fmt.Errorf("the view cannot be read: %w", err)
	}
	layout := map[string]any{}
	_ = d.Decode("layout", &layout)
	var cur struct {
		Cards  map[string]Pos `json:"cards"`
		Stacks []Stack        `json:"stacks"`
		Notes  []CanvasNote         `json:"notes"`
	}
	// Through JSON: the layout came from YAML as plain maps and lists.
	if b, err := json.Marshal(map[string]any{"cards": layout["cards"], "stacks": layout["stacks"], "notes": layout["notes"]}); err == nil {
		_ = json.Unmarshal(b, &cur)
	}
	if cur.Cards == nil {
		cur.Cards = map[string]Pos{}
	}
	for id, pos := range p.Cards {
		if pos == nil {
			delete(cur.Cards, id)
		} else {
			cur.Cards[id] = Pos{X: math.Round(pos.X), Y: math.Round(pos.Y)}
		}
	}
	stacks := make([]Stack, 0, len(cur.Stacks)+len(p.Stacks))
	seen := map[string]bool{}
	for _, st := range cur.Stacks {
		if ch, has := p.Stacks[st.ID]; has {
			seen[st.ID] = true
			if ch == nil {
				continue
			}
			st = *ch
		}
		stacks = append(stacks, st)
	}
	for _, id := range sortedKeys(p.Stacks) {
		if ch := p.Stacks[id]; ch != nil && !seen[id] {
			stacks = append(stacks, *ch)
		}
	}
	notes := make([]CanvasNote, 0, len(cur.Notes)+len(p.Notes))
	seen = map[string]bool{}
	for _, n := range cur.Notes {
		if ch, has := p.Notes[n.ID]; has {
			seen[n.ID] = true
			if ch == nil {
				continue
			}
			n = *ch
		}
		notes = append(notes, n)
	}
	for _, id := range sortedKeys(p.Notes) {
		if ch := p.Notes[id]; ch != nil && !seen[id] {
			notes = append(notes, *ch)
		}
	}
	// A card is in one place: on the canvas or in one stack. What this patch says wins: a
	// card it places leaves every stack; a stack it sets takes its cards from the canvas and
	// from every other stack.
	for i := range stacks {
		stacks[i].X, stacks[i].Y = math.Round(stacks[i].X), math.Round(stacks[i].Y)
		seen := map[string]bool{}
		kept := []string{}
		for _, it := range stacks[i].Items {
			if !seen[it] {
				seen[it] = true
				kept = append(kept, it)
			}
		}
		stacks[i].Items = kept
	}
	for _, id := range sortedKeys(p.Stacks) {
		st := p.Stacks[id]
		if st == nil {
			continue
		}
		for _, it := range st.Items {
			delete(cur.Cards, it)
			for i := range stacks {
				if stacks[i].ID != id {
					stacks[i].Items = without(stacks[i].Items, it)
				}
			}
		}
	}
	for it, pos := range p.Cards {
		if pos != nil {
			for i := range stacks {
				stacks[i].Items = without(stacks[i].Items, it)
			}
		}
	}
	for i := range notes {
		notes[i].X, notes[i].Y = math.Round(notes[i].X), math.Round(notes[i].Y)
	}
	layout["cards"], layout["stacks"], layout["notes"] = cur.Cards, stacks, notes
	if err := d.Set("layout", layout); err != nil {
		return 0, err
	}
	title := d.Str("title")
	if title == "" {
		title = d.Str("view")
	}
	return s.write(ctx, ns, ViewName(view), d.String(), title)
}

func without(list []string, v string) []string {
	out := make([]string, 0, len(list))
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func sortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
