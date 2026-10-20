package feread

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/basalisk/internal/dataread"
)

func testToken(scope string) string {
	b, _ := json.Marshal(map[string]any{"sub": "reader-service", "aud": "fragments-engine", "scope": scope, "kind": "service", "exp": time.Now().Add(time.Minute).Unix()})
	return "test." + base64.RawURLEncoding.EncodeToString(b) + ".synthetic"
}
func readRequest() dataread.GetRequest {
	return dataread.GetRequest{Ref: dataread.Ref{Provider: "fragments-engine", Instance: "fe-instance", Kind: "fragment", ID: "fragment-1"}, Version: dataread.VersionRequest{Mode: "revision", ID: "revision-1"}}
}
func readerJSON(id, revision string) string {
	return fmt.Sprintf(`{"schema_version":"fe.reader.item.v1","fragment_id":%q,"fragment_revision_id":%q,"revision":2,"display":{"title":{"value":"Title"},"summary":{"value":"Summary"}},"article":{"preview_markdown":"# Markdown","full_content_href":"https://private-source/secret"},"curated_note":{"body_markdown":"private personal overlay"},"reading_state":{"principal_id":"local-user"},"actions":[{"command":"delete"}],"metadata":{"token":"do-not-expose"}}`, id, revision)
}
func makeClient(t *testing.T, handler http.HandlerFunc, scope string) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	c, e := New("fe-instance", server.URL, "reader-service", func(context.Context) (string, error) { return testToken(scope), nil }, server.Client())
	if e != nil {
		t.Fatal(e)
	}
	return c, server
}

func TestReadUsesNarrowCredentialAndClosedProjection(t *testing.T) {
	expectedToken := testToken(Scope)
	c, _ := makeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/reader/items/fragment-1" || r.URL.Query().Get("revision_id") != "revision-1" || r.Header.Get("Authorization") != "Bearer "+expectedToken {
			t.Error("unexpected read request", r.URL, r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, readerJSON("fragment-1", "revision-1"))
	}, Scope)
	c.token = func(context.Context) (string, error) { return expectedToken, nil }
	it, e := c.Get(context.Background(), readRequest())
	if e != nil || it.PreviewMarkdown != "# Markdown" || it.Version.ID != "revision-1" || it.ProjectionVersion != "2" {
		t.Fatal(it, e)
	}
	b, _ := json.Marshal(it)
	for _, private := range []string{"local-user", "private-source", "personal overlay", "do-not-expose", "delete"} {
		if strings.Contains(string(b), private) {
			t.Fatal("raw Reader field escaped", private)
		}
	}
	if it.ContentTrust != "untrusted_source" {
		t.Fatal("source content was trusted")
	}
}

func TestNoCaptureOrOwnerCredentialFallback(t *testing.T) {
	for _, scope := range []string{"fragments-engine.capture", "fragments-engine.*", "*", Scope + " fragments-engine.write"} {
		t.Run(scope, func(t *testing.T) {
			calls := 0
			c, _ := makeClient(t, func(http.ResponseWriter, *http.Request) { calls++ }, scope)
			if _, e := c.Get(context.Background(), readRequest()); e != dataread.ErrAccess || calls != 0 {
				t.Fatal("wrong grant reached network", e, calls)
			}
		})
	}
	c, _ := makeClient(t, func(http.ResponseWriter, *http.Request) { t.Error("owner credential reached network") }, Scope)
	c.token = func(context.Context) (string, error) { return strings.Repeat("a", 64), nil }
	if _, e := c.Get(context.Background(), readRequest()); e != dataread.ErrAccess {
		t.Fatal(e)
	}
}

func TestRedirectSizeIdentityAndVersionFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		expected error
	}{
		{"redirect", 302, "", dataread.ErrUnavailable},
		{"denied", 403, "sensitive upstream error", dataread.ErrAccess},
		{"missing revision", 404, "sensitive upstream error", dataread.ErrVersion},
		{"wrong revision", 200, readerJSON("fragment-1", "revision-2"), dataread.ErrVersion},
		{"canonical alias", 200, readerJSON("another-fragment", "revision-1"), dataread.ErrUnavailable},
		{"oversize", 200, strings.Repeat("x", dataread.MaxBytes+1), dataread.ErrLimit},
		{"malformed", 200, `{"schema_version":"unknown"}`, dataread.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := makeClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/redirect-target" {
					t.Error("followed redirect")
				}
				w.Header().Set("Location", "/redirect-target")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}, Scope)
			if _, e := c.Get(context.Background(), readRequest()); e != tc.expected {
				t.Fatal(e)
			}
		})
	}
}

func TestSelectionIsClosedAndKeepsNativeCursorInternal(t *testing.T) {
	c, _ := makeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/reader/items" || r.URL.Query().Get("scope") != "all" || r.URL.Query().Get("cursor") != "native?a&b" {
			t.Error("incorrect source selector", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"schema_version":"fe.reader.list.v1","scope":"all","items":[%s],"next_cursor":"next-native"}`, readerJSON("fragment-1", "revision-1"))
	}, Scope)
	page, e := c.Select(context.Background(), "all", "native?a&b")
	if e != nil || len(page.Items) != 1 || page.Cursor != "next-native" {
		t.Fatal(page, e)
	}
	if _, e = c.Select(context.Background(), "inbox", ""); e != dataread.ErrUnsupported {
		t.Fatal("personal Reader scope enabled", e)
	}
}
