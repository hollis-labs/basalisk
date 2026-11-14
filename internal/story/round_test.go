package story

import (
	"context"
	"strings"
	"testing"
)

type fakeTell struct {
	mails    []string // to|subject
	bodies   []string
	comments []string
}

func (f *fakeTell) Mail(_ context.Context, to, subject, body, ref string) error {
	f.mails = append(f.mails, to+"|"+subject)
	f.bodies = append(f.bodies, body)
	return nil
}
func (f *fakeTell) Comment(_ context.Context, task, text string) error {
	f.comments = append(f.comments, task+"|"+text)
	return nil
}

func TestSendRound(t *testing.T) {
	s, f := seeded(t)
	ctx := context.Background()
	key := "qspace.launch-teaser"
	// A task and a second owner that is not a Tether address.
	f.team(key, strings.Replace(strings.Replace(manifest, "approved: {}", "approved: {}\ntask: CW-20261114-0101", 1), `owners: ["msg://agent/agent-mux/agt_ndfltf559l"]`, `owners: ["msg://agent/agent-mux/agt_ndfltf559l", "@editor"]`, 1))
	if _, err := s.Refresh(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SendRound(ctx, key, RoundFrom, &fakeTell{}); err == nil {
		t.Fatal("an empty round was sent")
	}
	if _, err := s.AddNote(ctx, Note{Key: key, Scene: "s02", Kind: "comment", Section: "Narration", Text: "Too long."}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddNote(ctx, Note{Key: key, Scene: "s01", Kind: "suggestion", Section: "Narration", Quote: "starts somewhere", Replace: "begins here", Text: "Punchier."}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddNote(ctx, Note{Key: key, Kind: "suggestion", Text: "no quote"}); err == nil {
		t.Fatal("a suggestion without a passage was taken")
	}
	if err := s.SetOrder(ctx, key, []string{"s02", "s01", "s03"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOrder(ctx, key, []string{"s03", "s02", "s01"}); err != nil {
		t.Fatal(err)
	}
	drafts, _ := s.Drafts(ctx, key)
	moves := 0
	for _, d := range drafts {
		if d.Kind == "move" {
			moves++
			if d.Text != "New order: s03, s02, s01" {
				t.Fatal(d.Text)
			}
		}
	}
	if len(drafts) != 3 || moves != 1 {
		t.Fatalf("%d drafts, %d moves", len(drafts), moves)
	}

	tell := &fakeTell{}
	sent, err := s.SendRound(ctx, key, RoundFrom, tell)
	if err != nil {
		t.Fatal(err)
	}
	if sent.Round != 2 || sent.Notes != 3 || sent.Ref != "caster://story/"+key+".round-2" || len(sent.Mailed) != 1 || len(sent.NotMailed) != 1 || sent.NotMailed[0] != "@editor" || sent.Commented != "CW-20261114-0101" {
		t.Fatalf("%+v", sent) // the manifest says round: 1 already, so this is round 2
	}
	it, _, _ := f.Get(ctx, key+".round-2", 0)
	d, err := ParseDoc(it.Content)
	if err != nil {
		t.Fatal(err)
	}
	var notes []RoundNote
	_ = d.Decode("notes", &notes)
	revs := map[string]int{}
	_ = d.Decode("revs", &revs)
	if d.Num("round") != 2 || d.Str("from") != RoundFrom || len(notes) != 3 || notes[0].State != "open" || notes[1].Quote != "starts somewhere" || notes[1].Replace != "begins here" || notes[0].SceneRev != 1 || revs["s01"] != 1 || len(revs) != 3 || !strings.Contains(d.Body, "begins here") {
		t.Fatalf("%s", it.Content)
	}
	m, _, _ := f.Get(ctx, key, 0)
	md, _ := ParseDoc(m.Content)
	if md.Str("stage") != "revising" || md.Num("round") != 2 || md.Str("custom_field") != "keep me" {
		t.Fatalf("%s", m.Content)
	}
	if left, _ := s.Drafts(ctx, key); len(left) != 0 {
		t.Fatal("the draft was not emptied")
	}
	if !strings.Contains(tell.mails[0], "msg://agent/agent-mux/agt_ndfltf559l|Story: round 2 on QSpace launch teaser (3 notes on 2 scenes)") || !strings.Contains(tell.bodies[0], sent.Ref) || len(tell.comments) != 1 {
		t.Fatalf("%v %v", tell.mails, tell.comments)
	}
	rounds, _ := s.Rounds(ctx, key)
	if len(rounds) != 1 || rounds[0].Round != 2 || len(rounds[0].Notes) != 3 {
		t.Fatalf("%+v", rounds)
	}

	// A round already in Caster (sent from elsewhere) is never written over.
	f.team(key+".round-3", "---\nstory: v1\nround: 3\nnotes: []\n---\n")
	_, _ = s.AddNote(ctx, Note{Key: key, Kind: "comment", Text: "Overall good."})
	sent, err = s.SendRound(ctx, key, RoundFrom, nil)
	if err != nil || sent.Round != 4 || len(sent.NotMailed) != 2 {
		t.Fatalf("%+v %v", sent, err)
	}
}

func TestStructureNotesCancelOut(t *testing.T) {
	s, _ := seeded(t)
	ctx := context.Background()
	key := "qspace.launch-teaser"
	id, err := s.AddScene(ctx, key, "", "", "Beat")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CutScene(ctx, key, id); err != nil {
		t.Fatal(err)
	}
	drafts, _ := s.Drafts(ctx, key)
	if len(drafts) != 0 {
		t.Fatalf("an added-then-cut scene left %+v", drafts)
	}
	if err := s.CutScene(ctx, key, "s03"); err != nil {
		t.Fatal(err)
	}
	drafts, _ = s.Drafts(ctx, key)
	if len(drafts) != 1 || drafts[0].Kind != "cut" || drafts[0].Scene != "s03" {
		t.Fatalf("%+v", drafts)
	}
	if err := s.RestoreScene(ctx, key, "s03"); err != nil {
		t.Fatal(err)
	}
	if drafts, _ = s.Drafts(ctx, key); len(drafts) != 0 {
		t.Fatalf("restoring did not cancel the cut: %+v", drafts)
	}
}
