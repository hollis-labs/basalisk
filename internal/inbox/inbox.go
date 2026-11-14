// Package inbox is the owner's inbox: messages and notifications addressed to him, read from
// wherever they are kept. A Provider is one such place; Tether messaging is the first
// (tether.go). Nothing is copied here: the provider's store is the record, and reading,
// archiving and answering are done there.
package inbox

import (
	"context"
	"errors"
	"sort"
	"time"
)

// Item is one message or notification.
type Item struct {
	// ID is the provider's id for it; Provider and Account say whose it is.
	ID       string `json:"id"`
	Provider string `json:"provider"`
	// Account is the address it was sent to (one of the owner's).
	Account  string `json:"account"`
	From     string `json:"from"`
	FromName string `json:"from_name,omitempty"`
	FromRole string `json:"from_role,omitempty"`
	// Kind is the provider's kind: request, response, notice, status_update, handoff, escalation.
	Kind      string    `json:"kind"`
	Subject   string    `json:"subject"`
	Body      string    `json:"body"`
	At        time.Time `json:"at"`
	Read      bool      `json:"read"`
	Archived  bool      `json:"archived"`
	Thread    string    `json:"thread,omitempty"`
	InReplyTo string    `json:"in_reply_to,omitempty"`
	// Ask is set when the sender wants an answer from a person (a question, a decision, an
	// approval), with the choices it offered.
	Ask *Ask `json:"ask,omitempty"`
}

// Ask is a typed request: what is wanted and the options offered.
type Ask struct {
	Kind    string   `json:"kind"`
	Re      string   `json:"re,omitempty"`
	Options []Option `json:"options,omitempty"`
	Default string   `json:"default,omitempty"`
	Due     string   `json:"due,omitempty"`
}

// Option is one choice of an Ask.
type Option struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Why         string `json:"why,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

// Filter narrows a listing.
type Filter struct {
	UnreadOnly bool
	Archived   bool // include archived items
	Limit      int  // per account
}

// Reply is the owner's answer to an item.
type Reply struct {
	Body string
	// Choice is the id of the option picked, when the item asked for one.
	Choice string
}

// Provider is one place messages come from.
type Provider interface {
	ID() string
	Title() string
	// Accounts are the owner's addresses there.
	Accounts() []string
	List(ctx context.Context, f Filter) ([]Item, error)
	// Unread counts what is unread and not archived.
	Unread(ctx context.Context) (int, error)
	MarkRead(ctx context.Context, account, id string) error
	Archive(ctx context.Context, account, id string, archived bool) error
	Reply(ctx context.Context, account, id string, r Reply) error
}

// ErrUnknown means no such provider, or an account that is not the owner's.
var ErrUnknown = errors.New("no such inbox")

// Inbox is the providers together.
type Inbox struct{ providers []Provider }

// New returns an inbox over the given providers.
func New(p ...Provider) *Inbox { return &Inbox{providers: p} }

// Source describes a provider in a listing, with the reason it could not be read, if so.
type Source struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Accounts []string `json:"accounts"`
	Error    string   `json:"error,omitempty"`
}

// List is every provider's items, newest first. A provider that fails is reported in its
// Source and the others still answer.
func (in *Inbox) List(ctx context.Context, f Filter) ([]Item, []Source) {
	items := []Item{}
	sources := make([]Source, 0, len(in.providers))
	for _, p := range in.providers {
		src := Source{ID: p.ID(), Title: p.Title(), Accounts: p.Accounts()}
		got, err := p.List(ctx, f)
		if err != nil {
			src.Error = err.Error()
		}
		items = append(items, got...)
		sources = append(sources, src)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].At.After(items[j].At) })
	return items, sources
}

// Unread is the count across providers; a provider that fails counts nothing.
func (in *Inbox) Unread(ctx context.Context) int {
	n := 0
	for _, p := range in.providers {
		if c, err := p.Unread(ctx); err == nil {
			n += c
		}
	}
	return n
}

// Provider finds one by id, checking the account is the owner's there.
func (in *Inbox) Provider(id, account string) (Provider, error) {
	for _, p := range in.providers {
		if p.ID() != id {
			continue
		}
		for _, a := range p.Accounts() {
			if a == account {
				return p, nil
			}
		}
	}
	return nil, ErrUnknown
}
