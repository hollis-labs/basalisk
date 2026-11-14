package story

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Feedback rounds. The owner's comments, suggestions, moves, cuts and additions collect as a
// draft round (notes with round 0, kept here only). Send round writes them to Caster as
// story/<key>.round-<n>, moves the script to "revising", and tells the team.

// NoteKinds a note may be.
var NoteKinds = []string{"comment", "suggestion", "move", "cut", "add"}

// AddNote puts a note in the draft round. A suggestion needs the passage it replaces.
func (s *Store) AddNote(ctx context.Context, n Note) (Note, error) {
	known := false
	for _, k := range NoteKinds {
		known = known || k == n.Kind
	}
	switch {
	case !known:
		return Note{}, fmt.Errorf("unknown note kind %q", n.Kind)
	case n.Kind == "suggestion" && strings.TrimSpace(n.Quote) == "":
		return Note{}, errors.New("a suggestion needs the passage it replaces")
	case n.Kind == "comment" && strings.TrimSpace(n.Text) == "":
		return Note{}, errors.New("a comment needs some words")
	case len(n.Text) > 16<<10 || len(n.Quote) > 32<<10 || len(n.Replace) > 32<<10:
		return Note{}, errors.New("the note is too long")
	case n.Scene != "" && !ValidSceneID(n.Scene):
		return Note{}, errors.New("unknown scene")
	}
	if n.Section != "" {
		ok := false
		for _, sec := range Sections {
			ok = ok || sec == n.Section
		}
		if !ok {
			return Note{}, fmt.Errorf("unknown section %q", n.Section)
		}
	}
	if n.Scene != "" && n.SceneRev == 0 {
		var rev int
		_ = s.db.QueryRowContext(ctx, `SELECT rev FROM items WHERE name=?`, SceneName(n.Key, n.Scene)).Scan(&rev)
		n.SceneRev = rev
	}
	n.ID, n.CreatedAt, n.Round = newID("n"), now(), 0
	return n, s.addNote(ctx, n)
}

// EditNote changes a draft note's words (its text, or a suggestion's replacement).
func (s *Store) EditNote(ctx context.Context, key, id string, text, replace *string) error {
	set, args := []string{}, []any{}
	if text != nil {
		set, args = append(set, "text=?"), append(args, *text)
	}
	if replace != nil {
		set, args = append(set, "replace=?"), append(args, *replace)
	}
	if len(set) == 0 {
		return nil
	}
	res, err := s.db.ExecContext(ctx, `UPDATE notes SET `+strings.Join(set, ",")+` WHERE id=? AND key=? AND round=0`, append(args, id, key)...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("there is no such draft note")
	}
	return nil
}

// DropNote takes a note out of the draft round.
func (s *Store) DropNote(ctx context.Context, key, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM notes WHERE id=? AND key=? AND round=0`, id, key)
	return err
}

// noteStructure records a move, cut or addition the owner made, for the round. A newer move
// replaces the draft one (the team needs the order as it is now, not each step).
func (s *Store) noteStructure(ctx context.Context, key, kind, scene, text string) {
	if kind == "move" {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM notes WHERE key=? AND round=0 AND kind='move'`, key)
	}
	if kind == "add" || kind == "cut" {
		// Cutting a scene added in this round, or restoring one cut in it, cancels out.
		other := map[string]string{"add": "cut", "cut": "add"}[kind]
		res, _ := s.db.ExecContext(ctx, `DELETE FROM notes WHERE key=? AND round=0 AND kind=? AND scene=?`, key, other, scene)
		if n, _ := res.RowsAffected(); n > 0 {
			return
		}
	}
	_ = s.addNote(ctx, Note{Key: key, Scene: scene, Kind: kind, Text: text})
}

// Notifier tells the team: a Tether message to each owner, a comment on the script's task.
type Notifier interface {
	Mail(ctx context.Context, to, subject, body string, ref string) error
	Comment(ctx context.Context, task, text string) error
}

// RoundNote is a note as the round item carries it (contract story/v1).
type RoundNote struct {
	ID       string `yaml:"id" json:"id"`
	Scene    string `yaml:"scene,omitempty" json:"scene,omitempty"`
	SceneRev int    `yaml:"scene_rev,omitempty" json:"scene_rev,omitempty"`
	Kind     string `yaml:"kind" json:"kind"`
	Section  string `yaml:"section,omitempty" json:"section,omitempty"`
	Quote    string `yaml:"quote,omitempty" json:"quote,omitempty"`
	Replace  string `yaml:"replace,omitempty" json:"replace,omitempty"`
	Text     string `yaml:"text,omitempty" json:"text,omitempty"`
	State    string `yaml:"state" json:"state"`
	Reply    string `yaml:"reply,omitempty" json:"reply,omitempty"`
}

// Sent is what Send round did.
type Sent struct {
	Round     int      `json:"round"`
	Ref       string   `json:"ref"`
	Notes     int      `json:"notes"`
	Mailed    []string `json:"mailed"`
	NotMailed []string `json:"not_mailed"` // owners that are not Tether addresses, or whose mail failed
	Commented string   `json:"commented,omitempty"`
	Problem   string   `json:"problem,omitempty"`
}

// SendRound writes the draft round to Caster, moves the script to revising, and tells its
// owners. from is the address the mail goes out from.
func (s *Store) SendRound(ctx context.Context, key, from string, tell Notifier) (Sent, error) {
	if s.caster == nil {
		return Sent{}, ErrNoCaster
	}
	drafts, err := s.Drafts(ctx, key)
	if err != nil {
		return Sent{}, err
	}
	if len(drafts) == 0 {
		return Sent{}, errors.New("there is nothing in the draft round")
	}
	s.mu.Lock()
	it, ok, err := s.fresh(ctx, ManifestName(key))
	if err != nil || !ok {
		s.mu.Unlock()
		if err == nil {
			err = ErrNotFound
		}
		return Sent{}, err
	}
	md, err := ParseDoc(it.Content)
	if err != nil {
		s.mu.Unlock()
		return Sent{}, fmt.Errorf("the script's manifest cannot be read: %w", err)
	}
	round := int(md.Num("round")) + 1
	// Never write over a round that is there (sent from elsewhere): take the next free one.
	for {
		_, there, err := s.caster.Get(ctx, RoundName(key, round), 0)
		if err != nil {
			s.mu.Unlock()
			return Sent{}, fmt.Errorf("Caster could not be read: %w", err)
		}
		if !there {
			break
		}
		round++
	}
	// Every scene's revision as it is now: what "changed since this round" is measured from.
	revs := map[string]int{}
	if heads, err := s.caster.List(ctx); err == nil {
		for _, h := range heads {
			if n := ParseName(h.Name); n.Kind == KindScene && n.Key() == key {
				revs[n.Scene] = h.Rev
			}
		}
	} else {
		s.mu.Unlock()
		return Sent{}, fmt.Errorf("Caster could not be listed: %w", err)
	}
	listed := map[string]int{}
	for _, id := range md.Strings("scenes") {
		listed[id] = revs[id]
	}
	notes := make([]RoundNote, 0, len(drafts))
	for _, d := range drafts {
		rev := d.SceneRev
		if d.Scene != "" && revs[d.Scene] > 0 && rev == 0 {
			rev = revs[d.Scene]
		}
		notes = append(notes, RoundNote{ID: d.ID, Scene: d.Scene, SceneRev: rev, Kind: d.Kind, Section: d.Section, Quote: d.Quote, Replace: d.Replace, Text: d.Text, State: "open"})
	}
	sentAt := time.Now().UTC().Format(time.RFC3339)
	doc, _ := ParseDoc("")
	for _, kv := range []struct {
		k string
		v any
	}{{"story", "v1"}, {"round", round}, {"sent_at", sentAt}, {"from", from}, {"manifest_rev", it.Rev}, {"revs", listed}, {"notes", notes}} {
		if err := doc.Set(kv.k, kv.v); err != nil {
			s.mu.Unlock()
			return Sent{}, err
		}
	}
	doc.Body = roundProse(md.Str("title"), round, notes)
	name := RoundName(key, round)
	if _, err := s.write(ctx, name, doc.String(), fmt.Sprintf("%s: round %d", md.Str("title"), round)); err != nil {
		s.mu.Unlock()
		return Sent{}, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE notes SET round=? WHERE key=? AND round=0`, round, key); err != nil {
		s.mu.Unlock()
		return Sent{}, err
	}
	s.mu.Unlock()
	out := Sent{Round: round, Ref: Ref(name), Notes: len(notes), Mailed: []string{}, NotMailed: []string{}}
	if err := s.changeManifest(ctx, key, func(d *Doc) error {
		_ = d.Set("round", round)
		return d.Set("stage", "revising")
	}); err != nil {
		out.Problem = "The round was written, but the script's stage was not changed: " + err.Error()
	}

	// Tell the team.
	title := md.Str("title")
	scenes := map[string]bool{}
	for _, n := range notes {
		if n.Scene != "" {
			scenes[n.Scene] = true
		}
	}
	subject := fmt.Sprintf("Story: round %d on %s (%d note%s on %d scene%s)", round, title, len(notes), plural(len(notes)), len(scenes), plural(len(scenes)))
	body := fmt.Sprintf("Round %d of feedback on \"%s\" is in Caster: %s\n\nRead it with caster_get. Answer each note by putting new scene revisions (story/%s.<scene>) and setting the note's state (done or declined) and reply in the same round item. The contract is story/v1 (docs/story.md).\n\n%s",
		round, title, out.Ref, key, doc.Body)
	for _, owner := range md.Strings("owners") {
		if tell == nil || !strings.HasPrefix(owner, "msg://") {
			out.NotMailed = append(out.NotMailed, owner)
			continue
		}
		if err := tell.Mail(ctx, owner, subject, body, out.Ref); err != nil {
			out.NotMailed = append(out.NotMailed, owner)
			continue
		}
		out.Mailed = append(out.Mailed, owner)
	}
	if task := md.Str("task"); task != "" && tell != nil {
		if err := tell.Comment(ctx, task, fmt.Sprintf("Story round %d sent: %d note%s. %s", round, len(notes), plural(len(notes)), out.Ref)); err == nil {
			out.Commented = task
		}
	}
	return out, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// roundProse is the round written out for reading.
func roundProse(title string, round int, notes []RoundNote) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s: round %d\n", title, round)
	for _, n := range notes {
		where := "The script"
		if n.Scene != "" {
			where = "Scene " + n.Scene
			if n.Section != "" {
				where += ", " + n.Section
			}
		}
		fmt.Fprintf(&b, "\n- **%s** (%s, %s)", where, n.Kind, n.ID)
		if n.Text != "" {
			fmt.Fprintf(&b, ": %s", n.Text)
		}
		if n.Quote != "" {
			fmt.Fprintf(&b, "\n  - instead of: %s", oneLine(n.Quote))
		}
		if n.Kind == "suggestion" {
			fmt.Fprintf(&b, "\n  - put: %s", oneLine(n.Replace))
		}
	}
	b.WriteString("\n")
	return b.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// GatewayNotifier tells the team through the Tether gateway.
type GatewayNotifier struct{ Link Caller }

func (g GatewayNotifier) Mail(ctx context.Context, to, subject, body, ref string) error {
	payload, _ := json.Marshal(map[string]string{"subject": subject, "body": body, "ref": ref})
	_, err := g.Link.Call(ctx, "tether_message_send", map[string]any{
		"from": RoundFrom, "to": to, "kind": "request", "thread_id": "story", "payload_json": string(payload),
	})
	return err
}

func (g GatewayNotifier) Comment(ctx context.Context, task, text string) error {
	_, err := g.Link.Call(ctx, "torque_comment_add", map[string]any{"entity_type": "task", "entity_id": task, "author": "basalisk", "content": text})
	return err
}

// RoundFrom is the address a round's mail is sent from: the owner's, as the feedback is his.
const RoundFrom = "msg://user/local/operator"

// Rounds are the rounds of a script in Caster, newest first, with each note's state as the
// team left it.
type RoundView struct {
	Round   int            `json:"round"`
	Ref     string         `json:"ref"`
	SentAt  string         `json:"sent_at"`
	Revs    map[string]int `json:"revs"`
	Notes   []RoundNote    `json:"notes"`
	Problem string         `json:"problem,omitempty"`
}

func (s *Store) Rounds(ctx context.Context, key string) ([]RoundView, error) {
	rows, err := s.rows(ctx, "name LIKE ?", key+".round-%")
	if err != nil {
		return nil, err
	}
	out := []RoundView{}
	for _, r := range rows {
		n := ParseName(r.name)
		if n.Kind != KindRound || n.Key() != key {
			continue
		}
		v := RoundView{Round: n.Round, Ref: Ref(r.name), Revs: map[string]int{}, Notes: []RoundNote{}}
		doc, err := ParseDoc(r.content)
		if err != nil {
			v.Problem = err.Error()
		} else {
			v.SentAt = doc.Str("sent_at")
			_ = doc.Decode("revs", &v.Revs)
			if err := doc.Decode("notes", &v.Notes); err != nil {
				v.Problem = "its notes cannot be read: " + err.Error()
			}
		}
		out = append(out, v)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Round > out[i].Round {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}
