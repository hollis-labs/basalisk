package inbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Caller is what the gateway client offers (internal/tetherlink).
type Caller interface {
	Configured() bool
	Call(ctx context.Context, tool string, args map[string]any) ([]byte, error)
}

// Tether reads the owner's Tether mailboxes through the gateway. Listing is the
// non-destructive tether_message_list; nothing is consumed.
type Tether struct {
	Link Caller
	// Addresses are the owner's addresses (msg://…). Every call is made as one of these and
	// never as an address a request names.
	Addresses []string

	mu    sync.Mutex
	names map[string]profile
}

type profile struct {
	name, role string
	at         time.Time
}

func (t *Tether) ID() string         { return "tether" }
func (t *Tether) Title() string      { return "Tether" }
func (t *Tether) Accounts() []string { return append([]string{}, t.Addresses...) }

type envelope struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	From       string          `json:"from"`
	To         string          `json:"to"`
	Thread     string          `json:"thread_id"`
	InReplyTo  string          `json:"in_reply_to"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
	ReadAt     *time.Time      `json:"read_at"`
	ArchivedAt *time.Time      `json:"archived_at"`
	Subject    string          `json:"subject"`
	Body       string          `json:"body"`
}

// maxBody bounds what one item carries to the page.
const maxBody = 256 << 10

func (t *Tether) item(e envelope) Item {
	it := Item{
		ID: e.ID, Provider: t.ID(), Account: e.To, From: e.From, Kind: e.Kind,
		Subject: e.Subject, Body: e.Body, At: e.CreatedAt,
		Read: e.ReadAt != nil, Archived: e.ArchivedAt != nil,
		Thread: e.Thread, InReplyTo: e.InReplyTo,
	}
	var p struct {
		Kind      string `json:"kind"`
		Subject   string `json:"subject"`
		Body      string `json:"body"`
		Text      string `json:"text"`
		Re        string `json:"re"`
		Attention string `json:"attention"`
		Options   []struct {
			ID          string `json:"id"`
			Label       string `json:"label"`
			Why         string `json:"why"`
			Recommended bool   `json:"recommended"`
		} `json:"options"`
		Default struct {
			Option string `json:"option"`
			At     string `json:"at"`
		} `json:"default"`
	}
	structured := json.Unmarshal(e.Payload, &p) == nil
	if structured {
		if it.Subject == "" {
			it.Subject = p.Subject
		}
		if it.Body == "" {
			it.Body = p.Body
		}
		if it.Body == "" {
			it.Body = p.Text
		}
		switch p.Kind {
		case "question", "decision", "approval_request":
			ask := &Ask{Kind: p.Kind, Re: p.Re, Default: p.Default.Option, Due: p.Default.At}
			for _, o := range p.Options {
				if o.ID != "" {
					ask.Options = append(ask.Options, Option{ID: o.ID, Label: o.Label, Why: o.Why, Recommended: o.Recommended})
				}
			}
			it.Ask = ask
		}
	}
	// A payload that is neither subject/body nor text is shown as it is.
	if it.Body == "" && len(e.Payload) > 0 && string(e.Payload) != "null" {
		var s string
		if json.Unmarshal(e.Payload, &s) == nil {
			it.Body = s
		} else {
			it.Body = "```json\n" + string(e.Payload) + "\n```"
		}
	}
	if len(it.Body) > maxBody {
		it.Body = it.Body[:maxBody] + "\n\n… (cut; the message is longer)"
	}
	if it.Subject == "" {
		line, _, _ := strings.Cut(strings.TrimSpace(it.Body), "\n")
		if len(line) > 120 {
			line = line[:120] + "…"
		}
		it.Subject = strings.TrimLeft(line, "# ")
	}
	return it
}

func (t *Tether) list(ctx context.Context, account string, args map[string]any) ([]envelope, int, error) {
	args["to"] = account
	raw, err := t.Link.Call(ctx, "tether_message_list", args)
	if err != nil {
		return nil, 0, err
	}
	var out struct {
		Messages []envelope `json:"messages"`
		Total    int        `json:"total"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, 0, fmt.Errorf("tether_message_list: %w", err)
	}
	return out.Messages, out.Total, nil
}

func (t *Tether) List(ctx context.Context, f Filter) ([]Item, error) {
	if !t.Link.Configured() {
		return nil, errors.New("no Tether gateway is configured")
	}
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	var items []Item
	var first error
	for _, account := range t.Addresses {
		msgs, _, err := t.list(ctx, account, map[string]any{
			"limit": limit, "unread_only": f.UnreadOnly, "include_archived": f.Archived,
		})
		if err != nil {
			if first == nil {
				first = fmt.Errorf("%s: %w", account, err)
			}
			continue
		}
		for _, e := range msgs {
			if e.To == "" {
				e.To = account
			}
			items = append(items, t.item(e))
		}
	}
	t.name(ctx, items)
	return items, first
}

func (t *Tether) Unread(ctx context.Context) (int, error) {
	if !t.Link.Configured() {
		return 0, errors.New("no Tether gateway is configured")
	}
	n := 0
	for _, account := range t.Addresses {
		msgs, total, err := t.list(ctx, account, map[string]any{"limit": 1, "unread_only": true})
		if err != nil {
			return n, err
		}
		if total < len(msgs) {
			total = len(msgs)
		}
		n += total
	}
	return n, nil
}

// name fills in who each sender is, from the registry, remembering the answers.
func (t *Tether) name(ctx context.Context, items []Item) {
	const keep = 30 * time.Minute
	t.mu.Lock()
	if t.names == nil {
		t.names = map[string]profile{}
	}
	t.mu.Unlock()
	asked := 0
	for i := range items {
		urn := items[i].From
		if urn == "" {
			continue
		}
		t.mu.Lock()
		p, ok := t.names[urn]
		t.mu.Unlock()
		if (!ok || time.Since(p.at) > keep) && asked < 40 {
			asked++
			p = profile{at: time.Now()}
			c, cancel := context.WithTimeout(ctx, 5*time.Second)
			raw, err := t.Link.Call(c, "tether_registry_lookup", map[string]any{"urn": urn})
			cancel()
			if err == nil {
				var out struct {
					Name    string `json:"display_name"`
					Role    string `json:"role"`
					Profile *struct {
						Name string `json:"display_name"`
						Role string `json:"role"`
					} `json:"profile"`
				}
				if json.Unmarshal(raw, &out) == nil {
					p.name, p.role = out.Name, out.Role
					if out.Profile != nil && p.name == "" {
						p.name, p.role = out.Profile.Name, out.Profile.Role
					}
				}
			}
			t.mu.Lock()
			t.names[urn] = p
			t.mu.Unlock()
		}
		items[i].FromName, items[i].FromRole = p.name, p.role
	}
}

func (t *Tether) MarkRead(ctx context.Context, account, id string) error {
	_, err := t.Link.Call(ctx, "tether_message_mark_read", map[string]any{"message_id": id, "as": account})
	return err
}

func (t *Tether) Archive(ctx context.Context, account, id string, archived bool) error {
	tool := "tether_message_archive"
	if !archived {
		tool = "tether_message_unarchive"
	}
	_, err := t.Link.Call(ctx, tool, map[string]any{"message_id": id, "as": account})
	return err
}

// Reply answers the sender of a message, in its thread, and wakes the sender's session when
// one is running. Who it goes to is read from the message itself, not from the request.
func (t *Tether) Reply(ctx context.Context, account, id string, r Reply) error {
	raw, err := t.Link.Call(ctx, "tether_message_get", map[string]any{"message_id": id, "as": account})
	if err != nil {
		return err
	}
	var got struct {
		Message *envelope `json:"message"`
		envelope
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return fmt.Errorf("tether_message_get: %w", err)
	}
	e := got.envelope
	if got.Message != nil {
		e = *got.Message
	}
	if e.ID == "" || e.From == "" {
		return errors.New("that message could not be read")
	}
	if e.To != "" && e.To != account {
		return ErrUnknown
	}
	orig := t.item(e)
	subject := orig.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "re: " + subject
	}
	payload := map[string]any{"subject": subject, "body": r.Body}
	if r.Choice != "" {
		payload["choice"] = r.Choice
		if orig.Ask != nil {
			for _, o := range orig.Ask.Options {
				if o.ID == r.Choice {
					payload["choice_label"] = o.Label
				}
			}
		}
	}
	body, _ := json.Marshal(payload)
	args := map[string]any{
		"from": account, "to": e.From, "kind": "response", "in_reply_to": e.ID, "payload_json": string(body),
	}
	if e.Thread != "" {
		args["thread_id"] = e.Thread
	}
	_, err = t.Link.Call(ctx, "tether_message_notify", args)
	return err
}
