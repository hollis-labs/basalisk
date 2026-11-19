package gatecrasher_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/basalisk/internal/gatecrasher"
	"github.com/hollis-labs/basalisk/internal/gatecrasher/gatecrashertest"
)

type rig struct {
	fake  *gatecrashertest.Fake
	in    *gatecrasher.Inbox
	dir   string
	logs  *bytes.Buffer
	stop  context.CancelFunc
	state string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	f := gatecrashertest.New()
	dir := t.TempDir()
	key := filepath.Join(dir, "owner.key")
	if err := os.WriteFile(key, []byte(gatecrashertest.Key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "gatecrasher.json")
	if err := os.WriteFile(cfgPath, []byte(`{"url":"`+f.URL+`/"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := gatecrasher.LoadConfig(cfgPath, key)
	if err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	r := &rig{fake: f, dir: dir, logs: logs, state: filepath.Join(dir, "state.json")}
	r.start(t, cfg)
	t.Cleanup(func() {
		r.stop()
		f.Close()
	})
	return r
}

func (r *rig) start(t *testing.T, cfg gatecrasher.Config) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	r.stop = func() {
		cancel()
		r.fake.CloseClientConnections()
		<-done
	}
	r.in = gatecrasher.New(cfg, r.state, slog.New(slog.NewTextHandler(r.logs, nil)))
	go func() {
		r.in.Run(ctx)
		close(done)
	}()
}

// waitFor polls until ok holds or 2s pass (the acceptance's bound for a card to show).
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("not within 2s: %s", what)
}

func cardStatus(in *gatecrasher.Inbox, id string) string {
	cards, _, _ := in.List()
	for _, c := range cards {
		if c.ID == id {
			return c.Status
		}
	}
	return ""
}

func TestConfig(t *testing.T) {
	dir := t.TempDir()
	if _, err := gatecrasher.LoadConfig(filepath.Join(dir, "none.json"), ""); !errors.Is(err, gatecrasher.ErrNotConfigured) {
		t.Fatalf("missing file: %v", err)
	}
	p := filepath.Join(dir, "g.json")
	for _, bad := range []string{`{"url":"ftp://x"}`, `{"url":""}`, `{"url":"https://x","extra":1}`} {
		_ = os.WriteFile(p, []byte(bad), 0o600)
		if _, err := gatecrasher.LoadConfig(p, ""); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	_ = os.WriteFile(p, []byte(`{"url":"https://gate.example/","key_file":"~/k"}`), 0o600)
	c, err := gatecrasher.LoadConfig(p, "/default")
	home, _ := os.UserHomeDir()
	if err != nil || c.URL != "https://gate.example" || c.KeyFile != filepath.Join(home, "k") {
		t.Fatalf("got %+v %v", c, err)
	}
}

func TestCardShowsAndIsDecided(t *testing.T) {
	r := newRig(t)
	r.fake.Add("apr_1", "approve")
	waitFor(t, "the pending card", func() bool { return cardStatus(r.in, "apr_1") == "pending" })
	card, err := r.in.Decide(context.Background(), "apr_1", gatecrasher.Decision{Outcome: "approved"})
	if err != nil || card.Status != "approved" {
		t.Fatalf("decide: %+v %v", card, err)
	}
	if d := r.fake.Decisions(); len(d) != 1 || d[0]["outcome"] != "approved" {
		t.Fatalf("decisions: %v", d)
	}
	// Deciding again: Gatecrasher says it is over; the card shows how it ended.
	_, err = r.in.Decide(context.Background(), "apr_1", gatecrasher.Decision{Outcome: "denied"})
	var de *gatecrasher.DecideError
	if !errors.As(err, &de) || de.Status != 409 || de.Code != "terminal" {
		t.Fatalf("second decide: %v", err)
	}
	if cardStatus(r.in, "apr_1") != "approved" {
		t.Fatal("the card does not show the decision")
	}
}

func TestDecidedElsewhereUpdatesTheCard(t *testing.T) {
	r := newRig(t)
	r.fake.Add("apr_1", "approve")
	waitFor(t, "pending", func() bool { return cardStatus(r.in, "apr_1") == "pending" })
	r.fake.Settle("apr_1", "withdrawn", "withdrawn")
	waitFor(t, "withdrawn", func() bool { return cardStatus(r.in, "apr_1") == "withdrawn" })
}

func TestWaitWakesOnChange(t *testing.T) {
	r := newRig(t)
	_, v, _ := r.in.List()
	done := make(chan struct{})
	go func() {
		r.in.Wait(context.Background(), v, 5*time.Second)
		close(done)
	}()
	r.fake.Add("apr_1", "approve")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the wait did not wake on a new card")
	}
}

func TestReconnectResumesWithoutDuplicates(t *testing.T) {
	r := newRig(t)
	r.fake.DropAfter(1)
	r.fake.Add("apr_1", "approve")
	waitFor(t, "first card", func() bool { return cardStatus(r.in, "apr_1") == "pending" })
	r.fake.Add("apr_2", "approve")
	// The first connection ended after one event; the reconnect resumes after it.
	deadline := 4 * time.Second
	end := time.Now().Add(deadline)
	for time.Now().Before(end) && cardStatus(r.in, "apr_2") != "pending" {
		time.Sleep(20 * time.Millisecond)
	}
	if cardStatus(r.in, "apr_2") != "pending" {
		t.Fatal("the second card did not come after the reconnect")
	}
	ids := r.fake.LastEventIDs()
	if len(ids) < 2 || ids[0] != "" || ids[1] != "1" {
		t.Fatalf("Last-Event-ID per connection: %q", ids)
	}
	cards, _, _ := r.in.List()
	if len(cards) != 2 {
		t.Fatalf("cards: %d, want 2 (no duplicates)", len(cards))
	}
}

func TestRestartKeepsCursorAndCards(t *testing.T) {
	r := newRig(t)
	r.fake.Add("apr_1", "approve")
	waitFor(t, "card", func() bool { return cardStatus(r.in, "apr_1") == "pending" })
	fi, err := os.Stat(r.state)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file: %v %v", fi, err)
	}
	r.stop()
	cfg, _ := gatecrasher.LoadConfig(filepath.Join(r.dir, "gatecrasher.json"), filepath.Join(r.dir, "owner.key"))
	before := len(r.fake.LastEventIDs())
	r.start(t, cfg)
	if cardStatus(r.in, "apr_1") != "pending" {
		t.Fatal("the card was not kept across a restart")
	}
	waitFor(t, "reconnect", func() bool { return len(r.fake.LastEventIDs()) > before })
	if got := r.fake.LastEventIDs()[before]; got != "1" {
		t.Fatalf("the restart did not resume from the cursor: %q", got)
	}
}

func TestSecretRequestCannotBeApprovedHere(t *testing.T) {
	r := newRig(t)
	r.fake.Add("apr_s", "request_secret")
	waitFor(t, "secret card", func() bool { return cardStatus(r.in, "apr_s") == "pending" })
	if _, err := r.in.Decide(context.Background(), "apr_s", gatecrasher.Decision{Outcome: "approved"}); err == nil {
		t.Fatal("a secret request was approved through decide")
	}
	if len(r.fake.Decisions()) != 0 {
		t.Fatal("the refused approval reached Gatecrasher")
	}
	// Declining goes through.
	if c, err := r.in.Decide(context.Background(), "apr_s", gatecrasher.Decision{Outcome: "denied"}); err != nil || c.Status != "denied" {
		t.Fatalf("decline: %+v %v", c, err)
	}
}

func TestOwnerKeyIsReadEachTimeAndNeverLogged(t *testing.T) {
	r := newRig(t)
	r.fake.Add("apr_1", "approve")
	waitFor(t, "card", func() bool { return cardStatus(r.in, "apr_1") == "pending" })
	if strings.Contains(r.logs.String(), gatecrashertest.Key) {
		t.Fatal("the owner credential reached the log")
	}
	raw, _ := os.ReadFile(r.state)
	if strings.Contains(string(raw), gatecrashertest.Key) {
		t.Fatal("the owner credential reached the state file")
	}
}
