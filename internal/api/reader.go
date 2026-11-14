package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/basalisk/internal/feeds"
)

// The Reader (docs/reader.md): a read-it-later library and video player over Fragments
// Engine's reader API, plus RSS and Atom feeds kept here (internal/feeds). The library is
// FE's: these routes pass a fixed set of reader calls through to the owner's FE integration
// with the same credential as delivery. No request input chooses the host.

// EnableReader turns the feed routes on (the library routes need only an FE integration) and
// keeps the feeds fresh until ctx ends.
func (a *API) EnableReader(ctx context.Context, store *feeds.Store) {
	a.feeds = store
	a.feedClient = mediaClient(map[uint16]bool{80: true, 443: true})
	go func() {
		t := time.NewTicker(30 * time.Minute)
		defer t.Stop()
		for {
			a.refreshFeeds(ctx, 25*time.Minute)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

var feedsBusy atomic.Bool

// refreshFeeds fetches the feeds not fetched within `older`, one run at a time.
func (a *API) refreshFeeds(ctx context.Context, older time.Duration) {
	if a.feeds == nil || !feedsBusy.CompareAndSwap(false, true) {
		return
	}
	defer feedsBusy.Store(false)
	c, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := a.feeds.Refresh(c, a.feedClient, "", older); err != nil && ctx.Err() == nil {
		slog.Warn("reader: feeds were not refreshed", "err", err)
	}
}

func (a *API) readerRoutes() []route {
	r, w := ScopeRead, ScopeWrite
	rts := []route{
		{"GET", "/api/reader/items", r, a.readerList},
		{"GET", "/api/reader/items/{id}", r, a.readerItem},
		{"GET", "/api/reader/items/{id}/content", r, a.readerContent},
		{"POST", "/api/reader/items/{id}/commands", w, a.readerCommand},
		{"GET", "/api/reader/media/{variant}", r, a.readerMedia},
	}
	if a.feeds != nil {
		rts = append(rts,
			route{"GET", "/api/reader/feeds", r, a.feedList},
			route{"POST", "/api/reader/feeds", w, a.feedAdd},
			route{"PUT", "/api/reader/feeds/{id}", w, a.feedUpdate},
			route{"DELETE", "/api/reader/feeds/{id}", w, a.feedRemove},
			route{"POST", "/api/reader/feeds/refresh", w, a.feedRefresh},
			route{"POST", "/api/reader/feeds/read", w, a.feedReadAll},
			route{"GET", "/api/reader/entries", r, a.feedEntries},
			route{"GET", "/api/reader/entries/{id}", r, a.feedEntry},
			route{"PUT", "/api/reader/entries/{id}", w, a.feedMark},
		)
	}
	return rts
}

var (
	feID     = regexp.MustCompile(`^[0-9a-f]{16,128}$`)
	errNoFE  = errors.New("no Fragments Engine integration")
	feParams = []string{"scope", "cursor", "renderer", "tag", "state", "provider", "since", "until", "q", "later", "starred", "archived"}
)

// feDo sends one request to the owner's FE integration. path is built by the caller from
// checked ids; origin comes from the integration, never from the request.
func feDo(ctx context.Context, method, path string, body []byte, hdr map[string]string, timeout time.Duration) (*http.Response, error) {
	w := fragReader.Load()
	if w == nil {
		return nil, errNoFE
	}
	integration, ok := w.svc.ownerIntegration(ctx, w.config.IntegrationID)
	if !ok {
		return nil, errNoFE
	}
	var origin string
	if err := w.svc.db.QueryRowContext(ctx, `SELECT base_url FROM integrations WHERE id=?`, integration).Scan(&origin); err != nil || !fragmentOrigin(origin) {
		return nil, errNoFE
	}
	bearer, err := w.readBearer(ctx, integration)
	if err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(origin, "/")+path, rd) //nolint:gosec // origin is the owner's integration
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	for k, v := range hdr {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	hc := http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if t := w.httpTransport(); t != nil {
		hc.Transport = t
	}
	res, err := hc.Do(req) //nolint:gosec // see above
	if err != nil {
		return nil, errors.New("Fragments Engine could not be reached")
	}
	return res, nil
}

func readerFail(w http.ResponseWriter, err error) {
	if errors.Is(err, errNoFE) {
		fail(w, 503, "Add a Fragments Engine integration in Settings to use the library.")
		return
	}
	fail(w, 502, err.Error())
}

// feJSON passes FE's JSON answer through (its problem documents too, so the page can show
// why a filter or a command was refused).
func (a *API) feJSON(w http.ResponseWriter, r *http.Request, method, path string, body []byte) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	hdr := map[string]string{"Accept": "application/json"}
	if body != nil {
		hdr["Content-Type"] = "application/json"
	}
	res, err := feDo(r.Context(), method, path, body, hdr, 30*time.Second)
	if err != nil {
		readerFail(w, err)
		return
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		fail(w, 502, "Fragments Engine's answer could not be read.")
		return
	}
	status := res.StatusCode
	if status == 401 || status == 403 {
		status = 502 // not the owner's sign-in: FE refused Basalisk's credential
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func (a *API) readerList(w http.ResponseWriter, r *http.Request) {
	in, out := r.URL.Query(), url.Values{}
	for _, k := range feParams {
		for _, v := range in[k] {
			if v != "" && len(v) <= 8192 {
				out.Add(k, v)
			}
		}
	}
	if out.Get("scope") == "" {
		out.Set("scope", "library")
	}
	a.feJSON(w, r, "GET", "/v1/reader/items?"+out.Encode(), nil)
}

func (a *API) readerItem(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !feID.MatchString(id) {
		fail(w, 400, "That is not a fragment.")
		return
	}
	a.feJSON(w, r, "GET", "/v1/reader/items/"+id, nil)
}

// readerContent is an item's full text as Markdown (what FE keeps at rest), wrapped in JSON.
func (a *API) readerContent(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	id, rev := r.PathValue("id"), r.URL.Query().Get("revision_id")
	if !feID.MatchString(id) || (rev != "" && !feID.MatchString(rev)) {
		fail(w, 400, "That is not a fragment.")
		return
	}
	q := url.Values{"format": {"markdown"}}
	if rev != "" {
		q.Set("revision_id", rev)
	}
	res, err := feDo(r.Context(), "GET", "/v1/reader/items/"+id+"/content?"+q.Encode(), nil, map[string]string{"Accept": "text/markdown"}, 30*time.Second)
	if err != nil {
		readerFail(w, err)
		return
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		fail(w, 502, "The full text is not available.")
		return
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		fail(w, 502, "The full text could not be read.")
		return
	}
	reply(w, 200, map[string]string{"markdown": string(data)})
}

func (a *API) readerCommand(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !feID.MatchString(id) {
		fail(w, 400, "That is not a fragment.")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
	if err != nil || len(body) == 0 || len(body) >= 256<<10 {
		fail(w, 400, "The command is missing or too large.")
		return
	}
	a.feJSON(w, r, "POST", "/v1/reader/items/"+id+"/commands", body)
}

// mediaTypes are the kinds of file a reader item's media may be served as.
var mediaTypes = []string{"image/jpeg", "image/png", "image/gif", "image/webp", "video/mp4", "video/webm", "audio/mpeg", "audio/wav", "audio/x-wav", "audio/wave", "application/pdf", "text/vtt", "text/plain"}

// readerMedia streams one FE-held media file (an image, a mirrored video) with byte ranges,
// so a player can seek. Only the closed list of media types is passed on.
func (a *API) readerMedia(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	variant, q := r.PathValue("variant"), r.URL.Query()
	frag, rev := q.Get("fragment_id"), q.Get("revision_id")
	if !feID.MatchString(variant) || !feID.MatchString(frag) || !feID.MatchString(rev) {
		fail(w, 400, "That is not a media file.")
		return
	}
	path := "/v1/media/variants/" + variant + "/content?" + url.Values{"fragment_id": {frag}, "revision_id": {rev}}.Encode()
	res, err := feDo(r.Context(), "GET", path, nil, map[string]string{"Range": r.Header.Get("Range")}, 0)
	if err != nil {
		readerFail(w, err)
		return
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 && res.StatusCode != 206 {
		fail(w, 404, "That media is not available.")
		return
	}
	ctype := strings.ToLower(strings.TrimSpace(strings.Split(res.Header.Get("Content-Type"), ";")[0]))
	ok := false
	for _, t := range mediaTypes {
		ok = ok || t == ctype
	}
	if !ok {
		fail(w, 415, "That kind of media is not shown.")
		return
	}
	h := w.Header()
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if v := res.Header.Get(k); v != "" {
			h.Set(k, v)
		}
	}
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(res.StatusCode)
	_, _ = io.Copy(w, res.Body)
}

// ---- Feeds ----

func (a *API) feedOwner(w http.ResponseWriter, r *http.Request) bool {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return false
	}
	return true
}

func feedFail(w http.ResponseWriter, err error) {
	if errors.Is(err, feeds.ErrNotFound) {
		fail(w, 404, "There is no such feed or entry.")
		return
	}
	fail(w, 422, strings.ToUpper(err.Error()[:1])+err.Error()[1:]+".")
}

func (a *API) feedList(w http.ResponseWriter, r *http.Request) {
	if !a.feedOwner(w, r) {
		return
	}
	// Opening the reader looks for new entries, without waiting for them.
	go a.refreshFeeds(context.WithoutCancel(r.Context()), 15*time.Minute)
	list, err := a.feeds.Feeds(r.Context())
	if err != nil {
		fail(w, 500, "The feeds could not be read.")
		return
	}
	reply(w, 200, map[string]any{"feeds": list, "refreshing": feedsBusy.Load()})
}

func (a *API) feedAdd(w http.ResponseWriter, r *http.Request) {
	if !a.feedOwner(w, r) {
		return
	}
	var in struct {
		URL    string `json:"url"`
		Folder string `json:"folder"`
	}
	if !decodeLimit(w, r, &in, 16<<10) {
		return
	}
	if strings.TrimSpace(in.URL) == "" {
		fail(w, 400, "Give the feed's address, or the address of its site.")
		return
	}
	c, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	f, err := a.feeds.Add(c, a.feedClient, in.URL, in.Folder)
	if err != nil {
		feedFail(w, err)
		return
	}
	reply(w, 200, f)
}

func (a *API) feedUpdate(w http.ResponseWriter, r *http.Request) {
	if !a.feedOwner(w, r) {
		return
	}
	var in struct {
		Title  *string `json:"title"`
		Folder *string `json:"folder"`
	}
	if !decodeLimit(w, r, &in, 16<<10) {
		return
	}
	if err := a.feeds.Update(r.Context(), r.PathValue("id"), in.Title, in.Folder); err != nil {
		feedFail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (a *API) feedRemove(w http.ResponseWriter, r *http.Request) {
	if !a.feedOwner(w, r) {
		return
	}
	if err := a.feeds.Remove(r.Context(), r.PathValue("id")); err != nil {
		feedFail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (a *API) feedRefresh(w http.ResponseWriter, r *http.Request) {
	if !a.feedOwner(w, r) {
		return
	}
	var in struct {
		Feed string `json:"feed"`
	}
	if !decodeLimit(w, r, &in, 4<<10) {
		return
	}
	c, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	if err := a.feeds.Refresh(c, a.feedClient, in.Feed, 0); err != nil {
		feedFail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (a *API) feedReadAll(w http.ResponseWriter, r *http.Request) {
	if !a.feedOwner(w, r) {
		return
	}
	var in struct {
		Feed string `json:"feed"`
	}
	if !decodeLimit(w, r, &in, 4<<10) {
		return
	}
	if err := a.feeds.MarkAllRead(r.Context(), in.Feed); err != nil {
		fail(w, 500, "They could not be marked read.")
		return
	}
	w.WriteHeader(204)
}

func (a *API) feedEntries(w http.ResponseWriter, r *http.Request) {
	if !a.feedOwner(w, r) {
		return
	}
	q := r.URL.Query()
	list, err := a.feeds.Entries(r.Context(), feeds.Query{
		Feed: q.Get("feed"), Unread: q.Get("unread") == "1", Starred: q.Get("starred") == "1", Text: q.Get("q"), Before: q.Get("before"),
	})
	if err != nil {
		fail(w, 500, "The entries could not be read.")
		return
	}
	reply(w, 200, map[string]any{"entries": list})
}

func (a *API) feedEntry(w http.ResponseWriter, r *http.Request) {
	if !a.feedOwner(w, r) {
		return
	}
	e, err := a.feeds.Entry(r.Context(), r.PathValue("id"))
	if err != nil {
		feedFail(w, err)
		return
	}
	reply(w, 200, e)
}

func (a *API) feedMark(w http.ResponseWriter, r *http.Request) {
	if !a.feedOwner(w, r) {
		return
	}
	var in struct {
		Read    *bool `json:"read"`
		Starred *bool `json:"starred"`
		Saved   *bool `json:"saved"`
	}
	if !decodeLimit(w, r, &in, 4<<10) {
		return
	}
	if err := a.feeds.Mark(r.Context(), r.PathValue("id"), in.Read, in.Starred, in.Saved); err != nil {
		fail(w, 500, "The entry could not be changed.")
		return
	}
	w.WriteHeader(204)
}
