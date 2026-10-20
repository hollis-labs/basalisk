package assurance

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RequestAuthentication supplies workload transport credentials through its
// supported lifecycle. It never receives a browser session or human proof.
// It may set Authorization, but cannot select another destination.
type RequestAuthentication interface {
	AuthenticateRequest(context.Context, *http.Request) error
}
type StatusClient struct {
	origin         string
	client         *http.Client
	authentication RequestAuthentication
}

func NewStatusClient(origin string, client *http.Client, authentication RequestAuthentication) (*StatusClient, error) {
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.String() != origin || authentication == nil {
		return nil, ErrInvalid
	}
	if client == nil {
		client = &http.Client{}
	}
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if c.Timeout <= 0 || c.Timeout > 10*time.Second {
		c.Timeout = 10 * time.Second
	}
	return &StatusClient{origin, &c, authentication}, nil
}
func (c *StatusClient) LookupStatus(ctx context.Context, id string) (CurrentStatus, error) {
	if c == nil || !validID(id) {
		return CurrentStatus{}, ErrInvalid
	}
	query := rand.Text()
	raw, _ := json.Marshal(map[string]string{"completion_id": id, "query_id": query})
	req, e := http.NewRequestWithContext(ctx, "POST", c.origin+"/assurance/v1/status", bytes.NewReader(raw))
	if e != nil {
		return CurrentStatus{}, e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cache-Control", "no-store")
	if e = c.authentication.AuthenticateRequest(ctx, req); e != nil {
		return CurrentStatus{}, ErrUnavailable
	}
	if req.Method != "POST" || req.URL.String() != c.origin+"/assurance/v1/status" {
		return CurrentStatus{}, ErrDenied
	}
	resp, e := c.client.Do(req)
	if e != nil {
		return CurrentStatus{}, ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 || strings.Split(resp.Header.Get("Content-Type"), ";")[0] != "application/json" || resp.Header.Get("Cache-Control") != "no-store" {
		return CurrentStatus{}, ErrUnavailable
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if e != nil || len(data) > 64<<10 || boundedJSON(data) != nil {
		return CurrentStatus{}, ErrInvalid
	}
	var out CurrentStatus
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&out) != nil || dec.Decode(new(any)) != io.EOF || out.QueryID != query || out.Completion.ID != id {
		return CurrentStatus{}, ErrDenied
	}
	return out, nil
}
