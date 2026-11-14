package assist

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/basalisk/internal/inbox"
)

type box struct {
	items []inbox.Item
	read  map[string]bool
}

const me = "msg://user/local/operator"

func (b *box) ID() string         { return "tether" }
func (b *box) Title() string      { return "Tether" }
func (b *box) Accounts() []string { return []string{me} }
func (b *box) List(context.Context, inbox.Filter) ([]inbox.Item, error) {
	var out []inbox.Item
	for _, it := range b.items {
		if !b.read[it.ID] {
			out = append(out, it)
		}
	}
	return out, nil
}
func (b *box) Get(context.Context, string, string) (inbox.Item, error)  { return inbox.Item{}, nil }
func (b *box) Unread(context.Context) (int, error)                      { return 0, nil }
func (b *box) MarkRead(_ context.Context, _, id string) error           { b.read[id] = true; return nil }
func (b *box) Archive(context.Context, string, string, bool) error      { return nil }
func (b *box) Reply(context.Context, string, string, inbox.Reply) error { return nil }
func (b *box) Send(context.Context, string, inbox.Outgoing) (string, error) {
	return "", nil
}
func (b *box) Recipients(context.Context, string) ([]inbox.Recipient, error) { return nil, nil }

func msg(id, kind, from, subject string, ask bool) inbox.Item {
	it := inbox.Item{ID: id, Provider: "tether", Account: me, From: from, Kind: kind, Subject: subject, Body: "text", At: time.Now()}
	if ask {
		it.Ask = &inbox.Ask{Kind: "decision", Options: []inbox.Option{{ID: "a", Label: "A"}}}
	}
	return it
}

func TestWatcher(t *testing.T) {
	ctx := context.Background()
	log, err := Open(ctx, filepath.Join(t.TempDir(), "assist.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = log.Close() }()
	b := &box{read: map[string]bool{}, items: []inbox.Item{msg("old1", "notice", "msg://a/x/1", "Old news", false)}}
	var handed []Mail
	w := &Watcher{Mail: inbox.New(b), Log: log, Hand: func(_ context.Context, batch []Mail) { handed = append(handed, batch...) }}
	if err := w.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := w.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("what was waiting at the first sweep is left alone: %d %v", n, err)
	}
	noisy := "msg://a/x/noisy"
	if _, err := w.AddRule(ctx, Rule{From: noisy, Class: ClassSettle}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddRule(ctx, Rule{Class: ClassSettle}); err == nil {
		t.Fatal("a rule that matches everything is refused")
	}
	b.items = append(b.items,
		msg("q1", "request", "msg://a/x/2", "Pick one", true),
		msg("r1", "request", "msg://a/x/2", "Please look at this", false),
		msg("n1", "notice", "msg://a/x/3", "Deploy finished", false),
		msg("z1", "notice", noisy, "Heartbeat", false),
		msg("e1", "escalation", "msg://a/x/3", "Stuck", false))
	n, err := w.Sweep(ctx)
	if err != nil || n != 4 {
		t.Fatalf("four new and open (the settled one is done): %d %v", n, err)
	}
	needs, _ := w.Open(ctx, ClassNeeds)
	digest, _ := w.Open(ctx, ClassDigest)
	if len(needs) != 2 || len(digest) != 1 || len(handed) != 1 || handed[0].ID != "r1" {
		t.Fatalf("needs %d digest %d handed %+v", len(needs), len(digest), handed)
	}
	if !b.read["z1"] {
		t.Fatal("the owner's settle rule marks it read")
	}
	if entries, _ := log.Recent(ctx, 0, 0); len(entries) != 1 || entries[0].Kind != "settle" {
		t.Fatalf("and it is on the record: %+v", entries)
	}
	b.read["q1"] = true // answered somewhere
	if _, err := w.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if needs, _ = w.Open(ctx, ClassNeeds); len(needs) != 1 || needs[0].ID != "e1" {
		t.Fatalf("mail that is no longer unread is closed: %+v", needs)
	}
}
