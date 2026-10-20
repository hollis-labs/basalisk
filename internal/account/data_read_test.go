package account

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/basalisk/internal/dataread"
	"github.com/hollis-labs/libs/security/auth"
)

type sessionReadProvider struct {
	calls  int
	source dataread.Source
}

func (p *sessionReadProvider) Describe() dataread.Descriptor {
	return dataread.Descriptor{Source: p.source, AdapterVersion: "test-v1", Kinds: []string{"fragment"}, VersionModes: []string{"latest", "revision"}, Projection: dataread.Projection, MaxItems: dataread.MaxItems}
}
func (p *sessionReadProvider) Get(_ context.Context, in dataread.GetRequest) (dataread.Item, error) {
	p.calls++
	return dataread.Item{Ref: in.Ref, Version: dataread.Version{Kind: "immutable_revision", ID: in.Version.ID}, ProjectionVersion: "1", Title: "Canonical FE content", ContentTrust: "untrusted_source"}, nil
}
func (p *sessionReadProvider) Select(context.Context, string, string) (dataread.NativePage, error) {
	panic("session query must not scan FE inventory")
}
func readAllow(context.Context, auth.Principal, dataread.Target) (dataread.Grant, error) {
	return dataread.Grant{ID: "explicit-read-grant", PolicyVersion: "policy-1"}, nil
}

func TestSessionReadUsesOnlyConfirmedReceipts(t *testing.T) {
	f := newDeliveryFixture(t, true)
	p := &sessionReadProvider{source: dataread.Source{Provider: "fragments-engine", Instance: "stable-fe-instance"}}
	h, e := f.s.NewReadHost(dataread.AuthorizeFunc(readAllow), ReadSource{p, f.in.IntegrationID, f.server.URL})
	if e != nil {
		t.Fatal(e)
	}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Verified: true, ID: f.session.UserID, Kind: auth.KindOperator})
	q := dataread.QueryRequest{Source: p.source, Mode: "session", SessionID: f.session.ID}
	page, e := h.Query(ctx, q)
	if e != nil || len(page.Items) != 0 || p.calls != 0 {
		t.Fatal("queued capture became membership", page, e)
	}
	// A valid device-reported artifact is still not authoritative FE membership.
	raw, _ := json.Marshal(artifactReceipt{CaptureID: "device-reported-1", FragmentID: "claimed-fragment", Draft: *f.in.Draft})
	if _, e = f.s.db.Exec("INSERT INTO companion_artifacts(device_id,capture_id,session_id,payload_hash,payload,received_at) VALUES(?,?,?,?,?,?)", f.device.Device.ID, "device-reported-1", f.session.ID, "hash", string(raw), f.in.SubmittedAt); e != nil {
		t.Fatal(e)
	}
	page, e = h.Query(ctx, q)
	if e != nil || len(page.Items) != 0 || p.calls != 0 {
		t.Fatal("client claim became membership", page, e)
	}
	f.run(t)
	page, e = h.Query(ctx, q)
	if e != nil || len(page.Items) != 1 || page.Items[0].Ref.ID != "confirmed-fragment" || page.Items[0].Version.ID != "confirmed-revision" || p.calls != 1 {
		t.Fatal("confirmed capture missing", page, e)
	}
	other := auth.WithPrincipal(context.Background(), auth.Principal{Verified: true, ID: "another-owner"})
	if _, e = h.Query(other, q); e != dataread.ErrAccess || p.calls != 1 {
		t.Fatal("foreign session disclosed", e)
	}
	h, e = f.s.NewReadHost(nil, ReadSource{p, f.in.IntegrationID, f.server.URL})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.Query(ctx, q); e != dataread.ErrAccess || p.calls != 1 {
		t.Fatal("session bypassed read grant", e)
	}
	// A repointed integration is not the same source without an explicit mapping.
	h, e = f.s.NewReadHost(dataread.AuthorizeFunc(readAllow), ReadSource{p, f.in.IntegrationID, "https://another-fe.example"})
	if e != nil {
		t.Fatal(e)
	}
	page, e = h.Query(ctx, q)
	if e != nil || len(page.Items) != 0 || p.calls != 1 {
		t.Fatal("origin reassigned historical membership", page, e)
	}
}

func TestSessionReadRejectsMismatchedConfirmation(t *testing.T) {
	f := newDeliveryFixture(t, true)
	f.run(t)
	p := &sessionReadProvider{source: dataread.Source{Provider: "fragments-engine", Instance: "fe-instance"}}
	h, e := f.s.NewReadHost(dataread.AuthorizeFunc(readAllow), ReadSource{p, f.in.IntegrationID, f.server.URL})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.db.Exec("UPDATE fragment_submissions SET fragment_id='different-confirmation' WHERE sequence=?", f.status.Sequence); e != nil {
		t.Fatal(e)
	}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Verified: true, ID: f.session.UserID})
	page, e := h.Query(ctx, dataread.QueryRequest{Source: p.source, Mode: "session", SessionID: f.session.ID})
	if e != nil || len(page.Items) != 0 || p.calls != 0 {
		t.Fatal("inconsistent confirmation used", page, e)
	}
}
