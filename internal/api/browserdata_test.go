package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/basalisk/internal/macshell"
)

type fakeBrowser struct{}

func (fakeBrowser) List() ([]macshell.BrowserView, error) { return nil, nil }
func (fakeBrowser) Command(map[string]any) (json.RawMessage, error) {
	return json.RawMessage("true"), nil
}
func (fakeBrowser) Snapshot(string) ([]byte, error) { return nil, nil }

func browserClient(t *testing.T) (*client, string) {
	t.Helper()
	s, _ := newStore(t)
	dir := t.TempDir()
	s.api.SetDeskDir(dir)
	s.api.EnableBrowser(fakeBrowser{})
	c := &client{t: t, handler: s.api.Handler()}
	c.signIn()
	return c, dir
}

func TestBrowserHistory(t *testing.T) {
	c, dir := browserClient(t)
	c.call("POST", "/api/browser/history", map[string]string{"url": "https://news.example.com/a#top", "title": "A story"}, 204)
	c.call("POST", "/api/browser/history", map[string]string{"url": "https://docs.example.org/guide", "title": "Guide"}, 204)
	c.call("POST", "/api/browser/history", map[string]string{"url": "file:///etc/hosts", "title": "no"}, 204)
	c.call("POST", "/api/browser/history", map[string]string{"url": "https://u:p@example.com/", "title": "no"}, 204)
	// The same page again within a minute updates its title but is one visit.
	c.call("POST", "/api/browser/history", map[string]string{"url": "https://news.example.com/a", "title": "A story, updated"}, 204)

	var got struct{ Entries []visit }
	if err := json.Unmarshal(c.call("GET", "/api/browser/history", nil, 200).Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("entries = %+v", got.Entries)
	}
	_ = json.Unmarshal(c.call("GET", "/api/browser/history?q=news", nil, 200).Body.Bytes(), &got)
	if len(got.Entries) != 1 || got.Entries[0].URL != "https://news.example.com/a" || got.Entries[0].Title != "A story, updated" || got.Entries[0].Count != 1 {
		t.Fatalf("q=news: %+v", got.Entries)
	}

	// Kept per person, private.
	files, _ := filepath.Glob(filepath.Join(dir, "*.browser.json"))
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	if fi, _ := os.Stat(files[0]); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode())
	}

	c.call("DELETE", "/api/browser/history?url="+"https://news.example.com/a", nil, 204)
	_ = json.Unmarshal(c.call("GET", "/api/browser/history", nil, 200).Body.Bytes(), &got)
	if len(got.Entries) != 1 || got.Entries[0].Title != "Guide" {
		t.Fatalf("after forget: %+v", got.Entries)
	}
	c.call("DELETE", "/api/browser/history", nil, 204)
	_ = json.Unmarshal(c.call("GET", "/api/browser/history", nil, 200).Body.Bytes(), &got)
	if len(got.Entries) != 0 {
		t.Fatalf("after clear: %+v", got.Entries)
	}
}

func TestBrowserZoom(t *testing.T) {
	c, _ := browserClient(t)
	c.call("PUT", "/api/browser/prefs/zoom", map[string]any{"host": "Example.com", "zoom": 1.25}, 204)
	c.call("PUT", "/api/browser/prefs/zoom", map[string]any{"host": "other.org", "zoom": 0.9}, 204)
	c.call("PUT", "/api/browser/prefs/zoom", map[string]any{"host": "other.org", "zoom": 1}, 204) // back to 100% forgets it
	c.call("PUT", "/api/browser/prefs/zoom", map[string]any{"host": "x.org", "zoom": 9}, 400)
	var got struct{ Zoom map[string]float64 }
	_ = json.Unmarshal(c.call("GET", "/api/browser/prefs", nil, 200).Body.Bytes(), &got)
	if len(got.Zoom) != 1 || got.Zoom["example.com"] != 1.25 {
		t.Fatalf("zoom = %v", got.Zoom)
	}
}

func TestRankHistory(t *testing.T) {
	now := time.Now()
	all := []visit{
		{URL: "https://old.example.com/", Title: "Old favourite", Last: now.Add(-60 * 24 * time.Hour), Count: 40},
		{URL: "https://github.com/x/y", Title: "Repo", Last: now.Add(-time.Hour), Count: 2},
		{URL: "https://example.com/github-notes", Title: "Notes on github", Last: now.Add(-time.Minute), Count: 1},
	}
	got := rankHistory(all, "git", 10, now)
	if len(got) != 2 || got[0].URL != "https://github.com/x/y" {
		t.Fatalf("a host that starts with the query comes first: %+v", got)
	}
	if got = rankHistory(all, "", 2, now); len(got) != 2 || got[0].URL != "https://example.com/github-notes" {
		t.Fatalf("no query is most recent first: %+v", got)
	}
}
