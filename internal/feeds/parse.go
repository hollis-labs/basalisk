// Package feeds is the Reader's feed provider: RSS and Atom feeds the owner follows, their
// entries kept as plain text beside the app's database. An entry is not in the library until
// it is saved there; reading a feed copies nothing to Fragments Engine.
package feeds

import (
	"bytes"
	"encoding/xml"
	"errors"
	"html"
	"io"
	"regexp"
	"strings"
	"time"
)

// Parsed is a feed as fetched: its title, its site, its entries.
type Parsed struct {
	Title   string
	Site    string
	Entries []Entry
}

// Entry is one item of a feed.
type Entry struct {
	ID        string    `json:"id"`
	FeedID    string    `json:"feed_id"`
	GUID      string    `json:"-"`
	URL       string    `json:"url"`
	Title     string    `json:"title"`
	Author    string    `json:"author,omitempty"`
	Published time.Time `json:"published"`
	// Text is the entry's summary or content as plain text (markup removed).
	Text    string `json:"text"`
	Image   string `json:"image,omitempty"`
	Read    bool   `json:"read"`
	Starred bool   `json:"starred"`
	Saved   bool   `json:"saved"`
	Feed    string `json:"feed,omitempty"` // the feed's title, in listings
}

type rss struct {
	Channel struct {
		Title string `xml:"title"`
		Links []struct {
			Href string `xml:"href,attr"`
			Text string `xml:",chardata"`
		} `xml:"link"`
		Items []struct {
			Title     string `xml:"title"`
			Link      string `xml:"link"`
			GUID      string `xml:"guid"`
			PubDate   string `xml:"pubDate"`
			Date      string `xml:"date"`
			Author    string `xml:"author"`
			Creator   string `xml:"creator"`
			Desc      string `xml:"description"`
			Content   string `xml:"encoded"`
			Enclosure []struct {
				URL  string `xml:"url,attr"`
				Type string `xml:"type,attr"`
			} `xml:"enclosure"`
			Thumb []struct {
				URL string `xml:"url,attr"`
			} `xml:"thumbnail"`
		} `xml:"item"`
	} `xml:"channel"`
}

type atom struct {
	Title string `xml:"title"`
	Links []struct {
		Rel  string `xml:"rel,attr"`
		Href string `xml:"href,attr"`
		Type string `xml:"type,attr"`
	} `xml:"link"`
	Entries []struct {
		Title string `xml:"title"`
		ID    string `xml:"id"`
		Links []struct {
			Rel  string `xml:"rel,attr"`
			Href string `xml:"href,attr"`
		} `xml:"link"`
		Updated   string `xml:"updated"`
		Published string `xml:"published"`
		Author    struct {
			Name string `xml:"name"`
		} `xml:"author"`
		Summary string `xml:"summary"`
		Content string `xml:"content"`
		Group   struct {
			Desc  string `xml:"description"`
			Thumb []struct {
				URL string `xml:"url,attr"`
			} `xml:"thumbnail"`
		} `xml:"group"`
	} `xml:"entry"`
}

var dateLayouts = []string{
	time.RFC1123Z, time.RFC1123, time.RFC3339, time.RFC3339Nano, time.RFC822Z, time.RFC822,
	"Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST", "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02",
}

func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

var (
	reBlock = regexp.MustCompile(`(?i)<\s*(br|/p|/div|/li|/h[1-6]|/tr|/blockquote)[^>]*>`)
	reDrop  = regexp.MustCompile(`(?is)<\s*(script|style|iframe|svg|noscript)[^>]*>.*?<\s*/\s*(script|style|iframe|svg|noscript)\s*>`)
	reTag   = regexp.MustCompile(`(?s)<[^>]*>`)
	reSpace = regexp.MustCompile(`[ \t\r\f\v]+`)
	reLines = regexp.MustCompile(`\n\s*\n\s*(\n\s*)*`)
	reImg   = regexp.MustCompile(`(?i)<img[^>]+src\s*=\s*["']([^"']+)["']`)
)

// MaxText bounds an entry's kept text.
const MaxText = 64 << 10

// Plain is markup as plain text: tags removed, entities decoded, paragraphs kept apart.
func Plain(s string) string {
	s = reDrop.ReplaceAllString(s, "")
	s = reBlock.ReplaceAllString(s, "\n\n")
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = reSpace.ReplaceAllString(s, " ")
	s = reLines.ReplaceAllString(s, "\n\n")
	s = strings.TrimSpace(s)
	if len(s) > MaxText {
		s = s[:MaxText]
		for len(s) > 0 && s[len(s)-1]&0xC0 == 0x80 {
			s = s[:len(s)-1]
		}
		s += "…"
	}
	return s
}

func webURL(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
		return s
	}
	return ""
}

// Parse reads an RSS 2.0 (or RDF-like) or Atom document.
func Parse(data []byte) (Parsed, error) {
	dec := func(v any) error {
		d := xml.NewDecoder(bytes.NewReader(data))
		d.Strict = false
		d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) {
			return r, nil // read other charsets as they are: titles may show odd letters, nothing fails
		}
		return d.Decode(v)
	}
	var out Parsed
	var a atom
	if err := dec(&a); err == nil && len(a.Entries) > 0 {
		out.Title = Plain(a.Title)
		for _, l := range a.Links {
			if l.Rel == "" || l.Rel == "alternate" {
				out.Site = webURL(l.Href)
			}
		}
		for _, e := range a.Entries {
			en := Entry{GUID: strings.TrimSpace(e.ID), Title: Plain(e.Title), Author: Plain(e.Author.Name)}
			for _, l := range e.Links {
				if l.Rel == "" || l.Rel == "alternate" {
					en.URL = webURL(l.Href)
				}
			}
			en.Published = parseDate(e.Published)
			if en.Published.IsZero() {
				en.Published = parseDate(e.Updated)
			}
			body := e.Content
			if strings.TrimSpace(body) == "" {
				body = e.Summary
			}
			if strings.TrimSpace(body) == "" {
				body = e.Group.Desc
			}
			en.Text = Plain(body)
			if len(e.Group.Thumb) > 0 {
				en.Image = webURL(e.Group.Thumb[0].URL)
			} else if m := reImg.FindStringSubmatch(body); m != nil {
				en.Image = webURL(html.UnescapeString(m[1]))
			}
			out.Entries = append(out.Entries, en)
		}
		return out, nil
	}
	var r rss
	if err := dec(&r); err != nil {
		return out, errors.New("this is not an RSS or Atom feed")
	}
	if r.Channel.Title == "" && len(r.Channel.Items) == 0 {
		return out, errors.New("this is not an RSS or Atom feed")
	}
	out.Title = Plain(r.Channel.Title)
	for _, l := range r.Channel.Links {
		if u := webURL(l.Text); u != "" && out.Site == "" {
			out.Site = u
		}
	}
	for _, it := range r.Channel.Items {
		en := Entry{GUID: strings.TrimSpace(it.GUID), URL: webURL(it.Link), Title: Plain(it.Title)}
		en.Author = Plain(it.Creator)
		if en.Author == "" {
			en.Author = Plain(it.Author)
		}
		en.Published = parseDate(it.PubDate)
		if en.Published.IsZero() {
			en.Published = parseDate(it.Date)
		}
		body := it.Content
		if strings.TrimSpace(body) == "" {
			body = it.Desc
		}
		en.Text = Plain(body)
		for _, enc := range it.Enclosure {
			if strings.HasPrefix(enc.Type, "image/") {
				en.Image = webURL(enc.URL)
			}
		}
		if en.Image == "" && len(it.Thumb) > 0 {
			en.Image = webURL(it.Thumb[0].URL)
		}
		if en.Image == "" {
			if m := reImg.FindStringSubmatch(body); m != nil {
				en.Image = webURL(html.UnescapeString(m[1]))
			}
		}
		if en.URL == "" {
			en.URL = webURL(en.GUID)
		}
		out.Entries = append(out.Entries, en)
	}
	return out, nil
}

var reFeedLink = regexp.MustCompile(`(?i)<link\b[^>]*>`)
var reAttr = regexp.MustCompile(`(?i)\b(rel|type|href)\s*=\s*["']([^"']*)["']`)

// Discover finds a feed address in a web page's head (<link rel="alternate" type="application/rss+xml">).
func Discover(page []byte) string {
	if len(page) > 512<<10 {
		page = page[:512<<10]
	}
	for _, tag := range reFeedLink.FindAll(page, -1) {
		attrs := map[string]string{}
		for _, m := range reAttr.FindAllSubmatch(tag, -1) {
			attrs[strings.ToLower(string(m[1]))] = html.UnescapeString(string(m[2]))
		}
		t := strings.ToLower(attrs["type"])
		if strings.Contains(strings.ToLower(attrs["rel"]), "alternate") && (strings.Contains(t, "rss") || strings.Contains(t, "atom")) && attrs["href"] != "" {
			return attrs["href"]
		}
	}
	return ""
}
