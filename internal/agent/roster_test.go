package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoster(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	if text, _ := rosterLines(store.Roster(ctx)); text != "" {
		t.Fatalf("an empty roster says nothing, got %q", text)
	}
	for _, bad := range []Seat{
		{Name: "No Address", Leads: "things"},
		{Name: "No Part", Address: "msg://agent/x/1"},
		{Name: "Odd", Address: "msg://agent/x/1", Leads: "things", Runs: "cloud"},
	} {
		if store.SaveSeat(ctx, bad) == nil {
			t.Errorf("%q should have been refused", bad.Name)
		}
	}
	seats := []Seat{
		{Name: "Task Ops Manager", Address: "msg://agent/x/ops", Runs: "tether", Leads: "the task queue"},
		{Name: "Archie", Address: "msg://agent/x/arch", Runs: "local", Leads: "architecture"},
		{Name: "Editor", Address: "msg://agent/x/ed", Leads: "the book", Roster: "personal"},
	}
	for _, seat := range seats {
		if err := store.SaveSeat(ctx, seat); err != nil {
			t.Fatal(err)
		}
	}
	got := store.Roster(ctx)
	if len(got) != 3 || got[0].Key != "task-ops-manager" || got[2].Roster != "personal" {
		t.Fatalf("roster = %+v", got)
	}
	for name, want := range map[string]string{
		"task-ops-manager": "msg://agent/x/ops", "@Task Ops Manager": "msg://agent/x/ops",
		"archie": "msg://agent/x/arch", "msg://agent/x/ed": "msg://agent/x/ed",
	} {
		if seat, ok := FindSeat(got, name); !ok || seat.Address != want {
			t.Errorf("FindSeat(%q) = %v %v", name, seat.Address, ok)
		}
	}
	if _, ok := FindSeat(got, "nobody"); ok {
		t.Error("found a seat that is not there")
	}
	text, stamp := rosterLines(got)
	for _, want := range []string{"<roster>", "- archie (Archie, local): architecture", "Roster personal:", "- editor (Editor): the book"} {
		if !strings.Contains(text, want) {
			t.Errorf("the block lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "msg://") {
		t.Error("the block carries addresses; keys are enough")
	}

	// A change to what a seat leads changes the stamp, and so is told again.
	got[1].Leads = "architecture and the migration"
	if err := store.SaveSeat(ctx, got[1]); err != nil {
		t.Fatal(err)
	}
	if _, after := rosterLines(store.Roster(ctx)); after == stamp {
		t.Error("the stamp did not change with the roster")
	}
	if len(store.Roster(ctx)) != 3 {
		t.Error("saving a seat again made another")
	}
	if err := store.DropSeat(ctx, "editor"); err != nil {
		t.Fatal(err)
	}
	if store.DropSeat(ctx, "editor") == nil {
		t.Error("dropping a seat twice should say it is gone")
	}

	// A name on the roster is used before the registry is asked.
	d := OwnerDeps{Seats: store.Roster}
	if addr, err := d.address(ctx, "@archie"); err != nil || addr != "msg://agent/x/arch" {
		t.Errorf("address = %q %v", addr, err)
	}
	if _, err := d.address(ctx, "stranger"); err == nil {
		t.Error("a stranger with no registry should be an error")
	}
}
