package feeds

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // the database driver
)

// Feed is a feed the owner follows.
type Feed struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Title     string    `json:"title"`
	Site      string    `json:"site,omitempty"`
	Folder    string    `json:"folder,omitempty"`
	AddedAt   time.Time `json:"added_at"`
	FetchedAt time.Time `json:"fetched_at"`
	// Error is why the last fetch failed; empty when it worked.
	Error  string `json:"error,omitempty"`
	Unread int    `json:"unread"`
}

// Store keeps feeds and their entries.
type Store struct {
	db *sql.DB
	mu sync.Mutex // one refresh at a time
}

const schema = `
CREATE TABLE IF NOT EXISTS feeds (
  id TEXT PRIMARY KEY, url TEXT NOT NULL UNIQUE, title TEXT NOT NULL DEFAULT '', site TEXT NOT NULL DEFAULT '',
  folder TEXT NOT NULL DEFAULT '', added_at TEXT NOT NULL, fetched_at TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
  etag TEXT NOT NULL DEFAULT '', modified TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS entries (
  id TEXT PRIMARY KEY, feed_id TEXT NOT NULL REFERENCES feeds(id) ON DELETE CASCADE, guid TEXT NOT NULL,
  url TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '', author TEXT NOT NULL DEFAULT '', published TEXT NOT NULL,
  text TEXT NOT NULL DEFAULT '', image TEXT NOT NULL DEFAULT '', read INTEGER NOT NULL DEFAULT 0,
  starred INTEGER NOT NULL DEFAULT 0, saved INTEGER NOT NULL DEFAULT 0, UNIQUE(feed_id, guid));
CREATE INDEX IF NOT EXISTS entries_by_time ON entries(published DESC);
CREATE INDEX IF NOT EXISTS entries_by_feed ON entries(feed_id, published DESC);`

// Open opens (and creates) the store at path.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the store.
func (s *Store) Close() error { return s.db.Close() }

func hash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:10])
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }
func at(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// maxFeed is the largest feed document read; keep is how many entries a feed keeps (starred
// and saved ones are always kept).
const (
	maxFeed = 8 << 20
	keep    = 300
)

// ErrNotFound means there is no such feed or entry.
var ErrNotFound = errors.New("not found")

// get fetches an address with the given client (the caller's, which reaches public addresses
// only). It answers the body, or nil when the server says nothing changed.
func get(ctx context.Context, client *http.Client, raw, etag, modified string) (body []byte, final, newEtag, newModified, ctype string, err error) {
	u, perr := url.Parse(raw)
	if perr != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return nil, "", "", "", "", errors.New("that is not a web address")
	}
	req, rerr := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if rerr != nil {
		return nil, "", "", "", "", errors.New("that is not a web address")
	}
	req.Header.Set("User-Agent", "Basalisk-Reader/1 (feed reader)")
	req.Header.Set("Accept", "application/atom+xml, application/rss+xml, application/xml;q=0.9, text/xml;q=0.8, text/html;q=0.5, */*;q=0.1")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if modified != "" {
		req.Header.Set("If-Modified-Since", modified)
	}
	res, derr := client.Do(req)
	if derr != nil {
		return nil, "", "", "", "", errors.New("the feed could not be reached")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusNotModified {
		return nil, u.String(), etag, modified, "", nil
	}
	if res.StatusCode != 200 {
		return nil, "", "", "", "", fmt.Errorf("the feed answered %d", res.StatusCode)
	}
	b, rerr2 := io.ReadAll(io.LimitReader(res.Body, maxFeed+1))
	if rerr2 != nil {
		return nil, "", "", "", "", errors.New("the feed could not be read")
	}
	if len(b) > maxFeed {
		return nil, "", "", "", "", errors.New("the feed is too large")
	}
	return b, res.Request.URL.String(), res.Header.Get("ETag"), res.Header.Get("Last-Modified"), res.Header.Get("Content-Type"), nil
}

// Add follows a feed. The address may be the feed itself or a page that names one.
func (s *Store) Add(ctx context.Context, client *http.Client, raw, folder string) (Feed, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	body, final, etag, modified, _, err := get(ctx, client, raw, "", "")
	if err != nil {
		return Feed{}, err
	}
	parsed, perr := Parse(body)
	if perr != nil {
		// A page: look for the feed it names.
		href := Discover(body)
		if href == "" {
			return Feed{}, errors.New("no feed was found at that address")
		}
		base, _ := url.Parse(final)
		ref, rerr := url.Parse(href)
		if base == nil || rerr != nil {
			return Feed{}, errors.New("no feed was found at that address")
		}
		body, final, etag, modified, _, err = get(ctx, client, base.ResolveReference(ref).String(), "", "")
		if err != nil {
			return Feed{}, err
		}
		if parsed, perr = Parse(body); perr != nil {
			return Feed{}, perr
		}
	}
	f := Feed{ID: "feed_" + hash(final), URL: final, Title: parsed.Title, Site: parsed.Site, Folder: strings.TrimSpace(folder), AddedAt: time.Now().UTC(), FetchedAt: time.Now().UTC()}
	if f.Title == "" {
		if u, _ := url.Parse(final); u != nil {
			f.Title = u.Host
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO feeds(id,url,title,site,folder,added_at,fetched_at,etag,modified) VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET fetched_at=excluded.fetched_at, etag=excluded.etag, modified=excluded.modified, error=''`,
		f.ID, f.URL, f.Title, f.Site, f.Folder, ts(f.AddedAt), ts(f.FetchedAt), etag, modified); err != nil {
		return Feed{}, err
	}
	if err := s.merge(ctx, f.ID, parsed.Entries); err != nil {
		return Feed{}, err
	}
	return f, nil
}

// merge adds the entries a feed did not have and trims the oldest read ones.
func (s *Store) merge(ctx context.Context, feedID string, entries []Entry) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC()
	for _, e := range entries {
		guid := e.GUID
		if guid == "" {
			guid = e.URL
		}
		if guid == "" {
			guid = e.Title
		}
		if guid == "" {
			continue
		}
		if e.Published.IsZero() || e.Published.After(now.Add(24*time.Hour)) {
			e.Published = now
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO entries(id,feed_id,guid,url,title,author,published,text,image) VALUES(?,?,?,?,?,?,?,?,?)
			ON CONFLICT(feed_id,guid) DO UPDATE SET title=excluded.title, text=excluded.text, url=excluded.url, image=excluded.image, author=excluded.author`,
			"ent_"+hash(feedID, guid), feedID, guid, e.URL, e.Title, e.Author, ts(e.Published), e.Text, e.Image); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM entries WHERE feed_id=? AND starred=0 AND saved=0 AND id NOT IN
		(SELECT id FROM entries WHERE feed_id=? ORDER BY published DESC LIMIT ?)`, feedID, feedID, keep); err != nil {
		return err
	}
	return tx.Commit()
}

// Refresh fetches one feed again (id) or every feed not fetched within `older`.
func (s *Store) Refresh(ctx context.Context, client *http.Client, id string, older time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, args := `SELECT id,url,etag,modified,fetched_at FROM feeds`, []any{}
	if id != "" {
		q, args = q+` WHERE id=?`, []any{id}
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	type row struct{ id, url, etag, modified, fetched string }
	var list []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.id, &r.url, &r.etag, &r.modified, &r.fetched) == nil {
			list = append(list, r)
		}
	}
	_ = rows.Close()
	if id != "" && len(list) == 0 {
		return ErrNotFound
	}
	for _, r := range list {
		if id == "" && time.Since(at(r.fetched)) < older {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		body, _, etag, modified, _, err := get(c, client, r.url, r.etag, r.modified)
		cancel()
		msg := ""
		if err != nil {
			msg = err.Error()
		} else if body != nil {
			if parsed, perr := Parse(body); perr != nil {
				msg = perr.Error()
			} else if merr := s.merge(ctx, r.id, parsed.Entries); merr != nil {
				msg = "the entries could not be kept"
			}
		}
		if msg != "" {
			_, _ = s.db.ExecContext(ctx, `UPDATE feeds SET error=?, fetched_at=? WHERE id=?`, msg, ts(time.Now()), r.id)
			continue
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE feeds SET error='', fetched_at=?, etag=?, modified=? WHERE id=?`, ts(time.Now()), etag, modified, r.id)
	}
	return nil
}

// Feeds lists the feeds with their unread counts, by folder then title.
func (s *Store) Feeds(ctx context.Context) ([]Feed, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.id,f.url,f.title,f.site,f.folder,f.added_at,f.fetched_at,f.error,
		(SELECT COUNT(*) FROM entries e WHERE e.feed_id=f.id AND e.read=0) FROM feeds f ORDER BY f.folder, f.title COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Feed{}
	for rows.Next() {
		var f Feed
		var added, fetched string
		if err := rows.Scan(&f.ID, &f.URL, &f.Title, &f.Site, &f.Folder, &added, &fetched, &f.Error, &f.Unread); err != nil {
			return nil, err
		}
		f.AddedAt, f.FetchedAt = at(added), at(fetched)
		out = append(out, f)
	}
	return out, rows.Err()
}

// Update renames a feed or moves it to a folder (nil leaves a field as it is).
func (s *Store) Update(ctx context.Context, id string, title, folder *string) error {
	if title != nil && strings.TrimSpace(*title) != "" {
		if _, err := s.db.ExecContext(ctx, `UPDATE feeds SET title=? WHERE id=?`, strings.TrimSpace(*title), id); err != nil {
			return err
		}
	}
	if folder != nil {
		if _, err := s.db.ExecContext(ctx, `UPDATE feeds SET folder=? WHERE id=?`, strings.TrimSpace(*folder), id); err != nil {
			return err
		}
	}
	return nil
}

// Remove stops following a feed and forgets its entries.
func (s *Store) Remove(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM feeds WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Query narrows a listing of entries.
type Query struct {
	Feed    string
	Unread  bool
	Starred bool
	Text    string
	Before  string // the published time of the last entry of the previous page
	Limit   int
}

// Entries lists entries newest first.
func (s *Store) Entries(ctx context.Context, q Query) ([]Entry, error) {
	where, args := []string{"1=1"}, []any{}
	if q.Feed != "" {
		where, args = append(where, "e.feed_id=?"), append(args, q.Feed)
	}
	if q.Unread {
		where = append(where, "e.read=0")
	}
	if q.Starred {
		where = append(where, "e.starred=1")
	}
	if t := strings.TrimSpace(q.Text); t != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(t) + "%"
		where, args = append(where, `(e.title LIKE ? ESCAPE '\' OR e.text LIKE ? ESCAPE '\')`), append(args, like, like)
	}
	if q.Before != "" {
		where, args = append(where, "e.published < ?"), append(args, q.Before)
	}
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 60
	}
	args = append(args, q.Limit)
	rows, err := s.db.QueryContext(ctx, `SELECT e.id,e.feed_id,e.url,e.title,e.author,e.published,substr(e.text,1,600),e.image,e.read,e.starred,e.saved,f.title
		FROM entries e JOIN feeds f ON f.id=e.feed_id WHERE `+strings.Join(where, " AND ")+` ORDER BY e.published DESC, e.id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var pub string
		if err := rows.Scan(&e.ID, &e.FeedID, &e.URL, &e.Title, &e.Author, &pub, &e.Text, &e.Image, &e.Read, &e.Starred, &e.Saved, &e.Feed); err != nil {
			return nil, err
		}
		e.Published = at(pub)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Entry is one entry with its whole text.
func (s *Store) Entry(ctx context.Context, id string) (Entry, error) {
	var e Entry
	var pub string
	err := s.db.QueryRowContext(ctx, `SELECT e.id,e.feed_id,e.url,e.title,e.author,e.published,e.text,e.image,e.read,e.starred,e.saved,f.title
		FROM entries e JOIN feeds f ON f.id=e.feed_id WHERE e.id=?`, id).Scan(&e.ID, &e.FeedID, &e.URL, &e.Title, &e.Author, &pub, &e.Text, &e.Image, &e.Read, &e.Starred, &e.Saved, &e.Feed)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrNotFound
	}
	e.Published = at(pub)
	return e, err
}

// Mark sets an entry's flags (nil leaves one as it is).
func (s *Store) Mark(ctx context.Context, id string, read, starred, saved *bool) error {
	set := func(col string, v *bool) error {
		if v == nil {
			return nil
		}
		_, err := s.db.ExecContext(ctx, `UPDATE entries SET `+col+`=? WHERE id=?`, *v, id)
		return err
	}
	for col, v := range map[string]*bool{"read": read, "starred": starred, "saved": saved} {
		if err := set(col, v); err != nil {
			return err
		}
	}
	return nil
}

// MarkAllRead marks every entry of a feed (or of all feeds, with "") read.
func (s *Store) MarkAllRead(ctx context.Context, feed string) error {
	if feed == "" {
		_, err := s.db.ExecContext(ctx, `UPDATE entries SET read=1 WHERE read=0`)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE entries SET read=1 WHERE read=0 AND feed_id=?`, feed)
	return err
}
