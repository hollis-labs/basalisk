// Package gatecrashertest is a stand-in Gatecrasher for tests: the event stream, cards and
// decide, as api/openapi/gate-v1.yaml in hollis-labs/gatecrasher describes them.
package gatecrashertest

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Key is the owner credential the fake accepts.
const Key = "test-owner-key"

// Fake is a Gatecrasher with approvals the test adds.
type Fake struct {
	*httptest.Server

	mu        sync.Mutex
	cards     map[string]map[string]any
	events    []map[string]any
	changed   chan struct{}
	bodies    []string // every request body the fake received
	lastIDs   []string // the Last-Event-ID of each stream connection
	decisions []map[string]any
	dropAfter int // close a stream connection after this many events (0: never)

	// IntakeURL is the post_url a request_secret card carries.
	IntakeURL string
}

// New starts a fake on a free port.
func New() *Fake { return NewOn(nil) }

// NewOn starts a fake on l (a free port when nil).
func NewOn(l net.Listener) *Fake {
	f := &Fake{cards: map[string]map[string]any{}, changed: make(chan struct{}), IntakeURL: "http://127.0.0.1:1/intake"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/gate/v1/events", f.stream)
	mux.HandleFunc("GET /api/gate/v1/approvals/{id}/card", f.card)
	mux.HandleFunc("POST /api/gate/v1/approvals/{id}/decide", f.decide)
	srv := httptest.NewUnstartedServer(f.auth(mux))
	if l != nil {
		_ = srv.Listener.Close()
		srv.Listener = l
	}
	srv.Start()
	f.Server = srv
	return f
}

func (f *Fake) auth(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(strings.NewReader(string(raw)))
		f.mu.Lock()
		f.bodies = append(f.bodies, string(raw)+" "+r.URL.String())
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+Key {
			w.WriteHeader(401)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// DropAfter makes each stream connection close after n events.
func (f *Fake) DropAfter(n int) {
	f.mu.Lock()
	f.dropAfter = n
	f.mu.Unlock()
}

// Add puts a pending approval and its "submitted" event. typ is the card type (approve,
// choose, answer, request_secret).
func (f *Fake) Add(id, typ string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := map[string]any{
		"version": "gatecrasher.card/v1", "approval_id": id, "type": typ, "status": "pending",
		"tier": "critical", "kind": "credential_grant", "source": "warden", "title": "Test " + typ,
		"summary": "a test", "principal": map[string]any{"sub": "agt_test", "kind": "agent"},
		"evidence": map[string]any{}, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"deciders": "owner",
	}
	decide := "/api/gate/v1/approvals/" + id + "/decide"
	switch typ {
	case "request_secret":
		c["secret"] = map[string]any{"intake_id": "int_1", "post_url": f.IntakeURL, "field": "value", "write_only": true}
		c["actions"] = []any{
			map[string]any{"kind": "provide_secret", "label": "Provide"},
			map[string]any{"kind": "deny", "label": "Decline", "outcome": "denied", "decide": decide},
		}
	default:
		c["actions"] = []any{
			map[string]any{"kind": "approve", "label": "Approve", "outcome": "approved", "decide": decide},
			map[string]any{"kind": "deny", "label": "Deny", "outcome": "denied", "decide": decide},
		}
	}
	f.cards[id] = c
	f.event(id, "submitted", "pending")
}

// Settle closes an approval from elsewhere (another surface, or the producer withdrawing).
func (f *Fake) Settle(id, typ, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cards[id]["status"] = status
	delete(f.cards[id], "actions")
	f.event(id, typ, status)
}

// event appends to the log. Called with mu held.
func (f *Fake) event(id, typ, status string) {
	f.events = append(f.events, map[string]any{"seq": len(f.events) + 1, "approval_id": id, "type": typ, "status": status, "at": time.Now().UTC().Format(time.RFC3339)})
	close(f.changed)
	f.changed = make(chan struct{})
}

// Bodies is every request the fake received (body and URL), for canary checks.
func (f *Fake) Bodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.bodies...)
}

// LastEventIDs is the Last-Event-ID each stream connection carried.
func (f *Fake) LastEventIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.lastIDs...)
}

// Decisions is every decide body the fake accepted.
func (f *Fake) Decisions() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.decisions...)
}

func (f *Fake) stream(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.Atoi(r.Header.Get("Last-Event-ID"))
	f.mu.Lock()
	f.lastIDs = append(f.lastIDs, r.Header.Get("Last-Event-ID"))
	drop := f.dropAfter
	f.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	fl, _ := w.(http.Flusher)
	if fl != nil {
		fl.Flush()
	}
	sent := 0
	for {
		f.mu.Lock()
		evs := append([]map[string]any(nil), f.events...)
		ch := f.changed
		f.mu.Unlock()
		for _, ev := range evs {
			seq := ev["seq"].(int)
			if seq <= after {
				continue
			}
			raw, _ := json.Marshal(ev)
			_, _ = fmt.Fprintf(w, "id: %d\nevent: approval\ndata: %s\n\n", seq, raw)
			if fl != nil {
				fl.Flush()
			}
			after = seq
			sent++
			if drop > 0 && sent >= drop {
				return
			}
		}
		select {
		case <-ch:
		case <-r.Context().Done():
			return
		}
	}
}

func (f *Fake) card(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	c, ok := f.cards[r.PathValue("id")]
	var raw []byte
	if ok {
		raw, _ = json.Marshal(c)
	}
	f.mu.Unlock()
	if !ok {
		w.WriteHeader(404)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func (f *Fake) decide(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body map[string]any
	dec := json.NewDecoder(r.Body)
	if dec.Decode(&body) != nil {
		w.WriteHeader(400)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.cards[id]
	if !ok {
		w.WriteHeader(404)
		return
	}
	if c["status"] != "pending" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"code":"terminal","message":"Already decided"}`))
		return
	}
	f.decisions = append(f.decisions, body)
	c["status"] = body["outcome"]
	delete(c, "actions")
	c["outcome"] = map[string]any{"status": body["outcome"], "decider": map[string]any{"sub": "human:test", "kind": "human"}}
	f.event(id, "decided", body["outcome"].(string))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"approval_id": id, "status": body["outcome"], "decision": "x.y.z"})
}
