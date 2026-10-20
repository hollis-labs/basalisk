// Package feread consumes FE's public Reader contract. It never reads FE's DB,
// follows content URLs, issues credentials, or reuses capture/owner credentials.
package feread

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/basalisk/internal/dataread"
)

const Scope = "fragments-engine.read"

// TokenSource is supplied by a future, separate read enrollment. It must
// resolve current credentials and revocation state on each call. No production
// implementation or environment switch is installed by this foundation.
type TokenSource func(context.Context) (string, error)
type Client struct {
	source  dataread.Source
	origin  string
	subject string
	token   TokenSource
	http    *http.Client
}

func New(instance, origin, subject string, token TokenSource, client *http.Client) (*Client, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || !dataread.ValidID(instance) || subject == "" || token == nil {
		return nil, dataread.ErrInvalid
	}
	if client == nil {
		client = &http.Client{}
	}
	h := *client
	h.Timeout = 5 * time.Second
	h.Jar = nil
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return dataread.ErrUnavailable }
	return &Client{source: dataread.Source{Provider: "fragments-engine", Instance: instance}, origin: origin, subject: subject, token: token, http: &h}, nil
}
func (c *Client) Describe() dataread.Descriptor {
	// all is deliberately the only first-slice selection. Inbox/library and
	// personal overlays currently use FE's fixed local-user Reader principal.
	return dataread.Descriptor{Source: c.source, AdapterVersion: "fe-reader.v1", Kinds: []string{"fragment"}, VersionModes: []string{"latest", "revision"}, SelectionScopes: []string{"all"}, Projection: dataread.Projection, MaxItems: dataread.MaxItems, Consistency: "source_current_projection"}
}

// This is a credential-confusion guard, NOT JWT verification. FE verifies the
// signature, issuer, audience and revocation against its configured authority.
// Require exactly the narrow read scope and the separately configured subject.
func (c *Client) readCredential(raw string) bool {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || len(raw) > 16384 {
		return false
	}
	b, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return false
	}
	var claims struct {
		Subject  string `json:"sub"`
		Audience string `json:"aud"`
		Scope    string `json:"scope"`
		Kind     string `json:"kind"`
		Expires  int64  `json:"exp"`
	}
	if json.Unmarshal(b, &claims) != nil {
		return false
	}
	return claims.Subject == c.subject && claims.Audience == "fragments-engine" && claims.Scope == Scope && claims.Kind == "service" && claims.Expires > time.Now().Unix()
}
func (c *Client) request(ctx context.Context, path string, out any) error {
	token, err := c.token(ctx)
	if err != nil || !c.readCredential(token) {
		return dataread.ErrAccess
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+path, nil)
	if err != nil {
		return dataread.ErrInvalid
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return dataread.ErrUnavailable
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 200:
	case 401, 403:
		return dataread.ErrAccess
	case 404:
		return dataread.ErrNotFound
	default:
		return dataread.ErrUnavailable
	}
	if strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		return dataread.ErrUnavailable
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, dataread.MaxBytes+1))
	if err != nil {
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

// Decode only the projection we can justify. Do not return Reader actions,
// reading state, curated notes, annotations, tags, URLs or arbitrary metadata.
type readerItem struct {
	Schema     string `json:"schema_version"`
	ID         string `json:"fragment_id"`
	RevisionID string `json:"fragment_revision_id"`
	Revision   int    `json:"revision"`
	Display    struct {
		Title struct {
			Value string `json:"value"`
		} `json:"title"`
		Summary struct {
			Value string `json:"value"`
		} `json:"summary"`
	} `json:"display"`
	Article struct {
		Preview string `json:"preview_markdown"`
	} `json:"article"`
}

func (c *Client) project(in readerItem) (dataread.Item, error) {
	if in.Schema != "fe.reader.item.v1" || !dataread.ValidID(in.ID) || !dataread.ValidID(in.RevisionID) || in.Revision < 0 {
		return dataread.Item{}, dataread.ErrUnavailable
	}
	return dataread.Item{Ref: dataread.Ref{Provider: c.source.Provider, Instance: c.source.Instance, Kind: "fragment", ID: in.ID}, Version: dataread.Version{Kind: "immutable_revision", ID: in.RevisionID}, ProjectionVersion: strconv.Itoa(in.Revision), Title: in.Display.Title.Value, Summary: in.Display.Summary.Value, PreviewMarkdown: in.Article.Preview, ContentTrust: "untrusted_source"}, nil
}
func (c *Client) Get(ctx context.Context, in dataread.GetRequest) (dataread.Item, error) {
	if in.Ref.Source() != c.source || in.Ref.Kind != "fragment" || !dataread.ValidID(in.Ref.ID) {
		return dataread.Item{}, dataread.ErrInvalid
	}
	path := "/v1/reader/items/" + in.Ref.ID
	switch in.Version.Mode {
	case "latest":
		if in.Version.ID != "" {
			return dataread.Item{}, dataread.ErrInvalid
		}
	case "revision":
		if !dataread.ValidID(in.Version.ID) {
			return dataread.Item{}, dataread.ErrInvalid
		}
		path += "?revision_id=" + url.QueryEscape(in.Version.ID)
	default:
		return dataread.Item{}, dataread.ErrUnsupported
	}
	var raw readerItem
	err := c.request(ctx, path, &raw)
	if err != nil {
		if err == dataread.ErrNotFound && in.Version.Mode == "revision" {
			return dataread.Item{}, dataread.ErrVersion
		}
		return dataread.Item{}, err
	}
	out, err := c.project(raw)
	if err != nil {
		return out, err
	}
	if out.Ref != in.Ref {
		return dataread.Item{}, dataread.ErrUnavailable
	}
	if in.Version.Mode == "revision" && out.Version.ID != in.Version.ID {
		return dataread.Item{}, dataread.ErrVersion
	}
	return out, nil
}
func (c *Client) Select(ctx context.Context, scope, cursor string) (dataread.NativePage, error) {
	if scope != "all" {
		return dataread.NativePage{}, dataread.ErrUnsupported
	}
	if len(cursor) > 8192 {
		return dataread.NativePage{}, dataread.ErrInvalid
	}
	q := url.Values{"scope": {scope}}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var raw struct {
		Schema string       `json:"schema_version"`
		Scope  string       `json:"scope"`
		Items  []readerItem `json:"items"`
		Cursor string       `json:"next_cursor"`
	}
	if err := c.request(ctx, "/v1/reader/items?"+q.Encode(), &raw); err != nil {
		return dataread.NativePage{}, err
	}
	if raw.Schema != "fe.reader.list.v1" || raw.Scope != scope || raw.Items == nil || len(raw.Items) > dataread.MaxItems || len(raw.Cursor) > 8192 {
		return dataread.NativePage{}, dataread.ErrUnavailable
	}
	out := dataread.NativePage{Items: []dataread.Item{}, Cursor: raw.Cursor}
	for _, it := range raw.Items {
		v, err := c.project(it)
		if err != nil {
			return dataread.NativePage{}, err
		}
		out.Items = append(out.Items, v)
	}
	return out, nil
}
