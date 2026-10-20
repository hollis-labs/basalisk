package assurance

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	jcs "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

func httpsOrigin(origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.String() == origin
}

func wireClient(client *http.Client) *http.Client {
	c := http.Client{}
	if client != nil {
		c = *client
	}
	c.Jar = nil
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if c.Timeout <= 0 || c.Timeout > 10*time.Second {
		c.Timeout = 10 * time.Second
	}
	return &c
}

// decodeWire retains nullable outcomes and opaque shared HITL members while
// rejecting duplicate names, casing aliases and unknown envelope fields.
func decodeWire(raw []byte, out any) error {
	if boundedJSON(raw) != nil {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return ErrInvalid
	}
	return sameWireJSON(raw, encoded)
}

func sameWireJSON(raw, encoded []byte) error {
	a, err := jcs.Transform(raw)
	if err != nil {
		return ErrInvalid
	}
	b, err := jcs.Transform(encoded)
	if err != nil || !bytes.Equal(a, b) {
		return ErrInvalid
	}
	return nil
}

func postWire(ctx context.Context, client *http.Client, endpoint, bearer string, tether bool, input, output any, limit int64) error {
	body, err := json.Marshal(input)
	if err != nil {
		return ErrInvalid
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ErrInvalid
	}
	r.Header.Set("Authorization", "Bearer "+bearer)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Cache-Control", "no-store")
	if tether {
		r.Header.Set("Tether-Protocol", "1")
	}
	resp, err := client.Do(r)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case 400:
			return ErrInvalid
		case 401, 403, 404:
			return ErrDenied
		case 409:
			return ErrConflict
		default:
			return ErrUnavailable
		}
	}
	if strings.Split(resp.Header.Get("Content-Type"), ";")[0] != "application/json" || resp.Header.Get("Cache-Control") != "no-store" {
		return ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(raw)) > limit || ctx.Err() != nil {
		return ErrUnavailable
	}
	return decodeWire(raw, output)
}
