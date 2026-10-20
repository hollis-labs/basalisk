// Package tesseractread consumes only namespace-checked public item/history and
// typed recall routes. It does not use the unrestricted revision-ID route,
// full views/evaluate, legacy recall, source DB access or source mutations.
package tesseractread

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/hollis-labs/basalisk/internal/dataread"
)

const Scope = "tesseract.read"

type TokenSource func(context.Context) (string, error)
type Config struct {
	Instance  string
	Origin    string
	Principal string
	// Exact concrete namespaces, also required as the credential's complete
	// namespace ceiling. Broad issuer-wide globs are deliberately not accepted.
	Namespaces []string
}
type Client struct {
	config Config
	token  TokenSource
	http   *http.Client
}

func (c *Client) ContinuationAuthority(ctx context.Context) (string, error) {
	token, e := c.credential(ctx)
	if e != nil {
		return "", e
	}
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:]), nil
}

func New(cfg Config, token TokenSource, client *http.Client) (*Client, error) {
	u, e := url.Parse(cfg.Origin)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || !dataread.ValidID(cfg.Instance) || cfg.Principal == "" || cfg.Principal == "static-token" || token == nil || len(cfg.Namespaces) == 0 || len(cfg.Namespaces) > 8 {
		return nil, dataread.ErrInvalid
	}
	cfg.Namespaces = slices.Clone(cfg.Namespaces)
	slices.Sort(cfg.Namespaces)
	for i, ns := range cfg.Namespaces {
		if namespaceKind(ns) == "" || i > 0 && ns == cfg.Namespaces[i-1] {
			return nil, dataread.ErrInvalid
		}
	}
	if client == nil {
		client = &http.Client{}
	}
	h := *client
	h.Timeout = 5 * time.Second
	h.Jar = nil
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return dataread.ErrUnavailable }
	return &Client{cfg, token, &h}, nil
}
func namespaceKind(ns string) string {
	parts := strings.Split(ns, "/")
	if len(parts) < 3 || len(ns) > 512 {
		return ""
	}
	for _, p := range parts {
		if !dataread.ValidID(p) {
			return ""
		}
	}
	i := 2
	if parts[0] == "system" {
		i = 1
	} else if !slices.Contains([]string{"project", "app", "org", "session", "user"}, parts[0]) {
		return ""
	}
	if len(parts) <= i+1 || !slices.Contains([]string{"memory", "knowledge", "event", "workspace"}, parts[i]) {
		return ""
	}
	return parts[i]
}
func (c *Client) source() dataread.Source {
	return dataread.Source{Provider: "tesseract", Instance: c.config.Instance}
}
func (c *Client) Describe() dataread.Descriptor {
	kinds := []string{}
	for _, ns := range c.config.Namespaces {
		kind := namespaceKind(ns)
		if !slices.Contains(kinds, kind) {
			kinds = append(kinds, kind)
		}
	}
	slices.Sort(kinds)
	versions := map[string][]string{}
	for _, kind := range kinds {
		versions[kind] = []string{"latest", "revision"}
		if kind == "workspace" {
			versions[kind] = []string{"latest", "if_current"}
		}
	}
	return dataread.Descriptor{Source: c.source(), AdapterVersion: "tesseract-read.v1", Kinds: kinds, VersionModes: []string{"latest", "revision", "if_current"}, KindVersionModes: versions, RevisionRequiresHistory: true, QueryModes: []string{"recall"}, Projection: dataread.TesseractProjection, MaxItems: dataread.MaxItems, Consistency: "best_effort_pagination", ReadEffects: []string{"current_memory_knowledge_workspace_reinforces_activation", "event_current_history_recall_do_not_reinforce", "if_current_mismatch_may_reinforce", "human_session_membership_unknown"}}
}

// Verify the source's effective claims with the same bearer used for the
// operation. This supports managed opaque credentials without inventing token
// parsing or trusting a callback's self-reported scopes. Enforce mode is required.
func (c *Client) credential(ctx context.Context) (string, error) {
	token, e := c.token(ctx)
	if e != nil || token == "" || len(token) > 16384 {
		return "", dataread.ErrAccess
	}
	var who struct {
		Mode       string   `json:"mode"`
		Verified   bool     `json:"verified"`
		Principal  string   `json:"principal"`
		Kind       string   `json:"kind"`
		Scopes     []string `json:"scopes"`
		Namespaces []string `json:"namespace_globs"`
	}
	if e = c.request(ctx, token, "GET", "/v1/auth/whoami", nil, &who); e != nil {
		return "", e
	}
	slices.Sort(who.Namespaces)
	if !who.Verified || who.Mode != "enforce" || who.Principal != c.config.Principal || who.Kind != "service" || len(who.Scopes) != 1 || who.Scopes[0] != Scope || !slices.Equal(who.Namespaces, c.config.Namespaces) {
		return "", dataread.ErrAccess
	}
	return token, nil
}
func (c *Client) request(ctx context.Context, token, method, path string, body any, out any) error {
	var encoded []byte
	var e error
	if body != nil {
		encoded, e = json.Marshal(body)
		if e != nil {
			return dataread.ErrInvalid
		}
	}
	r, e := http.NewRequestWithContext(ctx, method, c.config.Origin+path, bytes.NewReader(encoded))
	if e != nil {
		return dataread.ErrInvalid
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Accept", "application/json")
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	resp, e := c.http.Do(r)
	if e != nil {
		return dataread.ErrUnavailable
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 200:
	case 401, 403:
		return dataread.ErrAccess
	case 404, 410:
		return dataread.ErrNotFound
	case 400:
		return dataread.ErrInvalid
	default:
		return dataread.ErrUnavailable
	}
	if strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		return dataread.ErrUnavailable
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, dataread.MaxBytes+1))
	if e != nil {
		return dataread.ErrUnavailable
	}
	if len(b) > dataread.MaxBytes {
		return dataread.ErrLimit
	}
	if json.Unmarshal(b, out) != nil {
		return dataread.ErrUnavailable
	}
	return nil
}

// Deliberately exclude raw data, consumer_state, authors, pointers, provenance,
// arbitrary links and activation bookkeeping from the consumer text projection.
type record struct {
	ID        string `json:"item_id"`
	MemoryID  string `json:"memory_id"`
	Domain    string `json:"domain"`
	Namespace string `json:"namespace"`
	Revision  string `json:"revision_id"`
	Token     string `json:"version_token"`
	Key       string `json:"key"`
	MemoryKey string `json:"memory_key"`
	Payload   struct {
		Summary string `json:"summary"`
		Body    string `json:"body"`
	} `json:"payload"`
}

func (c *Client) project(r record) (dataread.Item, error) {
	if !slices.Contains(c.config.Namespaces, r.Namespace) {
		return dataread.Item{}, dataread.ErrAccess
	}
	if !dataread.ValidID(r.ID) || r.MemoryID != "" && r.MemoryID != r.ID || namespaceKind(r.Namespace) != r.Domain {
		return dataread.Item{}, dataread.ErrUnavailable
	}
	version := dataread.Version{Kind: "immutable_revision", ID: r.Revision}
	if r.Domain == "workspace" {
		if r.Revision != "" {
			return dataread.Item{}, dataread.ErrUnavailable
		}
		version = dataread.Version{Kind: "current_token", ID: r.Token}
	} else if r.Token != "" {
		return dataread.Item{}, dataread.ErrUnavailable
	}
	if !dataread.ValidID(version.ID) {
		return dataread.Item{}, dataread.ErrUnavailable
	}
	title := r.Key
	if title == "" {
		title = r.MemoryKey
	}
	if title == "" {
		title = r.Payload.Summary
	}
	return dataread.Item{Ref: dataread.Ref{Provider: "tesseract", Instance: c.config.Instance, Kind: r.Domain, ID: r.ID}, Version: version, ProjectionVersion: version.ID, Title: title, Summary: r.Payload.Summary, BodyMarkdown: r.Payload.Body, Namespace: r.Namespace, ContentTrust: "untrusted_source"}, nil
}
func (c *Client) Get(ctx context.Context, in dataread.GetRequest) (dataread.Item, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if in.Ref.Source() != c.source() || !dataread.ValidID(in.Ref.ID) || !slices.Contains(c.Describe().Kinds, in.Ref.Kind) {
		return dataread.Item{}, dataread.ErrInvalid
	}
	switch in.Version.Mode {
	case "latest":
		if in.Version.ID != "" || in.Version.Token != "" {
			return dataread.Item{}, dataread.ErrInvalid
		}
	case "revision":
		if in.Ref.Kind == "workspace" {
			return dataread.Item{}, dataread.ErrUnsupported
		}
		if !dataread.ValidID(in.Version.ID) || in.Version.Token != "" {
			return dataread.Item{}, dataread.ErrInvalid
		}
	case "if_current":
		if in.Ref.Kind != "workspace" {
			return dataread.Item{}, dataread.ErrUnsupported
		}
		if !dataread.ValidID(in.Version.Token) || in.Version.ID != "" {
			return dataread.Item{}, dataread.ErrInvalid
		}
	default:
		return dataread.Item{}, dataread.ErrUnsupported
	}
	token, e := c.credential(ctx)
	if e != nil {
		return dataread.Item{}, e
	}
	if in.Version.Mode == "revision" {
		return c.revision(ctx, token, in)
	}
	var r record
	if e = c.request(ctx, token, "GET", "/v1/items/"+in.Ref.ID, nil, &r); e != nil {
		return dataread.Item{}, e
	}
	it, e := c.project(r)
	if e != nil {
		return dataread.Item{}, e
	}
	if it.Ref != in.Ref {
		return dataread.Item{}, dataread.ErrUnavailable
	}
	if in.Version.Mode == "if_current" && it.Version.ID != in.Version.Token {
		return dataread.Item{}, dataread.ErrChanged
	}
	return it, nil
}

type manifest struct {
	Returned  int     `json:"results_returned"`
	Total     int     `json:"results_total"`
	Truncated bool    `json:"truncated"`
	Reason    string  `json:"truncation_reason"`
	Cursor    *string `json:"next_cursor"`
}

func (m manifest) cursor() string {
	if m.Cursor == nil {
		return ""
	}
	return *m.Cursor
}
func (m manifest) valid(n int) bool {
	return m.Returned == n && m.Total >= n && n <= dataread.MaxItems && len(m.cursor()) <= 8192 &&
		(!m.Truncated && m.Reason == "" && m.cursor() == "" || m.Truncated && slices.Contains([]string{"limit", "budget_bytes", "budget_tokens", "payload_mode_limit_cap"}, m.Reason))
}
func (c *Client) revision(ctx context.Context, token string, in dataread.GetRequest) (dataread.Item, error) {
	cursor := ""
	seen := map[string]bool{}
	// The item's namespace is checked by Tesseract before history content is
	// loaded. Never fall back to GET /memory/revisions/{id}, which lacks that guard.
	for page := 0; page < 4; page++ {
		q := url.Values{"limit": {"50"}, "budget_bytes": {"524288"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var raw struct {
			Results  []record  `json:"results"`
			Manifest *manifest `json:"manifest"`
		}
		if e := c.request(ctx, token, "GET", "/v1/items/"+in.Ref.ID+"/history?"+q.Encode(), nil, &raw); e != nil {
			return dataread.Item{}, e
		}
		if raw.Manifest == nil || raw.Results == nil || !raw.Manifest.valid(len(raw.Results)) {
			return dataread.Item{}, dataread.ErrUnavailable
		}
		var match *dataread.Item
		for _, r := range raw.Results {
			it, e := c.project(r)
			if e != nil {
				return dataread.Item{}, e
			}
			if it.Ref != in.Ref {
				return dataread.Item{}, dataread.ErrUnavailable
			}
			if it.Version.ID == in.Version.ID {
				match = &it
			}
		}
		if match != nil {
			return *match, nil
		}
		cursor = raw.Manifest.cursor()
		if cursor == "" {
			if raw.Manifest.Truncated {
				return dataread.Item{}, dataread.ErrLimit
			}
			return dataread.Item{}, dataread.ErrVersion
		}
		if seen[cursor] || len(raw.Results) == 0 {
			return dataread.Item{}, dataread.ErrLimit
		}
		seen[cursor] = true
	}
	return dataread.Item{}, dataread.ErrLimit
}
func (c *Client) Select(context.Context, string, string) (dataread.NativePage, error) {
	return dataread.NativePage{}, dataread.ErrUnsupported
}
func (c *Client) Recall(ctx context.Context, sel dataread.RecallSelector, cursor string) (dataread.NativePage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !slices.Contains(c.config.Namespaces, sel.Namespace) {
		return dataread.NativePage{}, dataread.ErrAccess
	}
	if namespaceKind(sel.Namespace) != sel.Kind || len(sel.Query) > 2000 || len(cursor) > 8192 {
		return dataread.NativePage{}, dataread.ErrInvalid
	}
	token, e := c.credential(ctx)
	if e != nil {
		return dataread.NativePage{}, e
	}
	ranking := "chronological"
	if strings.TrimSpace(sel.Query) != "" {
		ranking = "relevance"
	}
	request := map[string]any{"namespaces": []string{sel.Namespace}, "domains": []string{sel.Kind}, "ranking": ranking, "query": sel.Query, "revision_scope": "current", "payload_mode": "full", "limit": 50, "budget_bytes": 524288}
	if ranking == "relevance" {
		request["search_mode"] = "lexical"
	}
	if cursor != "" {
		request["cursor"] = cursor
	}
	var raw struct {
		Results []struct {
			Revision *record  `json:"revision"`
			Item     *record  `json:"item"`
			Score    *float64 `json:"score"`
		} `json:"results"`
		Manifest *manifest `json:"manifest"`
	}
	if e = c.request(ctx, token, "POST", "/v1/tesseract/lookup", request, &raw); e != nil {
		return dataread.NativePage{}, e
	}
	if raw.Manifest == nil || raw.Results == nil || !raw.Manifest.valid(len(raw.Results)) {
		return dataread.NativePage{}, dataread.ErrUnavailable
	}
	out := dataread.NativePage{Items: []dataread.Item{}, Cursor: raw.Manifest.cursor(), Truncated: raw.Manifest.Truncated, TruncationReason: raw.Manifest.Reason, Ranking: ranking}
	for _, r := range raw.Results {
		if (r.Revision == nil) == (r.Item == nil) {
			return dataread.NativePage{}, dataread.ErrUnavailable
		}
		rec := r.Revision
		if rec == nil {
			rec = r.Item
		}
		if rec.Namespace != sel.Namespace || rec.Domain != sel.Kind {
			return dataread.NativePage{}, dataread.ErrAccess
		}
		it, e := c.project(*rec)
		if e != nil {
			return dataread.NativePage{}, e
		}
		it.Score = r.Score
		out.Items = append(out.Items, it)
	}
	return out, nil
}
