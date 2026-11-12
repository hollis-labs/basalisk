package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/hollis-labs/basalisk/internal/activity"
)

func responseAs[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func command(c *client, in activity.Command, want int) activity.Session {
	c.t.Helper()
	w := c.call("POST", "/api/activity/commands", in, want)
	if want != 200 {
		return activity.Session{}
	}
	return responseAs[activity.Session](c.t, w)
}

func TestActivityLifecycleAndImmutableContext(t *testing.T) {
	_, c := newStore(t)
	for _, path := range []string{"/api/activity", "/api/activity/sessions", "/api/activity/sessions/unknown/events"} {
		c.call("GET", path, nil, 401)
	}
	start := activity.Command{RequestID: "start-request-0001", Action: "start", Title: "Working across projects", Project: "Basalisk"}
	command(c, start, 401)
	c.signIn()
	first := command(c, start, 200)
	if first.Context.Source != "user" || first.Context.Workstream != "" || first.UserID != ownerPrincipal.ID {
		t.Fatal("incorrect identity or attribution")
	}
	other := start
	other.RequestID = "start-request-0002"
	command(c, other, 409)
	change := activity.Command{RequestID: "context-request-01", Action: "context", SessionID: first.ID, Revision: first.Revision, Project: "Fragments", Workstream: "Capture"}
	second := command(c, change, 200)
	if second.Context.ID == first.Context.ID || second.Revision != 2 {
		t.Fatal("context was not versioned")
	}
	if replay := command(c, start, 200); !reflect.DeepEqual(replay, first) {
		t.Fatal("retry did not return original receipt")
	}
	start.Title = "Changed payload"
	command(c, start, 409)
	change.RequestID = "context-stale-001"
	command(c, change, 409)
	paused := command(c, activity.Command{RequestID: "pause-request-001", Action: "pause", SessionID: first.ID, Revision: 2}, 200)
	command(c, activity.Command{RequestID: "pause-again-00001", Action: "pause", SessionID: first.ID, Revision: paused.Revision}, 409)
	resumed := command(c, activity.Command{RequestID: "resume-request-01", Action: "resume", SessionID: first.ID, Revision: paused.Revision}, 200)
	ended := command(c, activity.Command{RequestID: "end-request-00001", Action: "end", SessionID: first.ID, Revision: resumed.Revision}, 200)
	if ended.Status != "ended" || ended.EndedAt == "" {
		t.Fatal("session not ended")
	}
	command(c, activity.Command{RequestID: "resume-ended-001", Action: "resume", SessionID: first.ID, Revision: ended.Revision}, 409)
	state := responseAs[activity.State](t, c.call("GET", "/api/activity", nil, 200))
	if state.Current != nil {
		t.Fatal("ended session remains current")
	}
	events := responseAs[activity.Page[activity.Event]](t, c.call("GET", "/api/activity/sessions/"+first.ID+"/events", nil, 200))
	if len(events.Items) != 5 {
		t.Fatalf("retries or conflicts changed history: %+v", events)
	}
	old := events.Items[len(events.Items)-1]
	if old.Kind != "started" || old.Context.Project != "Basalisk" || old.Context.ID != first.Context.ID || old.ActorID != state.Identity.UserID {
		t.Fatal("old context or actor changed")
	}
	if events.Items[0].Context.ID != second.Context.ID || events.Items[0].Source != "basalisk.web" {
		t.Fatal("later event lost context/source")
	}
	other.Title = "Next session"
	command(c, other, 200)
	c.call("GET", "/api/activity/sessions/missing/events", nil, 404)
	c.call("GET", "/api/activity/sessions?before=-1", nil, 400)
	command(c, activity.Command{RequestID: "invalid-request-01", Action: "unknown"}, 400)
	command(c, activity.Command{RequestID: "missing-session01", Action: "end", SessionID: "missing", Revision: 1}, 404)
	c.call("POST", "/api/activity/commands", map[string]any{"action": "start", "requestId": "forged-actor-0001", "title": "X", "actorId": "other"}, 400)
}

func TestActivityPaginationAndAtomicRollback(t *testing.T) {
	s, c := newStore(t)
	c.signIn()
	current := command(c, activity.Command{RequestID: "history-start-001", Action: "start", Title: "History"}, 200)
	for i := range 52 {
		current = command(c, activity.Command{RequestID: fmt.Sprintf("history-context-%03d", i), Action: "context", SessionID: current.ID, Revision: current.Revision, Project: fmt.Sprintf("Project %d", i)}, 200)
	}
	path := "/api/activity/sessions/" + current.ID + "/events"
	first := responseAs[activity.Page[activity.Event]](t, c.call("GET", path, nil, 200))
	second := responseAs[activity.Page[activity.Event]](t, c.call("GET", fmt.Sprintf("%s?before=%d", path, first.NextBefore), nil, 200))
	if len(first.Items) != 50 || len(second.Items) != 3 || second.NextBefore != 0 || second.Items[2].Kind != "started" {
		t.Fatal("event pagination skipped history")
	}
	if first.Items[49].Sequence <= second.Items[0].Sequence {
		t.Fatal("pagination repeated items")
	}
	// Fail the journal insert after the projection/context have been written.
	if _, err := s.db.Exec(`CREATE TRIGGER fail_activity_event BEFORE INSERT ON activity_events BEGIN SELECT RAISE(ABORT,'test journal failure'); END`); err != nil {
		t.Fatal(err)
	}
	next := activity.Command{RequestID: "rollback-request01", Action: "context", SessionID: current.ID, Revision: current.Revision, Project: "Must roll back"}
	command(c, next, 500)
	state := responseAs[activity.State](t, c.call("GET", "/api/activity", nil, 200))
	if !reflect.DeepEqual(state.Current, &current) {
		t.Fatal("partial change survived journal failure")
	}
	if _, err := s.db.Exec("DROP TRIGGER fail_activity_event"); err != nil {
		t.Fatal(err)
	}
	command(c, next, 200)
}

func TestActivitySurvivesRestartAndHostCopy(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := &client{t: t, handler: s.Handler()}
	c.signIn()
	before := command(c, activity.Command{RequestID: "persist-start-001", Action: "start", Title: "Across hosts"}, 200)
	identity := responseAs[activity.State](t, c.call("GET", "/api/activity", nil, 200)).Identity
	if identity.UserID != ownerPrincipal.ID || identity.InstallationID == "" {
		t.Fatalf("identity is not the verified principal plus the installation: %+v", identity)
	}
	c.signOut()
	c.call("GET", "/api/activity", nil, 401)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	// A stopped database copy to another data root models a host move without
	// conflating the server's address with human/session identity.
	moved := t.TempDir()
	// The whole database set moves: a WAL database keeps recent commits in its -wal file.
	files, err := filepath.Glob(filepath.Join(dir, "basalisk.db*"))
	if err != nil || len(files) == 0 {
		t.Fatal("no database files", err)
	}
	for _, f := range files {
		data, readErr := os.ReadFile(f) //nolint:gosec // test copies its own temp files
		if readErr != nil {
			t.Fatal(readErr)
		}
		if err = os.WriteFile(filepath.Join(moved, filepath.Base(f)), data, 0600); err != nil { //nolint:gosec // test temp dir
			t.Fatal(err)
		}
	}
	s, err = Open(moved)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	c.handler = s.Handler()
	c.signIn()
	after := responseAs[activity.State](t, c.call("GET", "/api/activity", nil, 200))
	if after.Identity != identity || !reflect.DeepEqual(after.Current, &before) {
		t.Fatalf("identity or activity changed across restart/host move: %+v %+v / %+v %+v", after.Identity, after.Current, identity, before)
	}
}

// Activity sessions belong to the person who started them: another signed-in person neither
// sees nor changes them, and a reused request id returns no one else's receipt.
func TestActivityIsPerUser(t *testing.T) {
	_, c := newStore(t)
	c.signIn()
	start := activity.Command{RequestID: "per-user-start-001", Action: "start", Title: "Mine"}
	mine := command(c, start, 200)

	other := ownerPrincipal
	other.ID = "user:usr_other"
	c.principal = &other
	if state := responseAs[activity.State](t, c.call("GET", "/api/activity", nil, 200)); state.Current != nil || state.Identity.UserID != other.ID {
		t.Fatalf("another user sees %+v", state)
	}
	if page := responseAs[activity.Page[activity.Session]](t, c.call("GET", "/api/activity/sessions", nil, 200)); len(page.Items) != 0 {
		t.Fatal("another user lists my sessions")
	}
	c.call("GET", "/api/activity/sessions/"+mine.ID+"/events", nil, 404)
	c.call("GET", "/api/activity/sessions/"+mine.ID+"/artifacts", nil, 404)
	command(c, activity.Command{RequestID: "per-user-end-00001", Action: "end", SessionID: mine.ID, Revision: mine.Revision}, 404)
	command(c, start, 409) // my request id, their caller
	theirs := command(c, activity.Command{RequestID: "per-user-start-002", Action: "start", Title: "Theirs"}, 200)
	if theirs.UserID != other.ID || theirs.ID == mine.ID {
		t.Fatalf("second user's session: %+v", theirs)
	}
}

func TestConcurrentActivityStartAndRetry(t *testing.T) {
	s, _ := newStore(t)
	start := activity.Command{RequestID: "concurrent-start01", Action: "start", Title: "Only one"}
	var wg sync.WaitGroup
	results := make(chan activity.Session, 2)
	errors := make(chan error, 2)
	for range 2 {
		wg.Go(func() { v, e := s.activity.Apply(t.Context(), ownerPrincipal.ID, start); results <- v; errors <- e })
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first string
	for result := range results {
		if first != "" && first != result.ID {
			t.Fatal("retry created another session")
		}
		first = result.ID
	}
	page, err := s.activity.Events(t.Context(), ownerPrincipal.ID, first, 0)
	if err != nil || len(page.Items) != 1 {
		t.Fatal("duplicate start events", err)
	}
}
