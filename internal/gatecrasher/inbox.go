package gatecrasher

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

// Paths on Gatecrasher (api/openapi/gate-v1.yaml in hollis-labs/gatecrasher).
const (
	pathEvents    = "/api/gate/v1/events"
	pathApprovals = "/api/gate/v1/approvals/"
)

// keepDecided is how long a decided card stays, so the owner sees what became of it.
const keepDecided = 15 * time.Minute

// Event is one entry of Gatecrasher's event stream. Status is the approval's status now.
type Event struct {
	Seq        int64  `json:"seq"`
	ApprovalID string `json:"approval_id"`
	Type       string `json:"type"`
	Status     string `json:"status"`
}

// Card is one approval as the page shows it: Gatecrasher's card, kept as it came.
type Card struct {
	ID      string          `json:"id"`
	Status  string          `json:"status"`
	Updated time.Time       `json:"updated"`
	Card    json.RawMessage `json:"card"`
}

// Waiting reports whether the card still waits on the owner.
func (c Card) Waiting() bool { return c.Status == "pending" }

// state is what is kept between runs: where the stream is, and the cards.
type state struct {
	Cursor int64           `json:"cursor"`
	Cards  map[string]Card `json:"cards"`
}

// Inbox follows Gatecrasher's stream and keeps the cards. It is safe for concurrent use.
type Inbox struct {
	cfg  Config
	http *http.Client
	// stream is the client for the event stream: no overall timeout.
	stream    *http.Client
	statePath string
	log       *slog.Logger
	now       func() time.Time

	mu      sync.Mutex
	st      state
	version int64
	changed chan struct{}
	online  bool
}

// New makes an inbox. statePath is the file the cursor and cards are kept in.
func New(cfg Config, statePath string, log *slog.Logger) *Inbox {
	if log == nil {
		log = slog.Default()
	}
	in := &Inbox{
		cfg:       cfg,
		http:      &http.Client{Timeout: 20 * time.Second},
		stream:    &http.Client{},
		statePath: statePath,
		log:       log,
		now:       time.Now,
		st:        state{Cards: map[string]Card{}},
		changed:   make(chan struct{}),
	}
	in.load()
	return in
}

func (in *Inbox) load() {
	raw, err := os.ReadFile(in.statePath)
	if err != nil {
		return
	}
	var st state
	if json.Unmarshal(raw, &st) == nil && st.Cards != nil {
		in.st = st
	}
}

// save writes the state file (0600, atomically). Called with mu held.
func (in *Inbox) save() {
	raw, err := json.Marshal(in.st)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(in.statePath), ".gatecrasher-state-*")
	if err != nil {
		in.log.Warn("gatecrasher: the state could not be kept", "err", err)
		return
	}
	_ = tmp.Chmod(0o600)
	_, err = tmp.Write(raw)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), in.statePath)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		in.log.Warn("gatecrasher: the state could not be kept", "err", err)
	}
}

// bump marks a change and wakes whoever waits on one. Called with mu held.
func (in *Inbox) bump() {
	in.version++
	close(in.changed)
	in.changed = make(chan struct{})
}

// List is the cards, waiting ones first, then decided ones, newest first in each, and the
// version they are at.
func (in *Inbox) List() ([]Card, int64, bool) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.prune()
	out := make([]Card, 0, len(in.st.Cards))
	for _, c := range in.st.Cards {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Waiting() != out[j].Waiting() {
			return out[i].Waiting()
		}
		return out[i].Updated.After(out[j].Updated)
	})
	return out, in.version, in.online
}

// Wait returns when the cards are past version, ctx ends, or d passes.
func (in *Inbox) Wait(ctx context.Context, version int64, d time.Duration) {
	in.mu.Lock()
	if in.version != version {
		in.mu.Unlock()
		return
	}
	ch := in.changed
	in.mu.Unlock()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ch:
	case <-ctx.Done():
	case <-t.C:
	}
}

// prune drops decided cards past keepDecided. Called with mu held.
func (in *Inbox) prune() {
	for id, c := range in.st.Cards {
		if !c.Waiting() && in.now().Sub(c.Updated) > keepDecided {
			delete(in.st.Cards, id)
		}
	}
}

func (in *Inbox) setOnline(on bool) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.online != on {
		in.online = on
		in.bump()
	}
}

// key reads the owner's credential. It is read on each use, so a rotated key is picked up.
func (in *Inbox) key() (string, error) {
	raw, err := os.ReadFile(in.cfg.KeyFile)
	if err != nil {
		return "", fmt.Errorf("gatecrasher: the owner credential could not be read: %w", err)
	}
	k := strings.TrimSpace(string(raw))
	if k == "" {
		return "", errors.New("gatecrasher: the owner credential file is empty")
	}
	return k, nil
}

func (in *Inbox) request(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	k, err := in.key()
	if err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	r, err := http.NewRequestWithContext(ctx, method, in.cfg.URL+path, rd)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+k)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	return r, nil
}

// Run follows the event stream until ctx ends, reconnecting with backoff and resuming
// from the last event handled.
func (in *Inbox) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		start := in.now()
		err := in.follow(ctx)
		in.setOnline(false)
		if ctx.Err() != nil {
			return
		}
		if in.now().Sub(start) > time.Minute {
			backoff = time.Second
		}
		in.log.Warn("gatecrasher: the event stream ended; reconnecting", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

// follow reads the stream once, from the cursor, until it ends.
func (in *Inbox) follow(ctx context.Context) error {
	in.mu.Lock()
	cursor := in.st.Cursor
	in.mu.Unlock()
	r, err := in.request(ctx, http.MethodGet, pathEvents, nil)
	if err != nil {
		return err
	}
	r.Header.Set("Accept", "text/event-stream")
	if cursor > 0 {
		r.Header.Set("Last-Event-ID", strconv.FormatInt(cursor, 10))
	}
	res, err := in.stream.Do(r)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("events: %s", res.Status)
	}
	in.setOnline(true)
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var data strings.Builder
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if data.Len() > 0 {
				var ev Event
				if err := json.Unmarshal([]byte(data.String()), &ev); err == nil {
					if err = in.handle(ctx, ev); err != nil {
						return err
					}
				}
				data.Reset()
			}
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return io.EOF
}

// handle applies one event: the approval's card is read again and kept. An event already
// handled (its seq at or below the cursor) is skipped.
func (in *Inbox) handle(ctx context.Context, ev Event) error {
	in.mu.Lock()
	if ev.Seq <= in.st.Cursor || ev.ApprovalID == "" {
		in.mu.Unlock()
		return nil
	}
	_, known := in.st.Cards[ev.ApprovalID]
	in.mu.Unlock()
	// A finished approval the owner never saw needs no card (a stream read from the start).
	if ev.Status != "pending" && !known {
		in.advance(ev.Seq, "", nil)
		return nil
	}
	card, status, err := in.fetchCard(ctx, ev.ApprovalID)
	switch {
	case errors.Is(err, errGone):
		in.advance(ev.Seq, ev.ApprovalID, nil)
	case err != nil:
		// The cursor stays: the stream is read again from here after a reconnect.
		return fmt.Errorf("card %s: %w", ev.ApprovalID, err)
	default:
		in.advance(ev.Seq, ev.ApprovalID, &Card{ID: ev.ApprovalID, Status: status, Updated: in.now(), Card: card})
	}
	return nil
}

// advance moves the cursor and puts (or, with card nil and id set, removes) a card.
func (in *Inbox) advance(seq int64, id string, card *Card) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if seq > in.st.Cursor {
		in.st.Cursor = seq
	}
	switch {
	case card != nil:
		in.st.Cards[id] = *card
	case id != "":
		delete(in.st.Cards, id)
	}
	in.save()
	in.bump()
}

var errGone = errors.New("gatecrasher: no such approval")

func (in *Inbox) fetchCard(ctx context.Context, id string) (json.RawMessage, string, error) {
	r, err := in.request(ctx, http.MethodGet, pathApprovals+url.PathEscape(id)+"/card", nil)
	if err != nil {
		return nil, "", err
	}
	res, err := in.http.Do(r)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, "", err
	}
	if res.StatusCode == http.StatusNotFound {
		return nil, "", errGone
	}
	if res.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("card: %s", res.Status)
	}
	var head struct {
		Status string `json:"status"`
	}
	if err = json.Unmarshal(raw, &head); err != nil {
		return nil, "", fmt.Errorf("card: %w", err)
	}
	return raw, head.Status, nil
}

// Refresh reads one card again.
func (in *Inbox) Refresh(ctx context.Context, id string) (Card, error) {
	card, status, err := in.fetchCard(ctx, id)
	if errors.Is(err, errGone) {
		in.mu.Lock()
		delete(in.st.Cards, id)
		in.save()
		in.bump()
		in.mu.Unlock()
		return Card{}, err
	}
	if err != nil {
		return Card{}, err
	}
	c := Card{ID: id, Status: status, Updated: in.now(), Card: card}
	in.mu.Lock()
	in.st.Cards[id] = c
	in.save()
	in.bump()
	in.mu.Unlock()
	return c, nil
}

// Decision is what the owner decided on a card. It is checked field by field: anything
// else in the body (a secret value above all) is refused, never passed on.
type Decision struct {
	Outcome  string          `json:"outcome"`
	Choice   string          `json:"choice,omitempty"`
	Answer   string          `json:"answer,omitempty"`
	Note     string          `json:"note,omitempty"`
	Narrowed json.RawMessage `json:"narrowed,omitempty"`
}

// Check reports whether the decision is one Gatecrasher can take. Decode it with unknown
// fields refused, so nothing else in a body is passed on.
func (d Decision) Check() error {
	if d.Outcome != "approved" && d.Outcome != "denied" {
		return errors.New(`outcome must be "approved" or "denied"`)
	}
	return nil
}

// DecideError is Gatecrasher's refusal of a decision.
type DecideError struct {
	Status  int
	Code    string
	Message string
}

func (e *DecideError) Error() string { return e.Message }

// Decide sends the owner's decision and returns the card as it is afterwards.
func (in *Inbox) Decide(ctx context.Context, id string, d Decision) (Card, error) {
	in.mu.Lock()
	c, ok := in.st.Cards[id]
	in.mu.Unlock()
	if !ok {
		return Card{}, &DecideError{Status: http.StatusNotFound, Code: "not_found", Message: "No such approval"}
	}
	var head struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(c.Card, &head)
	if head.Type == "request_secret" && d.Outcome == "approved" {
		return Card{}, &DecideError{Status: http.StatusBadRequest, Code: "invalid", Message: "A secret is provided with the card's form, not approved here"}
	}
	body, err := json.Marshal(d)
	if err != nil {
		return Card{}, err
	}
	r, err := in.request(ctx, http.MethodPost, pathApprovals+url.PathEscape(id)+"/decide", body)
	if err != nil {
		return Card{}, err
	}
	res, err := in.http.Do(r)
	if err != nil {
		return Card{}, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusAccepted {
		var e struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Message == "" {
			e.Message = "Gatecrasher refused the decision (" + res.Status + ")"
		}
		// Someone else decided first, or it ended: show the card as it is now.
		if res.StatusCode == http.StatusConflict {
			_, _ = in.Refresh(ctx, id)
		}
		return Card{}, &DecideError{Status: res.StatusCode, Code: e.Code, Message: e.Message}
	}
	return in.Refresh(ctx, id)
}
