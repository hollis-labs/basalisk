package work

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

type tellLog struct {
	mails    []string // to|subject
	comments []string
	snaps    []string
}

func (l *tellLog) Mail(_ context.Context, to, subject, _, _ string) error {
	l.mails = append(l.mails, to+"|"+subject)
	return nil
}
func (l *tellLog) Comment(_ context.Context, task, text string) error {
	l.comments = append(l.comments, task+"|"+text)
	return nil
}
func (l *tellLog) Snap(_ context.Context, ref string) (string, error) {
	l.snaps = append(l.snaps, ref)
	return "cas://sha256/ab", nil
}

func TestRoundAndSignOff(t *testing.T) {
	ctx := context.Background()
	s, f := open(t)
	mkspace(t, s)
	id, err := s.MakeItem(ctx, "channel", NewItem{Kind: "article", Title: "Lab tour"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := editSummary(s, id, "A walk round the lab."); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOwners(ctx, "channel", []string{"msg://agent/x/writer", "msg://agent/x/writer", "  "}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOwners(ctx, "channel", []string{"bob@example.com"}); err == nil {
		t.Error("a non-Tether owner was taken")
	}

	// Notes: a suggestion must quote what is there.
	if _, err := s.AddNote(ctx, "channel", id, Note{Kind: "suggestion", Section: "Summary", Quote: "not there", Replace: "x"}); err == nil {
		t.Error("a suggestion on a passage that is not there was taken")
	}
	if _, err := s.AddNote(ctx, "channel", id, Note{Kind: "suggestion", Section: "Summary", Quote: "walk round", Replace: "tour of"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddNote(ctx, "channel", id, Note{Kind: "comment", Text: "Lovely."}); err != nil {
		t.Fatal(err)
	}
	log := &tellLog{}
	if _, err := s.SendRound(ctx, "channel", "nosuch01", log); err == nil {
		t.Error("a round with no notes was sent")
	}
	sent, err := s.SendRound(ctx, "channel", id, log)
	if err != nil {
		t.Fatal(err)
	}
	if sent.Round != 1 || sent.Notes != 2 || len(sent.Mailed) != 1 || len(log.mails) != 1 || !strings.Contains(log.mails[0], "round 1 on Lab tour") {
		t.Fatalf("sent = %+v, mails %v", sent, log.mails)
	}
	r1 := f.latest("w-channel", RoundName(id, 1))
	for _, want := range []string{"work: v1", "item: " + id, "round: 1", "state: open", "from: msg://user/local/operator"} {
		if !strings.Contains(r1, want) {
			t.Errorf("round lacks %q:\n%s", want, r1)
		}
	}
	d, _, _ := s.Detail(ctx, "channel", id)
	if len(d.Notes) != 0 || len(d.Rounds) != 1 || d.Rounds[0].Open != 2 || d.Changed {
		t.Fatalf("after send: notes %d rounds %+v changed %v", len(d.Notes), d.Rounds, d.Changed)
	}

	// Sign-off is refused while notes are open, unless anyway.
	var open *ErrOpenNotes
	if _, err := s.SignOff(ctx, "channel", id, false, log, log); !errors.As(err, &open) || open.Open != 2 {
		t.Fatalf("sign-off with open notes: %v", err)
	}
	// An unreadable round blocks sign-off too.
	_, _ = f.Put(ctx, "w-channel", RoundName(id, 1), strings.Replace(r1, "    state: open", "    state: open\n      bad: indent", 1), "")
	_, _ = s.Refresh(ctx, "w-channel", true)
	if _, err := s.SignOff(ctx, "channel", id, false, log, log); !errors.As(err, &open) || open.Problem == "" {
		t.Fatalf("sign-off over an unreadable round: %v", err)
	}
	// The team answers in the round and revises.
	answered := strings.ReplaceAll(r1, "state: open", "state: done")
	_, _ = f.Put(ctx, "w-channel", RoundName(id, 1), answered, "")
	if _, err := editSummary(s, id, "A tour of the lab."); err != nil {
		t.Fatal(err)
	}
	_, _ = s.Refresh(ctx, "w-channel", true)
	d, _, _ = s.Detail(ctx, "channel", id)
	if !d.Changed || d.Rounds[0].Open != 0 {
		t.Fatalf("after answer: changed %v open %d", d.Changed, d.Rounds[0].Open)
	}
	// Move it to review: sign-off moves it on to the next stage.
	if _, err := s.Move(ctx, "channel", id, "stage", "review"); err != nil {
		t.Fatal(err)
	}
	signed, err := s.SignOff(ctx, "channel", id, false, log, log)
	if err != nil {
		t.Fatal(err)
	}
	if signed.Stage != "scheduled" || signed.Pinned == "" || signed.Round != 1 || len(log.snaps) != 1 ||
		!strings.HasSuffix(log.snaps[0], "@"+strconv.Itoa(signed.Rev)) || len(log.mails) != 2 {
		t.Fatalf("signed = %+v snaps %v mails %v", signed, log.snaps, log.mails)
	}
	d, _, _ = s.Detail(ctx, "channel", id)
	if d.SignOff == nil || d.SignOff.Rev != signed.Rev || d.SignOff.Pinned == "" || !d.Signed || d.StaleSign || d.Stage != "scheduled" {
		t.Fatalf("detail after sign-off: %+v %+v", d.SignOff, d.Card)
	}
	// A stage change does not make it stale; a text change does.
	if _, err := s.Move(ctx, "channel", id, "stage", "published"); err != nil {
		t.Fatal(err)
	}
	if d, _, _ = s.Detail(ctx, "channel", id); d.StaleSign {
		t.Error("a stage change made the sign-off stale")
	}
	if _, err := editSummary(s, id, "Changed after."); err != nil {
		t.Fatal(err)
	}
	if d, _, _ = s.Detail(ctx, "channel", id); !d.StaleSign {
		t.Error("a text change did not make the sign-off stale")
	}
	if err := s.Unsign(ctx, "channel", id); err != nil {
		t.Fatal(err)
	}
	if d, _, _ = s.Detail(ctx, "channel", id); d.Signed || d.SignOff != nil {
		t.Error("unsign left it signed")
	}
	// The next round is 2.
	if _, err := s.AddNote(ctx, "channel", id, Note{Kind: "comment", Text: "Again."}); err != nil {
		t.Fatal(err)
	}
	if sent, err = s.SendRound(ctx, "channel", id, nil); err != nil || sent.Round != 2 || len(sent.NotMailed) != 1 {
		t.Fatalf("round 2: %+v %v", sent, err)
	}
}

func editSummary(s *Store, id, text string) (int, error) {
	ctx := context.Background()
	_, _ = s.Refresh(ctx, "w-channel", true)
	d, _, _ := s.Detail(ctx, "channel", id)
	return s.EditItem(ctx, "channel", id, Edit{BaseRev: d.Rev, Sections: map[string]string{"Summary": text}})
}
