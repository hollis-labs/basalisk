package story

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Changes since the last round, scene approval, and sign-off.

// Since is how a script stands against its last round and its sign-off: per scene, the
// revision the last round was sent against (0 when the scene is newer than the round), and
// the revision pinned at sign-off.
type Since struct {
	Round     int            `json:"round"`      // the last round sent (0: none)
	RoundRevs map[string]int `json:"round_revs"` // scene -> revision when it was sent
	Approved  map[string]int `json:"approved"`   // scene -> revision pinned at sign-off
	Changed   []string       `json:"changed"`    // scenes revised since the last round
	SinceSign []string       `json:"since_sign"` // scenes revised since sign-off
	SignedOff bool           `json:"signed_off"` // stage approved
	StaleSign bool           `json:"stale_sign"` // signed off, and changed since
}

// SinceOf works out Since for a script view (from the copy).
func (s *Store) SinceOf(ctx context.Context, sc Script) Since {
	out := Since{RoundRevs: map[string]int{}, Approved: map[string]int{}, Changed: []string{}, SinceSign: []string{}}
	rounds, _ := s.Rounds(ctx, sc.Key)
	if len(rounds) > 0 {
		out.Round, out.RoundRevs = rounds[0].Round, rounds[0].Revs
	}
	if it, ok, _ := s.copyOf(ctx, ManifestName(sc.Key)); ok {
		if d, err := ParseDoc(it.Content); err == nil {
			_ = d.Decode("approved", &out.Approved)
		}
	}
	if out.Approved == nil {
		out.Approved = map[string]int{}
	}
	out.SignedOff = sc.Stage == "approved"
	for _, x := range sc.List {
		if x.Missing {
			continue
		}
		if r, ok := out.RoundRevs[x.ID]; out.Round > 0 && (!ok || x.Rev > r) {
			out.Changed = append(out.Changed, x.ID)
		}
		if r, ok := out.Approved[x.ID]; len(out.Approved) > 0 && (!ok || x.Rev != r) {
			out.SinceSign = append(out.SinceSign, x.ID)
		}
	}
	out.StaleSign = out.SignedOff && len(out.SinceSign) > 0
	return out
}

func (s *Store) copyOf(ctx context.Context, name string) (Item, bool, error) {
	var it Item
	err := s.db.QueryRowContext(ctx, `SELECT name, rev, title, content FROM items WHERE name=?`, name).Scan(&it.Name, &it.Rev, &it.Title, &it.Content)
	if err != nil {
		return Item{}, false, nil
	}
	return it, true, nil
}

// ApproveScene sets a scene's status to approved (or, with approve false, back to ready).
// It is the owner's verdict on what he saw: when the scene moved on since baseRev, nothing
// is written and *Conflict says so.
func (s *Store) ApproveScene(ctx context.Context, key, id string, baseRev int, approve bool) (int, error) {
	if s.caster == nil {
		return 0, ErrNoCaster
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	name := SceneName(key, id)
	it, ok, err := s.fresh(ctx, name)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("there is no scene %s", id)
	}
	if baseRev != 0 && it.Rev != baseRev {
		return 0, &Conflict{Rev: it.Rev}
	}
	doc, err := ParseDoc(it.Content)
	if err != nil {
		return 0, err
	}
	status := "approved"
	if !approve {
		status = "ready"
	}
	_ = doc.Set("status", status)
	title := doc.Str("title")
	if title == "" {
		title = id
	}
	return s.write(ctx, name, doc.String(), title)
}

// Archiver keeps the signed-off script: a pinned snapshot in Caster and a knowledge item in
// Tesseract (through the gateway).
type Archiver interface {
	Snap(ctx context.Context, ref string) (pinned string, err error)
	Keep(ctx context.Context, namespace, key, title, summary, body, pointer string) error
}

// Signed is what sign-off did.
type Signed struct {
	Approved  map[string]int `json:"approved"`
	Pinned    string         `json:"pinned,omitempty"`
	Kept      string         `json:"kept,omitempty"` // the Tesseract namespace#key
	Mailed    []string       `json:"mailed"`
	NotMailed []string       `json:"not_mailed"`
	Problem   string         `json:"problem,omitempty"`
}

// ErrNotAllApproved: sign-off without "approve all" while scenes are not approved.
type ErrNotAllApproved struct{ Scenes []string }

func (e *ErrNotAllApproved) Error() string {
	return "not every scene is approved yet: " + strings.Join(e.Scenes, ", ")
}

// SignOff approves the script: every listed scene's current revision is pinned in the
// manifest (approved), the stage set to approved, the manifest snapped into CAS, the
// assembled script kept in Tesseract (project/<project>/knowledge/scripts), and the owners
// told. Without all, every scene must already be approved; with it, the rest are approved
// first.
func (s *Store) SignOff(ctx context.Context, key string, all bool, keep Archiver, tell Notifier) (Signed, error) {
	if s.caster == nil {
		return Signed{}, ErrNoCaster
	}
	out := Signed{Approved: map[string]int{}, Mailed: []string{}, NotMailed: []string{}}
	it, ok, err := s.fresh(ctx, ManifestName(key))
	if err != nil {
		return out, err
	}
	if !ok {
		return out, ErrNotFound
	}
	md, err := ParseDoc(it.Content)
	if err != nil {
		return out, err
	}
	ids := md.Strings("scenes")
	if len(ids) == 0 {
		return out, errors.New("the script has no scenes to sign off")
	}
	type sceneNow struct {
		id    string
		rev   int
		doc   *Doc
		title string
	}
	scenes := []sceneNow{}
	var pending []string
	for _, id := range ids {
		sit, ok, err := s.fresh(ctx, SceneName(key, id))
		if err != nil {
			return out, err
		}
		if !ok {
			return out, fmt.Errorf("scene %s is listed but not in Caster", id)
		}
		d, err := ParseDoc(sit.Content)
		if err != nil {
			return out, fmt.Errorf("scene %s cannot be read: %w", id, err)
		}
		if d.Str("status") != "approved" {
			pending = append(pending, id)
		}
		scenes = append(scenes, sceneNow{id: id, rev: sit.Rev, doc: d, title: d.Str("title")})
	}
	if len(pending) > 0 && !all {
		return out, &ErrNotAllApproved{Scenes: pending}
	}
	for i, sc := range scenes {
		if sc.doc.Str("status") == "approved" {
			continue
		}
		rev, err := s.ApproveScene(ctx, key, sc.id, sc.rev, true)
		if err != nil {
			return out, fmt.Errorf("scene %s: %w", sc.id, err)
		}
		scenes[i].rev = rev
	}
	for _, sc := range scenes {
		out.Approved[sc.id] = sc.rev
	}
	if err := s.changeManifest(ctx, key, func(d *Doc) error {
		_ = d.Set("approved", out.Approved)
		return d.Set("stage", "approved")
	}); err != nil {
		return out, err
	}

	// The kept record: the manifest pinned, and the script assembled.
	project, script := ParseName(key).Project, ParseName(key).Script
	title := md.Str("title")
	if title == "" {
		title = key
	}
	if keep != nil {
		if pinned, err := keep.Snap(ctx, Ref(ManifestName(key))); err == nil {
			out.Pinned = pinned
		} else {
			out.Problem = "The manifest was not snapped: " + err.Error()
		}
		// Scenes as they were approved: after approving the rest, their latest is it.
		parts := []string{}
		for _, sc := range scenes {
			it, ok, err := s.caster.Get(ctx, SceneName(key, sc.id), sc.rev)
			if err != nil || !ok {
				continue
			}
			d, err := ParseDoc(it.Content)
			if err != nil {
				continue
			}
			parts = append(parts, assembledScene(len(parts)+1, sc.id, d, RefAt(SceneName(key, sc.id), sc.rev)))
		}
		body := fmt.Sprintf("# %s\n\nSigned off %s. Manifest: %s%s\n\n%s\n%s", title, time.Now().UTC().Format("2006-01-02 15:04 MST"), Ref(ManifestName(key)),
			pinnedNote(out.Pinned), strings.TrimSpace(md.Body), strings.Join(parts, ""))
		ns := "project/" + project + "/knowledge/scripts"
		if err := keep.Keep(ctx, ns, script, title, fmt.Sprintf("Signed-off script \"%s\" (%d scenes), from Basalisk Story.", title, len(scenes)), body, out.Pinned); err != nil {
			if out.Problem != "" {
				out.Problem += " "
			}
			out.Problem += "The script was not kept in Tesseract: " + err.Error()
		} else {
			out.Kept = ns + "#" + script
		}
	}
	for _, owner := range md.Strings("owners") {
		if tell == nil || !strings.HasPrefix(owner, "msg://") {
			out.NotMailed = append(out.NotMailed, owner)
			continue
		}
		msg := fmt.Sprintf("\"%s\" is signed off: every scene approved at its pinned revision (%s).", title, Ref(ManifestName(key)))
		if out.Kept != "" {
			msg += " The assembled script is in Tesseract at " + out.Kept + "."
		}
		if err := tell.Mail(ctx, owner, "Story: signed off: "+title, msg, Ref(ManifestName(key))); err != nil {
			out.NotMailed = append(out.NotMailed, owner)
			continue
		}
		out.Mailed = append(out.Mailed, owner)
	}
	return out, nil
}

func pinnedNote(p string) string {
	if p == "" {
		return ""
	}
	return " (pinned " + p + ")"
}

// assembledScene is a scene in the kept script: its sections, its timing, its pinned ref.
func assembledScene(n int, id string, d *Doc, ref string) string {
	b := ParseSceneBody(d.Body)
	var w strings.Builder
	fmt.Fprintf(&w, "\n## %d. %s (%s, %gs)\n\n_%s_\n", n, d.Str("title"), id, d.Num("duration_s"), ref)
	for _, sec := range Sections {
		if t := strings.TrimSpace(b.Get(sec)); t != "" {
			fmt.Fprintf(&w, "\n**%s.** %s\n", sec, t)
		}
	}
	return w.String()
}

// GatewayArchiver keeps the signed-off script through the Tether gateway.
type GatewayArchiver struct{ Link Caller }

func (g GatewayArchiver) Snap(ctx context.Context, ref string) (string, error) {
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
	if strings.HasPrefix(out.Ref, "cas://") {
		return out.Ref, nil
	}
	if out.CAS != "" {
		return out.CAS, nil
	}
	return out.Ref, nil
}

func (g GatewayArchiver) Keep(ctx context.Context, namespace, key, title, summary, body, pointer string) error {
	scheme, locator := "nil", ""
	if pointer != "" {
		scheme, locator = "caster", pointer
	}
	tags, _ := json.Marshal([]string{"story", "script", "signed-off"})
	_, err := g.Link.Call(ctx, "knowledge_write", map[string]any{
		"namespace": namespace, "key": key, "kind": "doc", "source": "manual",
		"pointer_scheme": scheme, "pointer_locator": locator,
		"summary": summary, "body": body, "tags": string(tags),
		"author_agent_id": "basalisk-story", "session_id": "basalisk-story", "status": "reviewed",
	})
	return err
}
