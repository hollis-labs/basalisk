package tesseractread

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/basalisk/internal/dataread"
)

// Optional integration against an actual Tesseract executable. All credentials,
// config, listeners and source records are confined to t.TempDir, never live data.
func TestRealTesseract(t *testing.T) {
	binary := os.Getenv("BASALISK_TEST_TESSERACT_BINARY")
	if binary == "" {
		t.Skip("set BASALISK_TEST_TESSERACT_BINARY to exercise a disposable source")
	}
	dir := t.TempDir()
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "XDG_CONFIG_HOME=" + filepath.Join(dir, "config"), "XDG_DATA_HOME=" + filepath.Join(dir, "data"), "XDG_STATE_HOME=" + filepath.Join(dir, "state"), "XDG_CACHE_HOME=" + filepath.Join(dir, "cache"), "TESSERACT_DB_PATH=" + filepath.Join(dir, "test.db"), "HOLLIS_AUTH=enforce", "HOLLIS_OTEL_DISABLED=1", "OTEL_SDK_DISABLED=true"}
	cli := func(args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = dir
		cmd.Env = env
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		b, e := cmd.Output()
		if e != nil {
			t.Fatalf("disposable Tesseract CLI failed (%s): %v %s", args[1], e, stderr.String())
		}
		return b
	}
	privateNS := "project/private-test/memory/notes"
	knowledgeNS, eventNS := "project/read-test/knowledge/docs", "project/read-test/event/journal"
	allowedNS := []string{memoryNS, workspaceNS, knowledgeNS, eventNS}
	for _, ns := range append(append([]string{}, allowedNS...), privateNS) {
		cli("context", "namespace", "register", "--namespace", ns, "--owner-type", "project", "--owner-id", "read-test")
	}
	type credential struct {
		Token string `json:"token"`
		ID    string `json:"id"`
	}
	issue := func(name, scopes, namespaces string) credential {
		t.Helper()
		b := cli("context", "token", "create", "--name", name, "--client-id", "app:read-test", "--scopes", scopes, "--namespaces", namespaces, "--ttl", "15m", "--output", "json")
		var c credential
		if json.Unmarshal(b, &c) != nil || c.Token == "" || c.ID == "" {
			t.Fatal("invalid disposable token receipt")
		}
		return c
	}
	reader := issue("disposable-reader", "memory:read", strings.Join(allowedNS, ","))
	writer := issue("disposable-fixture-writer", "memory:read,memory:write,write", strings.Join(allowedNS, ",")+","+privateNS)
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	addr := l.Addr().String()
	l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binary, "serve", "--addr", addr, "--managed-auth")
	cmd.Dir = dir
	cmd.Env = env
	logFile, e := os.Create(filepath.Join(dir, "source.log"))
	if e != nil {
		cancel()
		t.Fatal(e)
	}
	defer logFile.Close()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if e = cmd.Start(); e != nil {
		cancel()
		t.Fatal(e)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	probe := &http.Client{Timeout: time.Second}
	target, _ := url.Parse("http://" + addr)
	ready := false
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); {
		r, e := probe.Get(target.String() + "/v1/health/readiness")
		if e == nil {
			r.Body.Close()
			if r.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		b, _ := os.ReadFile(logFile.Name())
		t.Fatal("disposable source failed to start: ", string(b))
	}
	proxy := httptest.NewTLSServer(httputil.NewSingleHostReverseProxy(target))
	defer proxy.Close()
	call := func(method, path, token string, body any, want int) []byte {
		t.Helper()
		b, _ := json.Marshal(body)
		r, _ := http.NewRequest(method, proxy.URL+path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		resp, e := proxy.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		raw, e := io.ReadAll(io.LimitReader(resp.Body, 2*dataread.MaxBytes))
		if e != nil {
			t.Fatal(e)
		}
		if resp.StatusCode != want {
			t.Fatalf("disposable source %s %s: %d %s", method, path, resp.StatusCode, raw)
		}
		return raw
	}
	write := func(ns, key, summary string) record {
		t.Helper()
		body := map[string]any{"namespace": ns, "memory_key": key, "summary": summary, "body": "# " + summary, "confidence": 0.9, "derived_from": "observation", "trigger": "manual", "session_id": "disposable-test", "author": map[string]string{"agent_id": "basalisk-test"}, "dedup": "none"}
		b := call("POST", "/v1/memory/write", writer.Token, body, 200)
		var r record
		if json.Unmarshal(b, &r) != nil {
			t.Fatal("bad write receipt")
		}
		return r
	}
	call("GET", "/v1/auth/whoami", reader.Token, nil, 200)
	first := write(memoryNS, "first", "Visible first revision")
	second := write(memoryNS, "first", "Visible second revision")
	private := write(privateNS, "hidden", "Hidden namespace content")
	for i := 0; i < 51; i++ {
		write(memoryNS, fmt.Sprintf("note_%d", i), fmt.Sprintf("Visible record %d", i))
	}
	workspaceCreate := map[string]any{"namespace": workspaceNS, "key": "draft", "summary": "Workspace draft", "body": "# Workspace v1", "session_id": "disposable-test", "author": map[string]string{"agent_id": "basalisk-test"}}
	var draft struct {
		ID    string `json:"item_id"`
		Token string `json:"version_token"`
	}
	if json.Unmarshal(call("POST", "/v1/workspace/write", writer.Token, workspaceCreate, 200), &draft) != nil || draft.ID == "" {
		t.Fatal("bad workspace receipt")
	}
	c, e := New(Config{Instance: "disposable-tess", Origin: proxy.URL, Principal: "tesseract-token/" + reader.ID, Namespaces: allowedNS}, func(context.Context) (string, error) { return reader.Token, nil }, proxy.Client())
	if e != nil {
		t.Fatal(e)
	}
	h, e := dataread.New(dataread.AuthorizeFunc(permit), nil, c)
	if e != nil {
		t.Fatal(e)
	}
	get := func(id, kind, mode, version string) dataread.GetRequest {
		r := req(kind, mode, version)
		r.Ref.Instance = c.config.Instance
		r.Ref.ID = id
		return r
	}
	for kind, ns := range map[string]string{"knowledge": knowledgeNS, "event": eventNS} {
		body := map[string]any{"namespace": ns, "summary": "Synthetic " + kind, "body": "# " + kind, "session_id": "disposable-test", "author": map[string]string{"agent_id": "basalisk-test"}}
		if kind == "knowledge" {
			body["kind"] = "doc"
			body["source"] = "manual"
			body["pointer"] = map[string]string{"scheme": "nil", "locator": "inline"}
		}
		var saved record
		if json.Unmarshal(call("POST", "/v1/"+kind+"/write", writer.Token, body, 200), &saved) != nil {
			t.Fatal("bad domain receipt")
		}
		for _, mode := range []string{"latest", "revision"} {
			version := ""
			if mode == "revision" {
				version = saved.Revision
			}
			got, e := h.Get(caller(), get(saved.ID, kind, mode, version))
			if e != nil || got.Item.Version.ID != saved.Revision || got.Item.BodyMarkdown != "# "+kind {
				t.Fatal("domain read", kind, mode, got, e)
			}
		}
		page, e := h.Query(caller(), dataread.QueryRequest{Source: c.source(), Mode: "recall", Recall: &dataread.RecallSelector{Namespace: ns, Kind: kind}})
		if e != nil || len(page.Items) != 1 || page.Items[0].Ref.ID != saved.ID {
			t.Fatal("domain recall", kind, page, e)
		}
	}
	current, e := h.Get(caller(), get(first.ID, "memory", "latest", ""))
	if e != nil || current.Item.Version.ID != second.Revision {
		call("GET", "/v1/items/"+first.ID, writer.Token, nil, 200)
		t.Fatal("current revision", current, e)
	}
	historical, e := h.Get(caller(), get(first.ID, "memory", "revision", first.Revision))
	if e != nil || historical.Item.BodyMarkdown != "# Visible first revision" {
		t.Fatal("exact revision", historical, e)
	}
	if _, e = h.Get(caller(), get(first.ID, "memory", "revision", private.Revision)); e != dataread.ErrVersion {
		t.Fatal("foreign revision association", e)
	}
	if _, e = h.Get(caller(), get(private.ID, "memory", "latest", "")); e != dataread.ErrAccess {
		t.Fatal("cross-namespace current read", e)
	}
	if _, e = h.Get(caller(), get(private.ID, "memory", "revision", private.Revision)); e != dataread.ErrAccess {
		t.Fatal("cross-namespace history read", e)
	}
	if _, e = h.Get(caller(), get(draft.ID, "workspace", "if_current", draft.Token)); e != nil {
		t.Fatal("workspace current token", e)
	}
	edit := map[string]any{"item_id": draft.ID, "version_token": draft.Token, "body": "# Workspace v2", "session_id": "disposable-test", "author": map[string]string{"agent_id": "basalisk-test"}}
	call("POST", "/v1/workspace/write", writer.Token, edit, 200)
	if got, e := h.Get(caller(), get(draft.ID, "workspace", "if_current", draft.Token)); e != dataread.ErrChanged || got.Item.BodyMarkdown != "" {
		t.Fatal("stale workspace fallback", got, e)
	}
	q := dataread.QueryRequest{Source: c.source(), Mode: "recall", Recall: &dataread.RecallSelector{Namespace: memoryNS, Kind: "memory"}, Limit: 13}
	seen := map[string]bool{}
	for pages := 0; pages < 10; pages++ {
		page, e := h.Query(caller(), q)
		if e != nil {
			t.Fatal("actual recall", e)
		}
		for _, it := range page.Items {
			if it.Namespace != memoryNS || seen[it.Ref.ID] {
				t.Fatal("recall leaked or duplicated", it.Ref)
			}
			seen[it.Ref.ID] = true
		}
		if page.NextCursor == "" {
			if !page.Exhausted || page.Coverage != "recall_candidates" {
				t.Fatal("wrong candidate coverage", page)
			}
			break
		}
		q.Cursor = page.NextCursor
	}
	if len(seen) != 52 {
		t.Fatal("actual paged recall lost rows", len(seen))
	}
	var scopedTotals struct {
		Manifest manifest `json:"manifest"`
	}
	if e = json.Unmarshal(call("POST", "/v1/tesseract/lookup", reader.Token, map[string]any{"namespaces": []string{memoryNS}, "domains": []string{"memory"}, "ranking": "chronological", "payload_mode": "full", "limit": 50}, 200), &scopedTotals); e != nil || scopedTotals.Manifest.Total != 52 {
		t.Fatal("foreign records affected scoped aggregate", scopedTotals, e)
	}
	q.Cursor = ""
	q.Recall = &dataread.RecallSelector{Namespace: memoryNS, Kind: "memory", Query: "Visible"}
	if page, e := h.Query(caller(), q); e != nil || len(page.Items) == 0 || page.Ranking != "relevance" {
		t.Fatal("lexical recall", page, e)
	}
	q.Recall = &dataread.RecallSelector{Namespace: workspaceNS, Kind: "workspace"}
	if page, e := h.Query(caller(), q); e != nil || len(page.Items) != 1 || page.Items[0].Version.Kind != "current_token" {
		t.Fatal("workspace recall", page, e)
	}
	// Source rejects unauthorized selectors before returning candidates/facets.
	for _, ns := range []string{privateNS, "project/*"} {
		call("POST", "/v1/tesseract/lookup", reader.Token, map[string]any{"namespaces": []string{ns}, "domains": []string{"memory"}, "payload_mode": "full", "limit": 50}, 403)
	}
	call("POST", "/v1/workspace/write", reader.Token, workspaceCreate, 403)
	// Confirm the observed direct-revision gap only with synthetic private data.
	// Accept a future fixed server too; the adapter never depends on this route.
	r, _ := http.NewRequest("GET", proxy.URL+"/v1/memory/revisions/"+private.Revision, nil)
	r.Header.Set("Authorization", "Bearer "+reader.Token)
	resp, e := proxy.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Log("Confirmed upstream direct revision namespace gap on disposable data; adapter avoids route")
	} else if resp.StatusCode != 403 && resp.StatusCode != 404 {
		t.Fatal("unexpected direct revision gate", resp.StatusCode)
	}
	// A credential resolver that has revoked enrollment must stop all reads,
	// including content still held in a host continuation buffer.
	q.Recall = &dataread.RecallSelector{Namespace: memoryNS, Kind: "memory"}
	q.Cursor = ""
	buffered, e := h.Query(caller(), q)
	if e != nil || buffered.NextCursor == "" {
		t.Fatal("missing buffered page", e)
	}
	c.token = func(context.Context) (string, error) { return "", dataread.ErrAccess }
	if _, e = h.Get(caller(), get(first.ID, "memory", "latest", "")); e != dataread.ErrAccess {
		t.Fatal("revoked enrollment", e)
	}
	q.Cursor = buffered.NextCursor
	if _, e = h.Query(caller(), q); e != dataread.ErrAccess {
		t.Fatal("revoked buffered read", e)
	}
	t.Log("Actual Tesseract verified namespace/read ceiling, current/history reads, workspace CAS and scoped paged recall; resolver revocation blocks buffered reads")
}
