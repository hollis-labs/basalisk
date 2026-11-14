package api

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/basalisk/internal/inbox"
)

// The inbox (internal/inbox): messages and notifications addressed to the owner, read from
// their providers (Tether messaging first). The account a request names must be one of the
// owner's configured addresses; a reply goes to the sender the message itself names.

// EnableInbox turns the inbox routes on.
func (a *API) EnableInbox(in *inbox.Inbox) { a.inbox = in }

func (a *API) inboxRoutes() []route {
	if a.inbox == nil {
		return nil
	}
	r, w := ScopeRead, ScopeWrite
	return []route{
		{"GET", "/api/inbox", r, a.inboxList},
		{"GET", "/api/inbox/unread", r, a.inboxUnread},
		{"POST", "/api/inbox/{provider}/{id}/read", w, a.inboxRead},
		{"POST", "/api/inbox/{provider}/{id}/archive", w, a.inboxArchive},
		{"POST", "/api/inbox/{provider}/{id}/reply", w, a.inboxReply},
	}
}

func (a *API) inboxList(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	q := r.URL.Query()
	items, sources := a.inbox.List(r.Context(), inbox.Filter{
		UnreadOnly: q.Get("unread") == "1", Archived: q.Get("archived") == "1",
	})
	unread := 0
	for _, it := range items {
		if !it.Read && !it.Archived {
			unread++
		}
	}
	reply(w, 200, map[string]any{"items": items, "sources": sources, "unread": unread})
}

// unreadCache keeps the bar's count from asking the gateway more than every few seconds.
var unreadCache struct {
	sync.Mutex
	n  int
	at time.Time
}

func (a *API) inboxUnread(w http.ResponseWriter, r *http.Request) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return
	}
	unreadCache.Lock()
	defer unreadCache.Unlock()
	if time.Since(unreadCache.at) > 10*time.Second || r.URL.Query().Get("fresh") == "1" {
		unreadCache.n, unreadCache.at = a.inbox.Unread(r.Context()), time.Now()
	}
	reply(w, 200, map[string]any{"unread": unreadCache.n})
}

// inboxTarget decodes the body and finds the provider for the caller's own account.
func (a *API) inboxTarget(w http.ResponseWriter, r *http.Request, in any, account func() string) (inbox.Provider, bool) {
	if _, err := a.ownerID(r); err != nil {
		fail(w, 401, "Please sign in.")
		return nil, false
	}
	if !decodeLimit(w, r, in, 256<<10) {
		return nil, false
	}
	p, err := a.inbox.Provider(r.PathValue("provider"), account())
	if err != nil {
		fail(w, 404, "There is no such inbox.")
		return nil, false
	}
	return p, true
}

func inboxFail(w http.ResponseWriter, err error) {
	if errors.Is(err, inbox.ErrUnknown) {
		fail(w, 404, "There is no such message in your inbox.")
		return
	}
	fail(w, 502, "The inbox's provider refused: "+err.Error())
}

func (a *API) inboxDone(w http.ResponseWriter, err error) {
	if err != nil {
		inboxFail(w, err)
		return
	}
	unreadCache.Lock()
	unreadCache.at = time.Time{}
	unreadCache.Unlock()
	w.WriteHeader(204)
}

func (a *API) inboxRead(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Account string `json:"account"`
	}
	p, ok := a.inboxTarget(w, r, &in, func() string { return in.Account })
	if !ok {
		return
	}
	a.inboxDone(w, p.MarkRead(r.Context(), in.Account, r.PathValue("id")))
}

func (a *API) inboxArchive(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Account  string `json:"account"`
		Archived *bool  `json:"archived"`
	}
	p, ok := a.inboxTarget(w, r, &in, func() string { return in.Account })
	if !ok {
		return
	}
	a.inboxDone(w, p.Archive(r.Context(), in.Account, r.PathValue("id"), in.Archived == nil || *in.Archived))
}

func (a *API) inboxReply(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Account string `json:"account"`
		Body    string `json:"body"`
		Choice  string `json:"choice"`
	}
	p, ok := a.inboxTarget(w, r, &in, func() string { return in.Account })
	if !ok {
		return
	}
	if strings.TrimSpace(in.Body) == "" && in.Choice == "" {
		fail(w, 400, "Write a reply, or pick an option.")
		return
	}
	id := r.PathValue("id")
	if err := p.Reply(r.Context(), in.Account, id, inbox.Reply{Body: in.Body, Choice: in.Choice}); err != nil {
		inboxFail(w, err)
		return
	}
	// Answered is read.
	_ = p.MarkRead(r.Context(), in.Account, id)
	a.inboxDone(w, nil)
}
