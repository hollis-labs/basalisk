package dataread

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/security/auth"
)

var testSource = Source{"fragments-engine", "test-instance"}

func testItem(id string) Item {
	return Item{Ref: Ref{testSource.Provider, testSource.Instance, "fragment", id}, Version: Version{"immutable_revision", "rev-" + id}, ProjectionVersion: "1", Title: "private-" + id, ContentTrust: "untrusted_source"}
}
func testGet(id string) GetRequest {
	return GetRequest{testItem(id).Ref, VersionRequest{Mode: "latest"}}
}
func verified() context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{Verified: true, ID: "owner-1", Kind: auth.KindOperator, TokenID: "login-1"})
}
func allow(context.Context, auth.Principal, Target) (Grant, error) {
	return Grant{ID: "read-grant", PolicyVersion: "policy-1", WholeSelection: true}, nil
}

type fakeProvider struct {
	calls      int
	get        func(GetRequest) (Item, error)
	selectPage func(string) (NativePage, error)
}

func (p *fakeProvider) Describe() Descriptor {
	return Descriptor{Source: testSource, AdapterVersion: "test-v1", Kinds: []string{"fragment"}, VersionModes: []string{"latest", "revision"}, SelectionScopes: []string{"all"}, Projection: Projection, MaxItems: MaxItems}
}
func (p *fakeProvider) Get(_ context.Context, r GetRequest) (Item, error) {
	p.calls++
	if p.get != nil {
		return p.get(r)
	}
	return testItem(r.Ref.ID), nil
}
func (p *fakeProvider) Select(_ context.Context, scope, cursor string) (NativePage, error) {
	p.calls++
	return p.selectPage(cursor)
}

type memberFunc func(context.Context, auth.Principal, Source, string, string) (MembershipPage, error)

func (f memberFunc) Select(ctx context.Context, p auth.Principal, s Source, id, cursor string) (MembershipPage, error) {
	return f(ctx, p, s, id, cursor)
}
func host(t *testing.T, p *fakeProvider, policy Authorizer, m Membership) *Host {
	t.Helper()
	h, e := New(policy, m, p)
	if e != nil {
		t.Fatal(e)
	}
	return h
}

func TestAuthorizationBeforeReadAndAfterResponse(t *testing.T) {
	p := &fakeProvider{}
	h := host(t, p, nil, nil)
	if _, e := h.Get(verified(), testGet("one")); e != ErrAccess || p.calls != 0 {
		t.Fatal("default policy did not deny", e, p.calls)
	}
	h = host(t, p, AuthorizeFunc(allow), nil)
	for _, ctx := range []context.Context{context.Background(), auth.WithPrincipal(context.Background(), auth.Principal{ID: "owner-1"}), auth.WithPrincipal(context.Background(), auth.Principal{Verified: true, ID: "owner-1", ExpiresAt: time.Now().Add(-time.Second)})} {
		if _, e := h.Get(ctx, testGet("one")); e != ErrAccess || p.calls != 0 {
			t.Fatal("unverified/expired authority reached provider", e)
		}
	}
	revoked := false
	h = host(t, p, AuthorizeFunc(func(c context.Context, a auth.Principal, target Target) (Grant, error) {
		if revoked {
			return Grant{}, ErrAccess
		}
		return allow(c, a, target)
	}), nil)
	p.get = func(in GetRequest) (Item, error) { revoked = true; return testItem(in.Ref.ID), nil }
	if got, e := h.Get(verified(), testGet("one")); e != ErrAccess || got.Item.Title != "" {
		t.Fatal("in-flight revocation leaked content", got, e)
	}
}

func TestExactIdentityAndRevision(t *testing.T) {
	p := &fakeProvider{}
	h := host(t, p, AuthorizeFunc(allow), nil)
	in := testGet("one")
	in.Version = VersionRequest{Mode: "revision", ID: "original-revision"}
	if _, e := h.Get(verified(), in); e != ErrVersion {
		t.Fatal("revision fell back", e)
	}
	p.get = func(GetRequest) (Item, error) { return testItem("canonical-other"), nil }
	if _, e := h.Get(verified(), testGet("one")); e != ErrUnavailable {
		t.Fatal("alias crossed authorization boundary", e)
	}
	in.Version = VersionRequest{Mode: "if_current", ID: "current"}
	if _, e := h.Get(verified(), in); e != ErrInvalid {
		t.Fatal("unsupported version accepted", e)
	}
}

func TestPaginationBindsGrantCallerAndQueryWithoutDroppingItems(t *testing.T) {
	p := &fakeProvider{selectPage: func(cursor string) (NativePage, error) {
		if cursor == "" {
			items := []Item{}
			for i := 0; i < 50; i++ {
				items = append(items, testItem(fmt.Sprintf("item-%02d", i)))
			}
			return NativePage{Items: items, Cursor: "private-native-cursor"}, nil
		}
		if cursor != "private-native-cursor" {
			t.Fatal("wrong provider continuation", cursor)
		}
		return NativePage{Items: []Item{testItem("last")}}, nil
	}}
	policyVersion := "policy-1"
	h := host(t, p, AuthorizeFunc(func(ctx context.Context, p auth.Principal, target Target) (Grant, error) {
		g, _ := allow(ctx, p, target)
		g.PolicyVersion = policyVersion
		return g, nil
	}), nil)
	q := QueryRequest{Source: testSource, Mode: "selection", Scope: "all", Limit: 13}
	first, e := h.Query(verified(), q)
	if e != nil || len(first.Items) != 13 || first.Exhausted || first.NextCursor == "" {
		t.Fatal(first, e)
	}
	encoded, _ := json.Marshal(first)
	if strings.Contains(string(encoded), "private-native-cursor") {
		t.Fatal("native cursor leaked")
	}
	q.Cursor = first.NextCursor
	other := auth.WithPrincipal(context.Background(), auth.Principal{ID: "another-owner", Verified: true})
	if _, e = h.Query(other, q); e != ErrCursor {
		t.Fatal("caller replay", e)
	}
	changed := q
	changed.Limit++
	if _, e = h.Query(verified(), changed); e != ErrCursor {
		t.Fatal("query replay", e)
	}
	policyVersion = "policy-2"
	if _, e = h.Query(verified(), q); e != ErrCursor {
		t.Fatal("policy replay", e)
	}
	policyVersion = "policy-1"
	ids := []string{}
	for _, it := range first.Items {
		ids = append(ids, it.Ref.ID)
	}
	for i := 0; i < 5 && q.Cursor != ""; i++ {
		page, err := h.Query(verified(), q)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range page.Items {
			ids = append(ids, it.Ref.ID)
		}
		q.Cursor = page.NextCursor
	}
	if len(ids) != 51 || q.Cursor != "" || p.calls != 2 {
		t.Fatal("lost or repeated provider pages", len(ids), p.calls, q.Cursor)
	}
	for i := 0; i < 50; i++ {
		if ids[i] != fmt.Sprintf("item-%02d", i) {
			t.Fatal("page order changed", ids)
		}
	}
	if ids[50] != "last" {
		t.Fatal(ids)
	}
	h.now = func() time.Time { return time.Now().Add(3 * time.Minute) }
	q.Cursor = first.NextCursor
	if _, e = h.Query(verified(), q); e != ErrCursor {
		t.Fatal("expired cursor", e)
	}
	h = host(t, p, AuthorizeFunc(allow), nil)
	if _, e = h.Query(verified(), q); e != ErrCursor {
		t.Fatal("restart cursor survived", e)
	}
}

func TestSelectionNeedsWholeCandidateAuthorization(t *testing.T) {
	p := &fakeProvider{}
	h := host(t, p, AuthorizeFunc(func(context.Context, auth.Principal, Target) (Grant, error) {
		return Grant{ID: "one-resource", PolicyVersion: "p"}, nil
	}), nil)
	if _, e := h.Query(verified(), QueryRequest{Source: testSource, Mode: "selection", Scope: "all"}); e != ErrUnsupported || p.calls != 0 {
		t.Fatal("broad selection reached provider", e)
	}
}

func TestSessionMembershipIsNotAReadGrantAndMissingContentIsPartial(t *testing.T) {
	m := memberFunc(func(context.Context, auth.Principal, Source, string, string) (MembershipPage, error) {
		one, two := testGet("one"), testGet("two")
		one.Version = VersionRequest{Mode: "revision", ID: "rev-one"}
		two.Version = VersionRequest{Mode: "revision", ID: "rev-two"}
		return MembershipPage{Refs: []GetRequest{one, two}}, nil
	})
	p := &fakeProvider{get: func(in GetRequest) (Item, error) {
		if in.Ref.ID == "two" {
			return Item{}, ErrVersion
		}
		return testItem("one"), nil
	}}
	h := host(t, p, AuthorizeFunc(func(c context.Context, p auth.Principal, target Target) (Grant, error) {
		if target.Action == "get" && target.Get.Ref.ID == "two" {
			return Grant{}, ErrAccess
		}
		return allow(c, p, target)
	}), m)
	q := QueryRequest{Source: testSource, Mode: "session", SessionID: "session-1"}
	if _, e := h.Query(verified(), q); e != ErrAccess || p.calls != 0 {
		t.Fatal("membership conferred access", e, p.calls)
	}
	h = host(t, p, AuthorizeFunc(allow), m)
	page, e := h.Query(verified(), q)
	if e != nil || page.Status != "partial" || len(page.Items) != 1 || len(page.Problems) != 1 || page.Problems[0].Code != "version_unavailable" || !page.Exhausted {
		t.Fatal(page, e)
	}
}

func TestHTTPAndToolDispatchUseTheSameBoundary(t *testing.T) {
	p := &fakeProvider{}
	h := host(t, p, AuthorizeFunc(allow), nil)
	body, _ := json.Marshal(testGet("one"))
	for _, verifiedCaller := range []bool{false, true} {
		ctx := context.Background()
		want := 403
		if verifiedCaller {
			ctx = verified()
			want = 200
		}
		_, callErr := h.Call(ctx, "data.get", body)
		r := httptest.NewRequest("POST", "/api/data/get", strings.NewReader(string(body))).WithContext(ctx)
		w := httptest.NewRecorder()
		h.Handler().ServeHTTP(w, r)
		if w.Code != want || (callErr == nil) != verifiedCaller {
			t.Fatal("transport bypass", w.Code, callErr)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable response")
		}
	}
	if _, e := h.Call(verified(), "data.get", []byte(`{"principal":{"verified":true},"ref":{},"version":{}}`)); e != ErrInvalid {
		t.Fatal("client authority accepted", e)
	}
}

func TestOutputBudgetAndSafeErrors(t *testing.T) {
	p := &fakeProvider{get: func(in GetRequest) (Item, error) {
		it := testItem(in.Ref.ID)
		it.Summary = strings.Repeat("x", MaxBytes)
		return it, nil
	}}
	h := host(t, p, AuthorizeFunc(allow), nil)
	if _, e := h.Get(verified(), testGet("one")); e != ErrLimit {
		t.Fatal(e)
	}
	p.get = func(GetRequest) (Item, error) { return Item{}, fmt.Errorf("upstream secret detail") }
	if _, e := h.Get(verified(), testGet("one")); e != ErrUnavailable {
		t.Fatal("raw error escaped", e)
	}
}
