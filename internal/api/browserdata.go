package api

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The browser's own data, per person: the pages visited (for suggestions and the palette)
// and the zoom chosen for each site. One JSON document beside the desk settings, private to
// its owner, never logged.

const (
	maxHistory   = 5000
	maxURL       = 4096
	maxTitle     = 300
	revisitAfter = time.Minute // a visit to the same page sooner only updates its title
)

// visit is one page in the history.
type visit struct {
	URL   string    `json:"url"`
	Title string    `json:"title"`
	Last  time.Time `json:"last"`
	Count int       `json:"count"`
}

type browserData struct {
	Zoom    map[string]float64 `json:"zoom"`
	History []visit            `json:"history"`
}

// One lock for every person's file: writes are rare and small.
var browserDataMu sync.Mutex

func (a *API) browserDataRoutes() []route {
	r, w := ScopeRead, ScopeWrite
	return []route{
		{"GET", "/api/browser/prefs", r, a.browserPrefs},
		{"PUT", "/api/browser/prefs/zoom", w, a.browserSetZoom},
		{"GET", "/api/browser/history", r, a.browserHistory},
		{"POST", "/api/browser/history", w, a.browserVisit},
		{"DELETE", "/api/browser/history", w, a.browserForget},
	}
}

func (a *API) browserDataFile(r *http.Request) (string, error) {
	p, err := a.deskFile(r)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(p, ".json") + ".browser.json", nil
}

func readBrowserData(path string) (browserData, error) {
	d := browserData{Zoom: map[string]float64{}, History: []visit{}}
	b, err := os.ReadFile(path) //nolint:gosec // the path is derived from the verified caller
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return d, nil
	case err != nil:
		return d, err
	}
	if err = json.Unmarshal(b, &d); err != nil {
		// A damaged file starts over rather than locking the person out of the browser.
		return browserData{Zoom: map[string]float64{}, History: []visit{}}, nil //nolint:nilerr // see above
	}
	if d.Zoom == nil {
		d.Zoom = map[string]float64{}
	}
	return d, nil
}

func writeBrowserData(path string, d browserData) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	// path is derived from the verified caller's subject (deskFile), never from the request.
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil { //nolint:gosec // see above
		return err
	}
	tmp := path + ".tmp-" + rand.Text()[:8]
	if err = os.WriteFile(tmp, b, 0o600); err != nil { //nolint:gosec // see above
		return err
	}
	if err = os.Rename(tmp, path); err != nil { //nolint:gosec // see above
		_ = os.Remove(tmp) //nolint:gosec // see above
		return err
	}
	return nil
}

// updateBrowserData reads, changes and writes the caller's document under the lock.
func (a *API) updateBrowserData(w http.ResponseWriter, r *http.Request, change func(*browserData)) bool {
	path, err := a.browserDataFile(r)
	if err != nil {
		fail(w, 503, "Browser data is not kept by this server.")
		return false
	}
	browserDataMu.Lock()
	defer browserDataMu.Unlock()
	d, err := readBrowserData(path)
	if err == nil {
		change(&d)
		err = writeBrowserData(path, d)
	}
	if err != nil {
		fail(w, 500, "The browser data could not be saved.")
		return false
	}
	return true
}

func (a *API) loadBrowserData(w http.ResponseWriter, r *http.Request) (browserData, bool) {
	path, err := a.browserDataFile(r)
	if err != nil {
		return browserData{Zoom: map[string]float64{}, History: []visit{}}, true
	}
	browserDataMu.Lock()
	defer browserDataMu.Unlock()
	d, err := readBrowserData(path)
	if err != nil {
		fail(w, 500, "The browser data could not be read.")
		return d, false
	}
	return d, true
}

func (a *API) browserPrefs(w http.ResponseWriter, r *http.Request) {
	if d, ok := a.loadBrowserData(w, r); ok {
		reply(w, 200, map[string]any{"zoom": d.Zoom})
	}
}

func (a *API) browserSetZoom(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Host string  `json:"host"`
		Zoom float64 `json:"zoom"`
	}
	if !decode(w, r, &in) {
		return
	}
	host := strings.ToLower(strings.TrimSpace(in.Host))
	if host == "" || len(host) > 255 || in.Zoom < 0.25 || in.Zoom > 5 {
		fail(w, 400, "A host and a zoom between 0.25 and 5 are required.")
		return
	}
	if a.updateBrowserData(w, r, func(d *browserData) {
		if in.Zoom == 1 {
			delete(d.Zoom, host)
		} else {
			d.Zoom[host] = in.Zoom
		}
	}) {
		w.WriteHeader(204)
	}
}

// historyURL is the form a page is kept under: http(s) only, without its fragment.
func historyURL(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", false
	}
	u.Fragment, u.RawFragment = "", ""
	s := u.String()
	return s, len(s) <= maxURL
}

func (a *API) browserVisit(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, ok := historyURL(in.URL)
	if !ok {
		w.WriteHeader(204) // not a page worth remembering (file:, about:, data:...)
		return
	}
	title := strings.TrimSpace(in.Title)
	if r := []rune(title); len(r) > maxTitle {
		title = string(r[:maxTitle])
	}
	now := time.Now().UTC()
	if a.updateBrowserData(w, r, func(d *browserData) {
		for i := range d.History {
			v := &d.History[i]
			if v.URL != u {
				continue
			}
			if title != "" {
				v.Title = title
			}
			if now.Sub(v.Last) >= revisitAfter {
				v.Count++
			}
			v.Last = now
			return
		}
		d.History = append(d.History, visit{URL: u, Title: title, Last: now, Count: 1})
		if len(d.History) > maxHistory {
			sort.Slice(d.History, func(i, j int) bool { return d.History[i].Last.After(d.History[j].Last) })
			d.History = d.History[:maxHistory]
		}
	}) {
		w.WriteHeader(204)
	}
}

// browserHistory answers GET /api/browser/history?q=&limit=: with no query the most recent
// pages, else the pages whose address or title hold every word, best first.
func (a *API) browserHistory(w http.ResponseWriter, r *http.Request) {
	d, ok := a.loadBrowserData(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	reply(w, 200, map[string]any{"entries": rankHistory(d.History, r.URL.Query().Get("q"), limit, time.Now())})
}

func rankHistory(all []visit, q string, limit int, now time.Time) []visit {
	words := strings.Fields(strings.ToLower(q))
	type scored struct {
		v     visit
		score float64
	}
	var out []scored
	for _, v := range all {
		hay := strings.ToLower(v.URL + " " + v.Title)
		match := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		days := now.Sub(v.Last).Hours() / 24
		score := float64(v.Count)/(1+days/7) + 1/(1+days)
		if len(words) > 0 {
			host := strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(v.URL), "https://"), "http://")
			host = strings.TrimPrefix(host, "www.")
			if strings.HasPrefix(host, words[0]) {
				score += 10 // what the address field completes
			}
		}
		out = append(out, scored{v, score})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(words) == 0 {
			return out[i].v.Last.After(out[j].v.Last)
		}
		return out[i].score > out[j].score
	})
	res := make([]visit, 0, min(limit, len(out)))
	for i := 0; i < len(out) && i < limit; i++ {
		res = append(res, out[i].v)
	}
	return res
}

// browserForget removes one page (?url=) or, without one, the whole history.
func (a *API) browserForget(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("url")
	if a.updateBrowserData(w, r, func(d *browserData) {
		if target == "" {
			d.History = []visit{}
			return
		}
		u, _ := historyURL(target)
		kept := d.History[:0]
		for _, v := range d.History {
			if v.URL != u && v.URL != target {
				kept = append(kept, v)
			}
		}
		d.History = kept
	}) {
		w.WriteHeader(204)
	}
}
