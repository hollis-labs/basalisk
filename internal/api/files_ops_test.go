package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/basalisk/internal/files"
)

func filesClient(t *testing.T) (*client, string) {
	t.Helper()
	s, _ := newStore(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "dev", "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "dev", "a.md"), []byte("alpha"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := files.New(files.Config{Local: []files.RootConfig{{ID: "dev", Label: "dev", Path: "~/dev"}}}, home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	m.SetConfigFile(filepath.Join(home, "files.json"))
	s.api.EnableFiles(m)
	c := &client{t: t, handler: s.api.Handler()}
	c.signIn()
	return c, home
}

func ref(root, path string) map[string]string { return map[string]string{"root": root, "path": path} }

func TestFilesOpsRoutes(t *testing.T) {
	c, home := filesClient(t)
	dev := filepath.Join(home, "dev")

	c.call("POST", "/api/files/mkdir", ref("dev", "notes"), 201)
	c.call("POST", "/api/files/mkdir", ref("dev", "notes"), 409)
	c.call("POST", "/api/files/create", ref("dev", "notes/new.md"), 201)
	c.call("POST", "/api/files/create", ref("dev", "a.md"), 409)
	c.call("POST", "/api/files/mkdir", ref("dev", "../out"), 403)
	c.call("POST", "/api/files/create", ref("nope", "x"), 404)

	c.call("POST", "/api/files/move", map[string]any{"from": ref("dev", "a.md"), "to": ref("dev", "sub")}, 409)
	c.call("POST", "/api/files/move", map[string]any{"from": ref("dev", "a.md"), "to": ref("dev", "../a.md")}, 403)
	c.call("POST", "/api/files/move", map[string]any{"from": ref("dev", "sub"), "to": ref("dev", "sub/inner")}, 400)
	c.call("POST", "/api/files/move", map[string]any{"from": ref("dev", "a.md"), "to": ref("dev", "notes/a.md")}, 200)
	c.call("POST", "/api/files/copy", map[string]any{"from": ref("dev", "notes/a.md"), "to": ref("dev", "a copy.md")}, 201)
	c.call("POST", "/api/files/copy", map[string]any{"from": ref("dev", "notes/a.md"), "to": ref("dev", "a copy.md")}, 409)
	if b, _ := os.ReadFile(filepath.Join(dev, "a copy.md")); string(b) != "alpha" { //nolint:gosec // test temp dir
		t.Fatalf("copy: %q", b)
	}

	var del struct{ Trashed bool }
	_ = json.Unmarshal(c.call("POST", "/api/files/delete", ref("dev", "a copy.md"), 200).Body.Bytes(), &del)
	if !del.Trashed {
		t.Fatal("local delete did not go to the Trash")
	}
	if _, err := os.Stat(filepath.Join(home, ".Trash", "a copy.md")); err != nil {
		t.Fatal("not in the Trash")
	}
	c.call("POST", "/api/files/delete", ref("dev", "../a.md"), 403)
	c.call("POST", "/api/files/delete", ref("dev", "missing.md"), 404)

	var found files.SearchResult
	_ = json.Unmarshal(c.call("GET", "/api/files/search?root=dev&path=&q=new", nil, 200).Body.Bytes(), &found)
	if len(found.Entries) != 1 || found.Entries[0].Path != "notes/new.md" {
		t.Fatalf("search: %+v", found)
	}
	c.call("GET", "/api/files/search?root=dev&path=../&q=x", nil, 403)

	// Places: a remote is added and kept, then removed; a local place cannot be removed.
	c.call("GET", "/api/files/hosts", nil, 200)
	c.call("POST", "/api/files/roots", map[string]string{"host": "box", "path": "~/notes"}, 201)
	c.call("POST", "/api/files/roots", map[string]string{"host": "box", "path": "~/notes"}, 409)
	c.call("POST", "/api/files/roots", map[string]string{"host": "-oProxyCommand=x", "path": "/"}, 400)
	if b, _ := os.ReadFile(filepath.Join(home, "files.json")); !json.Valid(b) || len(b) == 0 { //nolint:gosec // test temp dir
		t.Fatalf("files.json: %s", b)
	}
	c.call("DELETE", "/api/files/roots/box-notes", nil, 204)
	c.call("DELETE", "/api/files/roots/dev", nil, 400)
	c.call("DELETE", "/api/files/roots/nope", nil, 404)
}
