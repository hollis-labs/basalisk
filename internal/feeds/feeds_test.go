package feeds

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

const rssDoc = `<?xml version="1.0"?><rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:dc="http://purl.org/dc/elements/1.1/"><channel>
<title>Example &amp; Co</title><link>https://example.test/</link>
<item><title>First post</title><link>https://example.test/1</link><guid>g1</guid><pubDate>Mon, 02 Nov 2026 10:00:00 +0000</pubDate><dc:creator>Ann</dc:creator>
<description><![CDATA[<p>Hello <b>world</b>.</p><script>alert(1)</script><p>Second &amp; last. <img src="https://example.test/a.png"></p>]]></description></item>
<item><title>Second</title><link>https://example.test/2</link><guid>g2</guid><pubDate>Tue, 03 Nov 2026 10:00:00 +0000</pubDate><content:encoded><![CDATA[<p>Full text</p>]]></content:encoded><description>short</description></item>
</channel></rss>`

const atomDoc = `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"><title>Atom feed</title><link rel="alternate" href="https://a.test/"/>
<entry><title>One</title><id>urn:1</id><link href="https://a.test/one"/><updated>2026-11-01T00:00:00Z</updated><author><name>Bo</name></author><summary>Sum</summary></entry></feed>`

func TestParse(t *testing.T) {
	p, err := Parse([]byte(rssDoc))
	if err != nil || p.Title != "Example & Co" || p.Site != "https://example.test/" || len(p.Entries) != 2 {
		t.Fatalf("%v %+v", err, p)
	}
	e := p.Entries[0]
	if e.Text != "Hello world.\n\nSecond & last." || e.Author != "Ann" || e.Image != "https://example.test/a.png" || e.Published.IsZero() {
		t.Fatalf("%+v", e)
	}
	if p.Entries[1].Text != "Full text" {
		t.Fatalf("content:encoded wins: %q", p.Entries[1].Text)
	}
	a, err := Parse([]byte(atomDoc))
	if err != nil || a.Title != "Atom feed" || len(a.Entries) != 1 || a.Entries[0].URL != "https://a.test/one" || a.Entries[0].Text != "Sum" {
		t.Fatalf("%v %+v", err, a)
	}
	if _, err := Parse([]byte("<html><body>no</body></html>")); err == nil {
		t.Fatal("a page is not a feed")
	}
}

func TestStore(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<html><head><link rel="alternate" type="application/rss+xml" href="/feed.xml"></head></html>`))
		case "/feed.xml":
			if r.Header.Get("If-None-Match") == `"v1"` {
				w.WriteHeader(304)
				return
			}
			w.Header().Set("ETag", `"v1"`)
			_, _ = w.Write([]byte(rssDoc))
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "feeds.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	f, err := s.Add(ctx, srv.Client(), srv.URL+"/", "news")
	if err != nil || f.Title != "Example & Co" {
		t.Fatalf("discovered from the page: %v %+v", err, f)
	}
	list, _ := s.Entries(ctx, Query{})
	if len(list) != 2 || list[0].Title != "Second" || list[0].Feed != "Example & Co" {
		t.Fatalf("%+v", list)
	}
	yes := true
	if err := s.Mark(ctx, list[0].ID, &yes, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Refresh(ctx, srv.Client(), f.ID, 0); err != nil {
		t.Fatal(err)
	}
	feeds, _ := s.Feeds(ctx)
	if len(feeds) != 1 || feeds[0].Unread != 1 || feeds[0].Error != "" {
		t.Fatalf("read state survives a refresh: %+v", feeds)
	}
	unread, _ := s.Entries(ctx, Query{Unread: true, Text: "hello"})
	if len(unread) != 1 || unread[0].Title != "First post" {
		t.Fatalf("%+v", unread)
	}
	if err := s.Remove(ctx, f.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.Entries(ctx, Query{}); len(list) != 0 {
		t.Fatal("entries go with their feed")
	}
}
