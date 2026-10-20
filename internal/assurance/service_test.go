package assurance

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/security/webauthn/presencetest"
	_ "modernc.org/sqlite"
)

type testIdentity struct {
	mu       sync.Mutex
	sessions map[string]Session
}

func (i *testIdentity) LookupSession(_ context.Context, h string) (Session, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	u, ok := i.sessions[h]
	if !ok {
		return u, ErrDenied
	}
	return u, nil
}

type testItems struct {
	mu   sync.Mutex
	item Item
	fail bool
}

func (i *testItems) LookupItem(context.Context, string) (Item, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.fail {
		return Item{}, ErrUnavailable
	}
	return i.item, nil
}

type testEnrollment struct{ allow bool }

func (p testEnrollment) AuthorizeEnrollment(context.Context, Session, string) error {
	if !p.allow {
		return ErrDenied
	}
	return nil
}

type fixture struct {
	t        *testing.T
	db       *sql.DB
	path     string
	service  *Service
	identity *testIdentity
	items    *testItems
	auth     *presencetest.Authenticator
	key      string
	now      time.Time
}

func fixtureDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	if _, e = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL;`); e != nil {
		t.Fatal(e)
	}
	return db
}
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, path: filepath.Join(t.TempDir(), "test.db"), now: time.Unix(1900000000, 0).UTC()}
	f.identity = &testIdentity{sessions: map[string]Session{"session-a": {"human-a", "session-a", f.now.Add(time.Hour)}, "session-b": {"human-a", "session-b", f.now.Add(time.Hour)}, "session-other": {"human-other", "session-other", f.now.Add(time.Hour)}}}
	op := Operation{Profile: OperationProfile, OperationID: "operation-a", Revision: "1", Requester: Requester{"tether", "agent-a"}, Audience: "tether", Class: "high", ExpiresAt: strconv.FormatInt(f.now.Add(10*time.Minute).Unix(), 10), AuthorizationDetails: []Registration{{Type: "credential_registration", Action: "create", PrincipalID: "producer-a", OwnerID: "human-a", Purpose: "approval producer", CredentialExpiresAt: strconv.FormatInt(f.now.Add(24*time.Hour).Unix(), 10), OverlapSeconds: "0", DestinationID: "destination-a", DestinationRevision: "1", Scopes: []string{"tachyon.approvals_read"}}}}
	raw, _ := json.Marshal(op)
	_, d, e := ParseOperation(raw)
	if e != nil {
		t.Fatal(e)
	}
	f.items = &testItems{item: Item{"item-a", 1, "pending", f.now.Add(10 * time.Minute), raw, d}}
	f.open()
	f.auth = presencetest.New(t, "identity.example.test", "https://identity.example.test")
	id, options, e := f.service.BeginEnrollment(context.Background(), "session-a", f.auth.Origin, testEnrollment{true})
	if e != nil {
		t.Fatal(e)
	}
	f.key, e = f.service.FinishEnrollment(context.Background(), "session-a", id, f.auth.Register(t, options), testEnrollment{true})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = f.db.Close() })
	return f
}
func (f *fixture) open() {
	f.db = fixtureDB(f.t, f.path)
	var e error
	f.service, e = New(context.Background(), f.db, Config{"basalisk-test", "identity.example.test", []string{"https://identity.example.test"}, []string{"tether"}}, f.items, f.identity, func() time.Time { return f.now })
	if e != nil {
		f.t.Fatal(e)
	}
}
func (f *fixture) prepare(id string) Prepared {
	f.t.Helper()
	p, e := f.service.Prepare(context.Background(), "session-a", f.auth.Origin, PrepareCommand{id, "item-a", 1, f.key})
	if e != nil {
		f.t.Fatal(e)
	}
	return p
}
func (f *fixture) complete(id string) (Completion, json.RawMessage) {
	f.t.Helper()
	p := f.prepare(id)
	raw := f.auth.Assert(f.t, p.Options)
	c, e := f.service.Finish(context.Background(), "session-a", f.auth.Origin, id, raw)
	if e != nil {
		f.t.Fatal(e)
	}
	return c, raw
}
func TestCompletionRetryRestartAndArchive(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := f.prepare("attempt-a")
	again := f.prepare("attempt-a")
	x, _ := json.Marshal(p)
	y, _ := json.Marshal(again)
	if !bytes.Equal(x, y) {
		t.Fatal("prepare response changed")
	}
	raw := f.auth.Assert(t, p.Options)
	want, e := f.service.Finish(ctx, "session-a", f.auth.Origin, p.CompletionID, raw)
	if e != nil {
		t.Fatal(e)
	}
	// Simulate lost completion response, restart, and exact retry.
	if e = f.db.Close(); e != nil {
		t.Fatal(e)
	}
	f.open()
	got, e := f.service.Finish(ctx, "session-a", f.auth.Origin, p.CompletionID, raw)
	if e != nil || got.AuthTime != want.AuthTime || got.Evidence != want.Evidence {
		t.Fatalf("retry changed completion: %v", e)
	}
	st, e := f.service.store.Load(ctx, "human-a")
	if e != nil || st.User.Credentials[0].Authenticator.SignCount != 1 {
		t.Fatal("retry advanced counter", e)
	}
	if _, e = f.service.Finish(ctx, "session-b", f.auth.Origin, p.CompletionID, raw); e == nil {
		t.Fatal("different session recovered completion")
	}
	if _, e = f.service.Finish(ctx, "session-a", f.auth.Origin, p.CompletionID, f.auth.Assert(t, p.Options)); e == nil {
		t.Fatal("changed retry accepted")
	}
	// The next shared begin prunes expired receipts. The wrapper archives the
	// original committed receipt first; it survives restart without becoming live.
	f.now = f.now.Add(5 * time.Minute)
	f.prepare("attempt-b")
	a, e := f.service.loadAttempt(ctx, p.CompletionID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.service.store.receipt(ctx, "human-a", a.CeremonyID); e != nil {
		t.Fatal("lost historical receipt", e)
	}
	if _, e = f.service.LookupCompletion(ctx, p.CompletionID); e == nil {
		t.Fatal("expired completion became eligible")
	}
}
func TestConcurrentCompletionOnce(t *testing.T) {
	f := newFixture(t)
	p := f.prepare("attempt-a")
	raw := f.auth.Assert(t, p.Options)
	var wg sync.WaitGroup
	results := make(chan Completion, 12)
	failures := make(chan error, 12)
	for range 12 {
		wg.Go(func() {
			c, e := f.service.Finish(context.Background(), "session-a", f.auth.Origin, p.CompletionID, raw)
			if e != nil {
				failures <- e
			} else {
				results <- c
			}
		})
	}
	wg.Wait()
	close(results)
	close(failures)
	var first Completion
	success := 0
	for c := range results {
		if success > 0 && (c.Evidence != first.Evidence || c.AuthTime != first.AuthTime) {
			t.Fatal("different receipts")
		}
		first = c
		success++
	}
	if success == 0 {
		t.Fatal("no successful completion")
	}
	st, e := f.service.store.Load(context.Background(), "human-a")
	if e != nil || st.User.Credentials[0].Authenticator.SignCount != 1 {
		t.Fatal("counter advanced twice", e)
	}
}
func TestCurrentEligibilityAndBinding(t *testing.T) {
	for _, name := range []string{"logout", "key-revoked", "ceremony-revoked", "expiry", "wrong-human", "wrong-key", "wrong-origin", "item-outage", "item-revision", "item-operation", "missing-uv", "wrong-rp", "counter-regression"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			p := f.prepare("attempt-a")
			if name == "logout" || name == "key-revoked" || name == "ceremony-revoked" || name == "expiry" {
				f.complete("attempt-a")
				switch name {
				case "logout":
					delete(f.identity.sessions, "session-a")
				case "key-revoked":
					if e := f.service.RevokeKey(ctx, "human-a", f.key); e != nil {
						t.Fatal(e)
					}
				case "ceremony-revoked":
					if e := f.service.RevokeCompletion(ctx, p.CompletionID); e != nil {
						t.Fatal(e)
					}
				case "expiry":
					f.now = f.now.Add(5 * time.Minute)
				}
				if _, e := f.service.LookupCompletion(ctx, p.CompletionID); e == nil {
					t.Fatal("ineligible receipt accepted")
				}
				return
			}
			handle, origin := "session-a", f.auth.Origin
			switch name {
			case "wrong-human":
				handle = "session-other"
			case "wrong-key":
				f.auth = presencetest.New(t, f.auth.RPID, f.auth.Origin)
			case "wrong-origin":
				origin = "https://other.example.test"
			case "item-outage":
				f.items.fail = true
			case "item-revision":
				f.items.item.Revision++
			case "item-operation":
				f.items.item.Operation = bytes.Replace(f.items.item.Operation, []byte("destination-a"), []byte("destination-b"), 1)
			case "missing-uv":
				f.auth.UV = false
			case "wrong-rp":
				f.auth.RPID = "other.example.test"
			case "counter-regression":
				st, _ := f.service.store.Load(ctx, "human-a")
				st.User.Credentials[0].Authenticator.SignCount = 7
				if e := f.service.store.CompareAndSwap(ctx, "human-a", st.Version, st); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := f.service.Finish(ctx, handle, origin, p.CompletionID, f.auth.Assert(t, p.Options)); e == nil {
				t.Fatal("invalid completion accepted")
			}
		})
	}
}
func TestArchiveFailureCannotPruneReceipt(t *testing.T) {
	f := newFixture(t)
	c, _ := f.complete("attempt-a")
	ctx := context.Background()
	a, _ := f.service.loadAttempt(ctx, c.ID)
	if _, e := f.db.Exec(`CREATE TRIGGER reject_archive BEFORE INSERT ON assurance_receipts BEGIN SELECT RAISE(ABORT,'simulated disk error'); END`); e != nil {
		t.Fatal(e)
	}
	f.now = f.now.Add(5 * time.Minute)
	_, e := f.service.Prepare(ctx, "session-a", f.auth.Origin, PrepareCommand{"attempt-b", "item-a", 1, f.key})
	if e == nil {
		t.Fatal("archive failure ignored")
	}
	st, e := f.service.store.Load(ctx, "human-a")
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := st.Proofs[a.CeremonyID]; !ok {
		t.Fatal("receipt pruned before archive")
	}
}
func TestFailedCounterCommitReturnsNoProof(t *testing.T) {
	f := newFixture(t)
	p := f.prepare("attempt-a")
	if _, e := f.db.Exec(`CREATE TRIGGER reject_state BEFORE UPDATE ON webauthn_subjects BEGIN SELECT RAISE(ABORT,'simulated write error'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e := f.service.Finish(context.Background(), "session-a", f.auth.Origin, p.CompletionID, f.auth.Assert(t, p.Options)); e == nil {
		t.Fatal("uncommitted proof returned")
	}
	if _, e := f.service.LookupCompletion(context.Background(), p.CompletionID); !errors.Is(e, ErrDenied) {
		t.Fatal("uncommitted evidence visible", e)
	}
}
func TestEnrollmentRequiresSeparateAuthority(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, e := f.service.BeginEnrollment(ctx, "session-a", f.auth.Origin, nil); e == nil {
		t.Fatal("password session enrolled key")
	}
	id, options, e := f.service.BeginEnrollment(ctx, "session-a", f.auth.Origin, testEnrollment{true})
	if e != nil {
		t.Fatal(e)
	}
	a := presencetest.New(t, f.auth.RPID, f.auth.Origin)
	if _, e = f.service.FinishEnrollment(ctx, "session-a", id, a.Register(t, options), testEnrollment{false}); e == nil {
		t.Fatal("revoked enrollment authority accepted")
	}
}

func TestCancellationRevokesOnlyOwnedAttempt(t *testing.T) {
	f := newFixture(t)
	c, _ := f.complete("attempt-a")
	ctx := context.Background()
	if e := f.service.Cancel(ctx, "session-b", c.ID); e == nil {
		t.Fatal("another session canceled completion")
	}
	if _, e := f.service.LookupCompletion(ctx, c.ID); e != nil {
		t.Fatal("rejected cancellation changed completion", e)
	}
	if e := f.service.Cancel(ctx, "session-a", c.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := f.service.LookupCompletion(ctx, c.ID); e == nil {
		t.Fatal("canceled completion accepted")
	}
	if f.items.item.Status != "pending" {
		t.Fatal("Basalisk cancellation resolved inbox item")
	}
}
