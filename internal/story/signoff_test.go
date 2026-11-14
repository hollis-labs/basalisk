package story

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeKeep struct {
	snapped        []string
	ns, key, body  string
	pointer, title string
}

func (f *fakeKeep) Snap(_ context.Context, ref string) (string, error) {
	f.snapped = append(f.snapped, ref)
	return "cas://sha256/feed", nil
}
func (f *fakeKeep) Keep(_ context.Context, ns, key, title, summary, body, pointer string) error {
	f.ns, f.key, f.title, f.body, f.pointer = ns, key, title, body, pointer
	return nil
}

func TestChangedSinceTheRound(t *testing.T) {
	s, f := seeded(t)
	ctx := context.Background()
	key := "qspace.launch-teaser"
	_, _ = s.AddNote(ctx, Note{Key: key, Scene: "s02", Kind: "comment", Text: "Shorter."})
	if _, err := s.SendRound(ctx, key, RoundFrom, nil); err != nil {
		t.Fatal(err)
	}
	f.team(key+".s02", scene("s02", "The claim", "Shorter now."))
	if _, err := s.Refresh(ctx, true); err != nil {
		t.Fatal(err)
	}
	sc, _, _ := s.Script(ctx, key)
	since := s.SinceOf(ctx, sc)
	if since.Round != 2 || strings.Join(since.Changed, ",") != "s02" || since.RoundRevs["s02"] != 1 || since.SignedOff {
		t.Fatalf("%+v", since)
	}
}

func TestApproveAndSignOff(t *testing.T) {
	s, f := seeded(t)
	ctx := context.Background()
	key := "qspace.launch-teaser"
	// Approving what one saw: a stale revision is refused.
	f.team(key+".s01", scene("s01", "Open", "Changed by the team."))
	var c *Conflict
	if _, err := s.ApproveScene(ctx, key, "s01", 1, true); !errors.As(err, &c) || c.Rev != 2 {
		t.Fatal(err)
	}
	if rev, err := s.ApproveScene(ctx, key, "s01", 2, true); err != nil || rev != 3 {
		t.Fatal(rev, err)
	}
	keep := &fakeKeep{}
	tell := &fakeTell{}
	var na *ErrNotAllApproved
	if _, err := s.SignOff(ctx, key, false, keep, tell); !errors.As(err, &na) || strings.Join(na.Scenes, ",") != "s02,s03" {
		t.Fatal(err)
	}
	signed, err := s.SignOff(ctx, key, true, keep, tell)
	if err != nil {
		t.Fatal(err)
	}
	if signed.Approved["s01"] != 3 || signed.Approved["s02"] != 2 || signed.Approved["s03"] != 2 || signed.Pinned != "cas://sha256/feed" || signed.Kept != "project/qspace/knowledge/scripts#launch-teaser" || len(signed.Mailed) != 1 {
		t.Fatalf("%+v", signed)
	}
	m, _, _ := f.Get(ctx, key, 0)
	md, _ := ParseDoc(m.Content)
	approved := map[string]int{}
	_ = md.Decode("approved", &approved)
	if md.Str("stage") != "approved" || approved["s02"] != 2 || md.Str("custom_field") != "keep me" {
		t.Fatalf("%s", m.Content)
	}
	for _, id := range []string{"s02", "s03"} {
		it, _, _ := f.Get(ctx, key+"."+id, 0)
		if d, _ := ParseDoc(it.Content); d.Str("status") != "approved" {
			t.Fatalf("%s not approved", id)
		}
	}
	if keep.ns != "project/qspace/knowledge/scripts" || keep.key != "launch-teaser" || keep.pointer != "cas://sha256/feed" || !strings.Contains(keep.body, "Changed by the team.") || !strings.Contains(keep.body, "caster://story/qspace.launch-teaser.s02@2") || !strings.Contains(keep.body, "Logline: a short teaser.") {
		t.Fatalf("%+v\n%s", keep, keep.body)
	}
	// A later edit shows the script as changed since sign-off.
	f.team(key+".s03", scene("s03", "Close", "A new ending."))
	_, _ = s.Refresh(ctx, true)
	sc, _, _ := s.Script(ctx, key)
	since := s.SinceOf(ctx, sc)
	if !since.SignedOff || !since.StaleSign || strings.Join(since.SinceSign, ",") != "s03" {
		t.Fatalf("%+v", since)
	}
}
