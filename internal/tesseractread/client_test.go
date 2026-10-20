package tesseractread

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/basalisk/internal/dataread"
	"github.com/hollis-labs/libs/security/auth"
)

const memoryNS = "project/read-test/memory/notes"
const workspaceNS = "project/read-test/workspace/drafts"

func wireRecord(id, kind, version string) map[string]any {
	ns := memoryNS
	versionField := "revision_id"
	if kind == "workspace" {
		ns = workspaceNS
		versionField = "version_token"
	}
	return map[string]any{"item_id": id, "domain": kind, "namespace": ns, versionField: version, "payload": map[string]any{"summary": "A summary", "body": "# Markdown", "data": map[string]string{"private": "do-not-expose"}}, "consumer_state": map[string]string{"private": "do-not-expose"}}
}
func whoami() map[string]any {
	return map[string]any{"mode": "enforce", "verified": true, "principal": "read-service", "kind": "service", "scopes": []string{Scope}, "namespace_globs": []string{memoryNS, workspaceNS}}
}
func fakeClient(t *testing.T, who map[string]any, handler http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer disposable-reader" {
			t.Error("wrong credential")
		}
		if r.URL.Path == "/v1/auth/whoami" {
			json.NewEncoder(w).Encode(who)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(s.Close)
	c, e := New(Config{Instance: "test-tess", Origin: s.URL, Principal: "read-service", Namespaces: []string{memoryNS, workspaceNS}}, func(context.Context) (string, error) { return "disposable-reader", nil }, s.Client())
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func req(kind, mode, version string) dataread.GetRequest {
	v := dataread.VersionRequest{Mode: mode}
	if mode == "revision" {
		v.ID = version
	}
	if mode == "if_current" {
		v.Token = version
	}
	return dataread.GetRequest{Ref: dataread.Ref{Provider: "tesseract", Instance: "test-tess", Kind: kind, ID: "item-1"}, Version: v}
}
func caller() context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{Verified: true, ID: "human-1", Kind: auth.KindOperator})
}
func permit(_ context.Context, _ auth.Principal, target dataread.Target) (dataread.Grant, error) {
	if target.Source.Provider != "" && target.Projection != dataread.TesseractProjection {
		return dataread.Grant{}, dataread.ErrAccess
	}
	return dataread.Grant{ID: "read-grant", PolicyVersion: "policy-1", WholeSelection: true, WholeHistory: true}, nil
}
func TestCurrentAndWorkspaceVersions(t *testing.T) {
	kind, version := "memory", "rev-1"
	c := fakeClient(t, whoami(), func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/items/item-1" {
			t.Error("wrong route", r.URL)
		}
		json.NewEncoder(w).Encode(wireRecord("item-1", kind, version))
	})
	h, e := dataread.New(dataread.AuthorizeFunc(permit), nil, c)
	if e != nil {
		t.Fatal(e)
	}
	got, e := h.Get(caller(), req("memory", "latest", ""))
	if e != nil || got.Item.Version.Kind != "immutable_revision" || got.Item.Version.ID != "rev-1" {
		t.Fatal(got, e)
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "do-not-expose") {
		t.Fatal("raw data escaped")
	}
	kind, version = "workspace", "token-1"
	got, e = h.Get(caller(), req("workspace", "if_current", "token-1"))
	if e != nil || got.Item.Version.Kind != "current_token" {
		t.Fatal(got, e)
	}
	if got, e = h.Get(caller(), req("workspace", "if_current", "old-token")); e != dataread.ErrChanged || got.Item.BodyMarkdown != "" {
		t.Fatal("stale workspace exposed content", got, e)
	}
	if _, e = h.Get(caller(), req("workspace", "revision", "token-1")); e != dataread.ErrUnsupported {
		t.Fatal("workspace fabricated history", e)
	}
}
func TestExactRevisionUsesAuthorizedItemHistory(t *testing.T) {
	calls := 0
	c := fakeClient(t, whoami(), func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/items/item-1/history" {
			t.Error("unsafe revision route", r.URL)
		}
		if r.URL.Query().Get("budget_bytes") == "" || r.URL.Query().Get("limit") != "50" {
			t.Error("unbounded history")
		}
		id := "rev-new"
		var cursor any = "native-next"
		truncated := true
		reason := "limit"
		if r.URL.Query().Get("cursor") == "native-next" {
			id = "rev-old"
			cursor = nil
			truncated = false
			reason = ""
		}
		json.NewEncoder(w).Encode(map[string]any{"results": []any{wireRecord("item-1", "memory", id)}, "manifest": map[string]any{"results_returned": 1, "results_total": 2, "truncated": truncated, "truncation_reason": reason, "next_cursor": cursor}})
	})
	it, e := c.Get(context.Background(), req("memory", "revision", "rev-old"))
	if e != nil || it.Version.ID != "rev-old" || calls != 2 {
		t.Fatal(it, e, calls)
	}
	if _, e = c.Get(context.Background(), req("memory", "revision", "missing")); e != dataread.ErrVersion {
		t.Fatal(e)
	}
}
func TestCredentialCeilingBeforeContent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"wildcard namespaces", func(w map[string]any) { w["namespace_globs"] = []string{"project/*"} }},
		{"extra scope", func(w map[string]any) { w["scopes"] = []string{Scope, "tesseract.write"} }},
		{"permissive", func(w map[string]any) { w["mode"] = "permissive" }},
		{"unverified", func(w map[string]any) { w["verified"] = false }},
		{"different principal", func(w map[string]any) { w["principal"] = "another-service" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := whoami()
			tc.change(w)
			c := fakeClient(t, w, func(http.ResponseWriter, *http.Request) { t.Error("reached content with wrong credential ceiling") })
			if _, e := c.Get(context.Background(), req("memory", "latest", "")); e != dataread.ErrAccess {
				t.Fatal(e)
			}
		})
	}
}
func TestMalformedOrOutOfScopeResponsesRefuseWholePage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		want   error
	}{
		{"foreign namespace", func(r map[string]any) { r["namespace"] = "project/secret/memory/notes" }, dataread.ErrAccess},
		{"foreign identity", func(r map[string]any) { r["item_id"] = "other-item" }, dataread.ErrUnavailable},
		{"mixed version", func(r map[string]any) { r["version_token"] = "workspace-token" }, dataread.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fakeClient(t, whoami(), func(w http.ResponseWriter, r *http.Request) {
				v := wireRecord("item-1", "memory", "rev-1")
				tc.mutate(v)
				json.NewEncoder(w).Encode(v)
			})
			if _, e := c.Get(context.Background(), req("memory", "latest", "")); e != tc.want {
				t.Fatal(e)
			}
		})
	}
}
func TestRecallPreservesManifestAndCursorIsolation(t *testing.T) {
	calls := 0
	c := fakeClient(t, whoami(), func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/v1/tesseract/lookup" {
			t.Error("wrong recall route", r.URL)
		}
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		if in["payload_mode"] != "full" || in["search_mode"] != "lexical" || in["ranking"] != "relevance" {
			t.Error("uncontrolled recall", in)
		}
		items := []any{}
		for i := 0; i < 3; i++ {
			items = append(items, map[string]any{"revision": wireRecord(fmt.Sprintf("item-%d", i), "memory", fmt.Sprintf("rev-%d", i)), "score": 0.25})
		}
		json.NewEncoder(w).Encode(map[string]any{"results": items, "manifest": map[string]any{"results_returned": 3, "results_total": 8, "truncated": true, "truncation_reason": "budget_bytes", "next_cursor": nil}})
	})
	h, e := dataread.New(dataread.AuthorizeFunc(permit), nil, c)
	if e != nil {
		t.Fatal(e)
	}
	q := dataread.QueryRequest{Source: c.source(), Mode: "recall", Recall: &dataread.RecallSelector{Namespace: memoryNS, Kind: "memory", Query: "markdown"}, Limit: 2}
	page, e := h.Query(caller(), q)
	if e != nil || len(page.Items) != 2 || page.Exhausted || page.NextCursor == "" || page.Coverage != "recall_candidates" || page.Items[0].Score == nil {
		t.Fatal(page, e)
	}
	q.Cursor = page.NextCursor
	original := *q.Recall
	q.Recall = &dataread.RecallSelector{Namespace: workspaceNS, Kind: "workspace", Query: "markdown"}
	if _, e = h.Query(caller(), q); e != dataread.ErrCursor {
		t.Fatal("cursor scope widened", e)
	}
	q.Recall = &original
	last, e := h.Query(caller(), q)
	if e != nil || len(last.Items) != 1 || last.Status != "partial" || last.Exhausted || last.NextCursor != "" || !last.SourceTruncated || last.SourceTruncationReason != "budget_bytes" || calls != 1 || !last.ObservedAt.Equal(page.ObservedAt) {
		t.Fatal(last, e, calls)
	}
	if _, e = c.Recall(context.Background(), dataread.RecallSelector{Namespace: "project/*", Kind: "memory"}, ""); e != dataread.ErrAccess || calls != 1 {
		t.Fatal("broad selector reached source", e)
	}
}
func TestTesseractHTTPAndCommonDispatchParity(t *testing.T) {
	c := fakeClient(t, whoami(), func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(wireRecord("item-1", "workspace", "token-2"))
	})
	h, e := dataread.New(dataread.AuthorizeFunc(permit), nil, c)
	if e != nil {
		t.Fatal(e)
	}
	for _, version := range []string{"token-2", "stale"} {
		b, _ := json.Marshal(req("workspace", "if_current", version))
		out, callErr := h.Call(caller(), "data.get", b)
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/data/get", strings.NewReader(string(b))).WithContext(caller())
		h.Handler().ServeHTTP(w, r)
		if version == "stale" {
			if callErr != dataread.ErrChanged || w.Code != 409 || strings.Contains(w.Body.String(), "Markdown") {
				t.Fatal("stale transport mismatch", callErr, w.Code)
			}
		} else if callErr != nil || out == nil || w.Code != 200 {
			t.Fatal(callErr, w.Code)
		}
	}
}

func TestSourceRevocationBlocksBufferedContinuation(t *testing.T) {
	who := whoami()
	calls := 0
	c := fakeClient(t, who, func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"revision": wireRecord("one", "memory", "r1")}, map[string]any{"revision": wireRecord("two", "memory", "r2")}}, "manifest": map[string]any{"results_returned": 2, "results_total": 2, "truncated": false, "next_cursor": nil}})
	})
	h, e := dataread.New(dataread.AuthorizeFunc(permit), nil, c)
	if e != nil {
		t.Fatal(e)
	}
	q := dataread.QueryRequest{Source: c.source(), Mode: "recall", Recall: &dataread.RecallSelector{Namespace: memoryNS, Kind: "memory"}, Limit: 1}
	page, e := h.Query(caller(), q)
	if e != nil || page.NextCursor == "" {
		t.Fatal(page, e)
	}
	who["verified"] = false
	q.Cursor = page.NextCursor
	if got, e := h.Query(caller(), q); e != dataread.ErrAccess || len(got.Items) != 0 || calls != 1 {
		t.Fatal("revoked source served buffered content", got, e, calls)
	}
}

func TestHistoryBudgetNeverBecomesMissingRevision(t *testing.T) {
	calls := 0
	c := fakeClient(t, whoami(), func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/items/item-1/history" {
			t.Error("unsafe fallback", r.URL)
		}
		json.NewEncoder(w).Encode(map[string]any{"results": []any{wireRecord("item-1", "memory", fmt.Sprintf("rev-%d", calls))}, "manifest": map[string]any{"results_returned": 1, "results_total": 1000, "truncated": true, "truncation_reason": "budget_bytes", "next_cursor": fmt.Sprintf("cursor-%d", calls)}})
	})
	if _, e := c.Get(context.Background(), req("memory", "revision", "unvisited-revision")); e != dataread.ErrLimit || calls != 4 {
		t.Fatal("budget exhaustion claimed absence", e, calls)
	}
}

func TestExactRevisionGrantDoesNotAuthorizeHistoryScan(t *testing.T) {
	c := fakeClient(t, whoami(), func(http.ResponseWriter, *http.Request) { t.Error("history candidates fetched without permission") })
	h, e := dataread.New(dataread.AuthorizeFunc(func(ctx context.Context, p auth.Principal, target dataread.Target) (dataread.Grant, error) {
		g, e := permit(ctx, p, target)
		g.WholeHistory = false
		return g, e
	}), nil, c)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.Get(caller(), req("memory", "revision", "rev-1")); e != dataread.ErrUnsupported {
		t.Fatal("history scan widened grant", e)
	}
}

func TestReadTransportBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"redirect", 302, "", dataread.ErrUnavailable},
		{"oversized", 200, strings.Repeat("x", dataread.MaxBytes+1), dataread.ErrLimit},
		{"upstream error", 500, "secret upstream diagnostics", dataread.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fakeClient(t, whoami(), func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/redirected" {
					t.Error("followed redirect")
				}
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			if _, e := c.Get(context.Background(), req("memory", "latest", "")); e != tc.want {
				t.Fatal(e)
			}
		})
	}
}
