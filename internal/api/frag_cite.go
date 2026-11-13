package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/basalisk/internal/tetherlink"
)

// A saved fragment as a cite reference (kind "fragments.fragment"): read straight from FE's
// reader with the same credential as delivery (the exchanged read token, or in personal mode
// the integration's key), not through the Tether gateway.

// fragReader is the delivery worker of this process, which knows the mode and credential.
var fragReader atomic.Pointer[FragmentWorker]

var errFragmentMissing = errors.New("fragment not found")

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

// readBearer is the token that reads fragments.
func (w *FragmentWorker) readBearer(ctx context.Context, integration string) (string, error) {
	if !w.personal() {
		return w.client.ExchangeRead(ctx)
	}
	return w.bearer(ctx, deliveryJob{integration: integration})
}

func fragmentCite(ctx context.Context, _ *tetherlink.Client, id string, full bool) (*citeEntity, error) {
	w := fragReader.Load()
	if w == nil {
		return nil, errors.New("fragments are not configured")
	}
	integration, ok := w.svc.ownerIntegration(ctx, w.config.IntegrationID)
	if !ok {
		return nil, errors.New("no Fragments Engine integration")
	}
	var origin string
	if err := w.svc.db.QueryRowContext(ctx, `SELECT base_url FROM integrations WHERE id=?`, integration).Scan(&origin); err != nil || !fragmentOrigin(origin) {
		return nil, errors.New("no Fragments Engine integration")
	}
	bearer, err := w.readBearer(ctx, integration)
	if err != nil {
		return nil, err
	}
	// The origin is the owner's FE integration (an https base URL checked by fragmentOrigin) and
	// id matched ^[0-9a-f]{64}$ in citeKinds: no request input chooses the host.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(origin, "/")+"/v1/reader/items/"+id, nil) //nolint:gosec // see above
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/json")
	hc := http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if t := w.httpTransport(); t != nil {
		hc.Transport = t
	}
	res, err := hc.Do(req) //nolint:gosec // see above
	if err != nil {
		return nil, errors.New("FE could not be reached")
	}
	defer func() { _ = res.Body.Close() }()
	switch {
	case res.StatusCode == http.StatusNotFound:
		return nil, errFragmentMissing
	case res.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("FE answered %d", res.StatusCode) // never the body: it may echo the request
	}
	var item readerItem
	if json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&item) != nil || item.Schema != "fe.reader.item.v1" || item.ID != id {
		return nil, errors.New("FE answered with something other than a reader item")
	}
	title := strings.TrimSpace(item.Display.Title.Value)
	if title == "" {
		title = "Fragment " + id[:8]
	}
	e := &citeEntity{
		Title:       title,
		Subtitle:    "Fragment",
		Description: strings.TrimSpace(item.Display.Summary.Value),
		ChipLabel:   truncateRunes(title, 48),
		Fields: []citeField{
			{Label: "Fragment", Value: id[:12] + "…", Mono: true},
			{Label: "Revision", Value: fmt.Sprint(item.Revision)},
		},
	}
	if full {
		e.Body = item.Article.Preview
	}
	return e, nil
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
