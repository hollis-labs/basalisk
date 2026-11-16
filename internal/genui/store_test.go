package genui

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hollis-labs/basalisk/internal/persistence/persistencetest"
	contract "github.com/hollis-labs/substrate/genui"
	"github.com/hollis-labs/substrate/genui/gateway"
)

func TestDurableJournalAtomicIdentityAndPermanentClaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.db")
	db, e := persistencetest.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	repo := &repository{db: db.DB()}
	key := gateway.Key{Scope: scope("user:owner"), ActionID: "logical-action"}
	call := contract.Call{Version: contract.ProtocolVersion, CallAgentFunction: contract.ActionRequest{SurfaceID: "review", FunctionCallID: "first", CallFunction: choice("Apply", "logical-action", "user:owner", contract.Basis{Revision: "1"}, map[string]any{"theme": "light"}).CallFunction}}
	initial := gateway.Record{Digest: "immutable-intent", Request: call, Phase: gateway.PhaseReady}
	var wg sync.WaitGroup
	results := make(chan bool, 16)
	errs := make(chan error, 16)
	for range 16 {
		wg.Go(func() {
			_, inserted, beginErr := repo.Begin(t.Context(), key, initial)
			results <- inserted
			errs <- beginErr
		})
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	winners := 0
	for inserted := range results {
		if inserted {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("logical key allocated %d times", winners)
	}
	rec, e := repo.Load(t.Context(), key)
	if e != nil {
		t.Fatal(e)
	}
	rec.Claimed = true
	rec.Phase = gateway.PhaseClaimed
	rec, ok, e := repo.CompareAndSwap(t.Context(), key, rec.Revision, rec)
	if e != nil || !ok {
		t.Fatal("claim", e)
	}
	for _, change := range []func(*gateway.Record){func(v *gateway.Record) { v.Claimed = false }, func(v *gateway.Record) { v.Digest = "other" }, func(v *gateway.Record) { v.Request.CallAgentFunction.FunctionCallID = "other" }} {
		candidate := rec
		change(&candidate)
		_, ok, e = repo.CompareAndSwap(t.Context(), key, rec.Revision, candidate)
		if e != nil || ok {
			t.Fatal("immutable identity/claim bypass", e)
		}
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	db, e = persistencetest.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = db.Close() }()
	repo = &repository{db: db.DB()}
	got, e := repo.Load(t.Context(), key)
	if e != nil || !got.Claimed || got.Phase != gateway.PhaseClaimed {
		t.Fatalf("restart released uncertainty %+v %v", got, e)
	}
	before, _ := json.Marshal(rec)
	after, _ := json.Marshal(got)
	if string(before) != string(after) {
		t.Fatal("restart rewrote canonical original")
	}
	if _, e = repo.Load(t.Context(), gateway.Key{Scope: scope("user:other"), ActionID: key.ActionID}); e == nil {
		t.Fatal("cross-owner journal leak")
	}
}
