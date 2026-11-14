package work

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Feedback rounds and sign-off on an item, as Story does them for a script (docs/work.md).
// The owner's comments and suggestions collect as a draft round (notes with round 0, kept
// here only). Send round writes them to Caster as r.<item>.<n> and tells the workspace's
// owners; the team answers in that item. Sign-off records the item's text as approved
// (signed in the item, pinned in CAS) and tells the owners. Only the owner's click does
// either: the agent drafts notes, never more.

// RoundName is the name of an item's round n in its workspace's namespace.
func RoundName(id string, n int) string { return "r." + id + "." + strconv.Itoa(n) }

// parseRound reads r.<id>.<n>.
func parseRound(rest string) (id string, n int, ok bool) {
	i := strings.LastIndexByte(rest, '.')
	if i < 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(rest[i+1:])
	if err != nil || n < 1 || !ValidID(rest[:i]) {
		return "", 0, false
	}
	return rest[:i], n, true
}

// NoteKinds a draft note may be.
var NoteKinds = []string{"comment", "suggestion"}

// AddNote puts the owner's (or the agent's, for the owner) note in the item's draft round. A
// suggestion names the passage it replaces, which must be in that section as it is now.
func (s *Store) AddNote(ctx context.Context, ws, id string, n Note) (Note, error) {
	switch {
	case !contains(NoteKinds, n.Kind):
		return Note{}, fmt.Errorf("unknown note kind %q", n.Kind)
	case n.Kind == "suggestion" && (strings.TrimSpace(n.Quote) == "" || n.Section == ""):
		return Note{}, errors.New("a suggestion needs the section and the passage it replaces")
	case n.Kind == "comment" && strings.TrimSpace(n.Text) == "":
		return Note{}, errors.New("a comment needs some words")
	case len(n.Text) > 16<<10 || len(n.Quote) > 32<<10 || len(n.Replace) > 32<<10:
		return Note{}, errors.New("the note is too long")
	}
	d, ok, err := s.Detail(ctx, ws, id)
	if err != nil {
		return Note{}, err
	}
	if !ok {
		return Note{}, ErrNotFound
	}
	if n.Section != "" {
		text, known := d.Sections[n.Section]
		if !known {
			return Note{}, fmt.Errorf("the item has no section %q", n.Section)
		}
		if n.Kind == "suggestion" && !strings.Contains(text, n.Quote) {
			return Note{}, errors.New("that passage is not in the section as it is now")
		}
	}
	n.ID, n.WS, n.Item, n.ItemRev, n.CreatedAt = "n"+NewID(), ws, id, d.Rev, now()
	return n, s.addNote(ctx, n)
}

// EditNote changes a draft note's words.
func (s *Store) EditNote(ctx context.Context, ws, id string, text, replace *string) error {
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
	res, err := s.db.ExecContext(ctx, `UPDATE notes SET `+strings.Join(set, ",")+` WHERE id=? AND ws=? AND round=0`, append(args, id, ws)...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("there is no such draft note")
	}
	return nil
}

// Notifier tells the team: a Tether message to each owner, a comment on the item's task.
type Notifier interface {
	Mail(ctx context.Context, to, subject, body, ref string) error
	Comment(ctx context.Context, task, text string) error
}

// Pinner pins an item's revision in CAS (caster_snap).
type Pinner interface {
	Snap(ctx context.Context, ref string) (string, error)
}

// RoundNote is a note as the round item carries it (as in story/v1).
type RoundNote struct {
	ID      string `yaml:"id" json:"id"`
	Kind    string `yaml:"kind" json:"kind"`
	Section string `yaml:"section,omitempty" json:"section,omitempty"`
	Quote   string `yaml:"quote,omitempty" json:"quote,omitempty"`
	Replace string `yaml:"replace,omitempty" json:"replace,omitempty"`
	Text    string `yaml:"text,omitempty" json:"text,omitempty"`
	State   string `yaml:"state" json:"state"` // open, done, declined
	Reply   string `yaml:"reply,omitempty" json:"reply,omitempty"`
}

// Sent is what Send round did.
type Sent struct {
	Round     int      `json:"round"`
	Ref       string   `json:"ref"`
	Notes     int      `json:"notes"`
	Mailed    []string `json:"mailed"`
	NotMailed []string `json:"not_mailed"` // owners that are not Tether addresses, or whose mail failed
	Commented string   `json:"commented,omitempty"`
}

// RoundFrom is the address a round's mail goes out from: the owner's, as the feedback is his.
const RoundFrom = "msg://user/local/operator"

// SendRound writes the item's draft round to Caster and tells the workspace's owners.
func (s *Store) SendRound(ctx context.Context, ws, id string, tell Notifier) (Sent, error) {
	if s.caster == nil {
		return Sent{}, ErrNoCaster
	}
	drafts, err := s.Notes(ctx, ws, id)
	if err != nil {
		return Sent{}, err
	}
	if len(drafts) == 0 {
		return Sent{}, errors.New("there is nothing in the draft round")
	}
	s.mu.Lock()
	ns, err := s.space(ctx, ws)
	if err != nil {
		s.mu.Unlock()
		return Sent{}, err
	}
	it, ok, err := s.fresh(ctx, ns, ItemName(id))
	if err != nil || !ok {
		s.mu.Unlock()
		if err == nil {
			err = ErrNotFound
		}
		return Sent{}, err
	}
	d, err := ParseDoc(it.Content)
	if err != nil {
		s.mu.Unlock()
		return Sent{}, fmt.Errorf("the item cannot be read: %w", err)
	}
	// The next free round: never write over one that is there (sent from elsewhere).
	round := 1
	if rs, _ := s.rounds(ctx, ns, id); len(rs) > 0 {
		round = rs[0].Round + 1
	}
	for {
		_, there, err := s.caster.Get(ctx, ns, RoundName(id, round), 0)
		if err != nil {
			s.mu.Unlock()
			return Sent{}, fmt.Errorf("Caster could not be read: %w", err)
		}
		if !there {
			break
		}
		round++
	}
	notes := make([]RoundNote, 0, len(drafts))
	for _, n := range drafts {
		notes = append(notes, RoundNote{ID: n.ID, Kind: n.Kind, Section: n.Section, Quote: n.Quote, Replace: n.Replace, Text: n.Text, State: "open"})
	}
	title := strings.TrimSpace(d.Str("title"))
	if title == "" {
		title = id
	}
	doc, _ := ParseDoc("")
	for _, kv := range []struct {
		k string
		v any
	}{{"work", "v1"}, {"item", id}, {"round", round}, {"sent_at", time.Now().UTC().Format(time.RFC3339)}, {"from", RoundFrom}, {"item_rev", it.Rev}, {"digest", textDigest(d)}, {"notes", notes}} {
		if err := doc.Set(kv.k, kv.v); err != nil {
			s.mu.Unlock()
			return Sent{}, err
		}
	}
	doc.Body = roundProse(title, round, notes)
	name := RoundName(id, round)
	if _, err := s.write(ctx, ns, name, doc.String(), fmt.Sprintf("%s: round %d", title, round)); err != nil {
		s.mu.Unlock()
		return Sent{}, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE notes SET round=? WHERE ws=? AND item=? AND round=0`, round, ws, id); err != nil {
		s.mu.Unlock()
		return Sent{}, err
	}
	w, _ := s.workspace(ctx, ws)
	s.mu.Unlock()

	out := Sent{Round: round, Ref: Ref(ns, name), Notes: len(notes), Mailed: []string{}, NotMailed: []string{}}
	subject := fmt.Sprintf("Work: round %d on %s (%d note%s)", round, title, len(notes), plural(len(notes)))
	body := fmt.Sprintf("Round %d of feedback on \"%s\" (%s, in %s) is in Caster: %s\n\nRead it with caster_get. Answer each note by putting a new revision of the item (%s) and setting the note's state (done or declined) and reply in the same round item. The contract is work/v1 (docs/work.md).\n\n%s",
		round, title, Ref(ns, ItemName(id)), w.Title, out.Ref, Ref(ns, ItemName(id)), doc.Body)
	out.Mailed, out.NotMailed = mailOwners(ctx, tell, w.Owners, subject, body, out.Ref)
	if task := d.Str("task"); task != "" && tell != nil {
		if tell.Comment(ctx, task, fmt.Sprintf("Work round %d sent on \"%s\": %d note%s. %s", round, title, len(notes), plural(len(notes)), out.Ref)) == nil {
			out.Commented = task
		}
	}
	return out, nil
}

func mailOwners(ctx context.Context, tell Notifier, owners []string, subject, body, ref string) (mailed, not []string) {
	mailed, not = []string{}, []string{}
	for _, o := range owners {
		if tell == nil || !strings.HasPrefix(o, "msg://") || tell.Mail(ctx, o, subject, body, ref) != nil {
			not = append(not, o)
			continue
		}
		mailed = append(mailed, o)
	}
	return mailed, not
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
		where := "The item"
		if n.Section != "" {
			where = n.Section
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

// RoundView is a sent round as the team left it.
type RoundView struct {
	Round   int         `json:"round"`
	Ref     string      `json:"ref"`
	SentAt  string      `json:"sent_at"`
	ItemRev int         `json:"item_rev"`
	Digest  string      `json:"digest,omitempty"` // of the item's text when sent
	Notes   []RoundNote `json:"notes"`
	Open    int         `json:"open"`
	Problem string      `json:"problem,omitempty"`
}

// Rounds are an item's sent rounds, newest first (from the copy).
func (s *Store) Rounds(ctx context.Context, ws, id string) ([]RoundView, error) {
	ns := s.NSOf(ctx, ws)
	if ns == "" {
		return nil, ErrNotFound
	}
	return s.rounds(ctx, ns, id)
}

func (s *Store) rounds(ctx context.Context, ns, id string) ([]RoundView, error) {
	rows, err := s.rows(ctx, ns, "r."+id+".")
	if err != nil {
		return nil, err
	}
	out := []RoundView{}
	for _, r := range rows {
		rid, n, ok := parseRound(strings.TrimPrefix(r.name, "r."))
		if !ok || rid != id {
			continue
		}
		v := RoundView{Round: n, Ref: Ref(ns, r.name), Notes: []RoundNote{}}
		if d, err := ParseDoc(r.content); err != nil {
			v.Problem = err.Error()
		} else {
			v.SentAt, v.ItemRev, v.Digest = d.Str("sent_at"), int(d.Num("item_rev")), d.Str("digest")
			if err := d.Decode("notes", &v.Notes); err != nil {
				v.Problem = "its notes cannot be read: " + err.Error()
			}
		}
		for _, x := range v.Notes {
			if x.State == "" || x.State == "open" {
				v.Open++
			}
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Round > out[j].Round })
	return out, nil
}

// ---- Sign-off ----

// SignedOff is the sign-off an item carries (its `signed` key).
type SignedOff struct {
	At     string `yaml:"at" json:"at"`
	Rev    int    `yaml:"rev" json:"rev"`       // the revision that was approved
	Digest string `yaml:"digest" json:"digest"` // of its title and text: a later change makes it stale
	Pinned string `yaml:"pinned,omitempty" json:"pinned,omitempty"`
	Round  int    `yaml:"round,omitempty" json:"round,omitempty"` // the last round sent before it
}

// textDigest is what a sign-off approves: the title and the body, not the stage or fields.
func textDigest(d *Doc) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(d.Str("title")) + "\n" + strings.TrimSpace(d.Body)))
	return hex.EncodeToString(h[:8])
}

// ErrOpenNotes: sign-off while the last round has notes the team has not answered, or
// cannot be read (then nothing says its notes were answered).
type ErrOpenNotes struct {
	Round, Open int
	Problem     string
}

func (e *ErrOpenNotes) Error() string {
	if e.Problem != "" {
		return fmt.Sprintf("round %d cannot be read (%s), so its answers are unknown", e.Round, e.Problem)
	}
	return fmt.Sprintf("round %d still has %d open note%s", e.Round, e.Open, plural(e.Open))
}

// Signed is what sign-off did.
type Signed struct {
	SignedOff
	Stage     string   `json:"stage,omitempty"` // the stage it moved to, if it moved
	Mailed    []string `json:"mailed"`
	NotMailed []string `json:"not_mailed"`
	Problem   string   `json:"problem,omitempty"`
}

// SignOff approves the item as it is now: the revision and a digest of its text are kept in
// the item (`signed`), an item at the review stage moves to the next one, the approved
// revision is pinned in CAS, and the owners are told. When the last round still has open
// notes, it is refused unless anyway is set. Draft notes are not sent; they stay.
func (s *Store) SignOff(ctx context.Context, ws, id string, anyway bool, pin Pinner, tell Notifier) (Signed, error) {
	out := Signed{Mailed: []string{}, NotMailed: []string{}}
	if s.caster == nil {
		return out, ErrNoCaster
	}
	s.mu.Lock()
	ns, err := s.space(ctx, ws)
	if err != nil {
		s.mu.Unlock()
		return out, err
	}
	rs, _ := s.rounds(ctx, ns, id)
	if len(rs) > 0 {
		out.Round = rs[0].Round
		if (rs[0].Open > 0 || rs[0].Problem != "") && !anyway {
			s.mu.Unlock()
			return out, &ErrOpenNotes{Round: rs[0].Round, Open: rs[0].Open, Problem: rs[0].Problem}
		}
	}
	kinds, _ := s.Kinds(ctx, ns)
	var title string
	rev, err := s.changeLocked(ctx, ns, id, func(d *Doc) error {
		title = strings.TrimSpace(d.Str("title"))
		out.Digest = textDigest(d)
		out.At = time.Now().UTC().Format(time.RFC3339)
		if st := kinds[d.Str("kind")].Stages; d.Str("stage") == "review" {
			for i, x := range st {
				if x == "review" && i+1 < len(st) {
					out.Stage = st[i+1]
					_ = d.Set("stage", out.Stage)
				}
			}
		}
		return d.Set("signed", SignedOff{At: out.At, Digest: out.Digest, Round: out.Round})
	})
	if err != nil {
		s.mu.Unlock()
		return out, err
	}
	out.Rev = rev
	w, _ := s.workspace(ctx, ws)
	s.mu.Unlock()
	if title == "" {
		title = id
	}
	ref := Ref(ns, ItemName(id))
	if pin != nil {
		if p, err := pin.Snap(ctx, fmt.Sprintf("%s@%d", ref, rev)); err == nil {
			out.Pinned = p
		} else {
			out.Problem = "The approved revision was not pinned: " + err.Error()
		}
	}
	// Record the revision and the pin. Its own write makes a newer revision; the digest, not
	// the revision, says whether the text has moved on since.
	if _, err := s.change(ctx, ws, id, func(d *Doc) error {
		return d.Set("signed", out.SignedOff)
	}); err != nil {
		out.Problem = strings.TrimSpace(out.Problem + " The sign-off's revision was not recorded: " + err.Error())
	}
	msg := fmt.Sprintf("\"%s\" (%s) is signed off at revision %d.", title, ref, rev)
	if out.Pinned != "" {
		msg += " Pinned: " + out.Pinned + "."
	}
	out.Mailed, out.NotMailed = mailOwners(ctx, tell, w.Owners, "Work: signed off: "+title, msg, ref)
	return out, nil
}

// signedOf reads an item's sign-off and whether its text moved on since.
func signedOf(d *Doc) (*SignedOff, bool) {
	if d == nil || !d.Has("signed") {
		return nil, false
	}
	var so SignedOff
	if d.Decode("signed", &so) != nil || so.Digest == "" {
		return nil, false
	}
	return &so, so.Digest != textDigest(d)
}

// Unsign takes the sign-off off an item (the stage stays).
func (s *Store) Unsign(ctx context.Context, ws, id string) error {
	_, err := s.change(ctx, ws, id, func(d *Doc) error {
		d.Del("signed")
		return nil
	})
	return err
}

// SetOwners sets the Tether addresses a workspace's rounds and sign-offs are mailed to.
func (s *Store) SetOwners(ctx context.Context, ws string, owners []string) error {
	if s.caster == nil {
		return ErrNoCaster
	}
	clean := []string{}
	for _, o := range owners {
		o = strings.TrimSpace(o)
		if o == "" || contains(clean, o) {
			continue
		}
		if !strings.HasPrefix(o, "msg://") || strings.ContainsAny(o, " \t\n") || len(o) > 200 {
			return fmt.Errorf("%q is not a Tether address (msg://…)", o)
		}
		clean = append(clean, o)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok, err := s.fresh(ctx, IndexNS, ws)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	d, err := ParseDoc(it.Content)
	if err != nil {
		return err
	}
	if err := d.Set("owners", clean); err != nil {
		return err
	}
	_, err = s.write(ctx, IndexNS, ws, d.String(), it.Title)
	return err
}

// ---- Through the gateway ----

// GatewayNotifier tells the team through the Tether gateway.
type GatewayNotifier struct{ Link Caller }

func (g GatewayNotifier) Mail(ctx context.Context, to, subject, body, ref string) error {
	payload, _ := json.Marshal(map[string]string{"subject": subject, "body": body, "ref": ref})
	_, err := g.Link.Call(ctx, "tether_message_send", map[string]any{
		"from": RoundFrom, "to": to, "kind": "request", "thread_id": "work", "payload_json": string(payload),
	})
	return err
}

func (g GatewayNotifier) Comment(ctx context.Context, task, text string) error {
	_, err := g.Link.Call(ctx, "torque_comment_add", map[string]any{"entity_type": "task", "entity_id": task, "author": "basalisk", "content": text})
	return err
}

// Snap pins a ref in CAS.
func (g GatewayNotifier) Snap(ctx context.Context, ref string) (string, error) {
	raw, err := g.Link.Call(ctx, "caster_snap", map[string]any{"ref": ref})
	if err != nil {
		return "", err
	}
	var out struct {
		Ref string `json:"ref"`
		CAS string `json:"cas"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if out.CAS != "" && !strings.HasPrefix(out.Ref, "cas://") {
		return out.CAS, nil
	}
	return out.Ref, nil
}
