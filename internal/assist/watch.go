package assist

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/basalisk/internal/inbox"
)

// The watcher sorts the owner's new mail so most of it never reaches them. Sorting is by
// rule, not by a model: the owner's own rules first, then what the message is.
//
//	needs   it asks the owner to decide (a question, a decision, an approval) or escalates:
//	        shown to them, with its options.
//	bas     it asks for something without offering options: handed to the assistant, in
//	        the background, to answer, settle or bring to the owner.
//	digest  it tells: held, counted, shown when asked for.
//	settle  the owner's rule says this needs nothing: marked read, on the record.
//
// What was already waiting when the watcher first ran is not sorted: it is left as it was.

// Classes of mail.
const (
	ClassNeeds  = "needs"
	ClassBas    = "bas"
	ClassDigest = "digest"
	ClassSettle = "settle"
)

// Mail is a message the watcher has sorted.
type Mail struct {
	ID       string     `json:"id"`
	Account  string     `json:"account"`
	Provider string     `json:"provider"`
	From     string     `json:"from"`
	FromName string     `json:"from_name,omitempty"`
	Kind     string     `json:"kind"`
	Subject  string     `json:"subject"`
	Start    string     `json:"start"`
	At       time.Time  `json:"at"`
	Class    string     `json:"class"`
	Ask      *inbox.Ask `json:"ask,omitempty"`
	// Why says which rule sorted it, when one of the owner's did.
	Why string `json:"why,omitempty"`
}

// Rule is one of the owner's: mail that matches goes to Class. Empty fields match anything.
type Rule struct {
	ID         int64  `json:"id"`
	From       string `json:"from,omitempty"`
	Kind       string `json:"kind,omitempty"`
	SubjectHas string `json:"subject_has,omitempty"`
	Class      string `json:"class"`
	Note       string `json:"note,omitempty"`
}

func (r Rule) matches(it inbox.Item) bool {
	return (r.From == "" || r.From == it.From) && (r.Kind == "" || r.Kind == it.Kind) &&
		(r.SubjectHas == "" || strings.Contains(strings.ToLower(it.Subject), strings.ToLower(r.SubjectHas)))
}

// Classify sorts one message: the first of the owner's rules that matches, else by what it is.
func Classify(it inbox.Item, rules []Rule) (class, why string) {
	for _, r := range rules {
		if r.matches(it) {
			return r.Class, fmt.Sprintf("your rule %d", r.ID)
		}
	}
	switch {
	case it.Ask != nil, it.Kind == "escalation":
		return ClassNeeds, ""
	case it.Kind == "request":
		return ClassBas, ""
	default:
		return ClassDigest, ""
	}
}

const watchSchema = `
CREATE TABLE IF NOT EXISTS mail (
  id TEXT PRIMARY KEY, account TEXT NOT NULL, provider TEXT NOT NULL, from_addr TEXT NOT NULL, from_name TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL, subject TEXT NOT NULL, start TEXT NOT NULL DEFAULT '', at TEXT NOT NULL, class TEXT NOT NULL,
  ask TEXT NOT NULL DEFAULT '', why TEXT NOT NULL DEFAULT '', state TEXT NOT NULL DEFAULT 'open', seen_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS mail_open ON mail(state, class);
CREATE TABLE IF NOT EXISTS rules (
  id INTEGER PRIMARY KEY AUTOINCREMENT, from_addr TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL DEFAULT '',
  subject_has TEXT NOT NULL DEFAULT '', class TEXT NOT NULL, note TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);`

// Watcher sorts the owner's mail as it arrives.
type Watcher struct {
	Mail *inbox.Inbox
	Log  *Log
	// Hand starts the assistant on a batch of mail that asks for something (class bas). It
	// may be nil: such mail is then shown to the owner like any that needs them.
	Hand func(ctx context.Context, batch []Mail)

	mu       sync.Mutex
	lastHand time.Time
}

// Init makes the watcher's tables. Call once before Run.
func (w *Watcher) Init(ctx context.Context) error {
	_, err := w.Log.db.ExecContext(ctx, watchSchema)
	return err
}

// Rules are the owner's rules, in the order they are tried (newest first).
func (w *Watcher) Rules(ctx context.Context) ([]Rule, error) {
	rows, err := w.Log.db.QueryContext(ctx, `SELECT id,from_addr,kind,subject_has,class,note FROM rules ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Rule{}
	for rows.Next() {
		var r Rule
		if err := rows.Scan(&r.ID, &r.From, &r.Kind, &r.SubjectHas, &r.Class, &r.Note); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddRule adds one of the owner's rules.
func (w *Watcher) AddRule(ctx context.Context, r Rule) (Rule, error) {
	switch r.Class {
	case ClassNeeds, ClassBas, ClassDigest, ClassSettle:
	default:
		return r, errors.New("a rule sends mail to needs, bas, digest or settle")
	}
	if r.From == "" && r.Kind == "" && r.SubjectHas == "" {
		return r, errors.New("a rule must say what it matches: a sender, a kind or words in the subject")
	}
	res, err := w.Log.db.ExecContext(ctx, `INSERT INTO rules(from_addr,kind,subject_has,class,note,created_at) VALUES(?,?,?,?,?,?)`,
		r.From, r.Kind, r.SubjectHas, r.Class, r.Note, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return r, err
	}
	r.ID, _ = res.LastInsertId()
	return r, nil
}

// DropRule removes a rule.
func (w *Watcher) DropRule(ctx context.Context, id int64) error {
	_, err := w.Log.db.ExecContext(ctx, `DELETE FROM rules WHERE id=?`, id)
	return err
}

// Open is the sorted mail still waiting, of a class, newest first.
func (w *Watcher) Open(ctx context.Context, class string) ([]Mail, error) {
	rows, err := w.Log.db.QueryContext(ctx, `SELECT id,account,provider,from_addr,from_name,kind,subject,start,at,class,ask,why FROM mail
		WHERE state='open' AND class=? ORDER BY at DESC LIMIT 200`, class)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Mail{}
	for rows.Next() {
		var m Mail
		var at, ask string
		if err := rows.Scan(&m.ID, &m.Account, &m.Provider, &m.From, &m.FromName, &m.Kind, &m.Subject, &m.Start, &at, &m.Class, &ask, &m.Why); err != nil {
			return nil, err
		}
		m.At, _ = time.Parse(time.RFC3339, at)
		if ask != "" {
			_ = json.Unmarshal([]byte(ask), &m.Ask)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetState moves a sorted message on: "done" (answered or read), "dismissed" (not now), or
// back to "open". With class set it is re-sorted too (the owner said where it belongs).
func (w *Watcher) SetState(ctx context.Context, id, state, class string) error {
	if class != "" {
		_, err := w.Log.db.ExecContext(ctx, `UPDATE mail SET state=?, class=? WHERE id=?`, state, class, id)
		return err
	}
	_, err := w.Log.db.ExecContext(ctx, `UPDATE mail SET state=? WHERE id=?`, state, id)
	return err
}

// Sweep sorts what is new and closes what is no longer unread. It returns how many were new.
func (w *Watcher) Sweep(ctx context.Context) (int, error) {
	items, sources := w.Mail.List(ctx, inbox.Filter{UnreadOnly: true, Limit: 100})
	for _, s := range sources {
		if s.Error != "" {
			return 0, errors.New(s.Error) // a partial listing would close mail that is still unread
		}
	}
	db := w.Log.db
	var known int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mail`).Scan(&known); err != nil {
		return 0, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	unread := map[string]bool{}
	rules, _ := w.Rules(ctx)
	var hand []Mail
	added := 0
	for _, it := range items {
		unread[it.ID] = true
		var have int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mail WHERE id=?`, it.ID).Scan(&have); err != nil || have > 0 {
			continue
		}
		class, why := Classify(it, rules)
		state := "open"
		if known == 0 {
			// The first sweep: what was already waiting is left as the owner had it.
			class, state = "old", "dismissed"
		}
		if class == ClassBas && w.Hand == nil {
			class = ClassNeeds
		}
		ask := ""
		if it.Ask != nil {
			raw, _ := json.Marshal(it.Ask)
			ask = string(raw)
		}
		start := it.Body
		if len(start) > 600 {
			start = start[:600]
			for len(start) > 0 && start[len(start)-1]&0xC0 == 0x80 {
				start = start[:len(start)-1]
			}
			start += "…"
		}
		m := Mail{ID: it.ID, Account: it.Account, Provider: it.Provider, From: it.From, FromName: it.FromName, Kind: it.Kind, Subject: it.Subject, Start: start, At: it.At, Class: class, Ask: it.Ask, Why: why}
		if class == ClassSettle {
			// The owner's rule: needs nothing. On the record, then marked read.
			if err := w.Log.Add(ctx, Entry{Kind: "settle", Summary: fmt.Sprintf("Settled “%s” from %s by %s", m.Subject, nameOr(m), why), With: m.From, Ref: m.ID}); err == nil {
				if p, perr := w.Mail.Provider(it.Provider, it.Account); perr == nil {
					_ = p.MarkRead(ctx, it.Account, it.ID)
				}
				state = "done"
			} else {
				class, m.Class = ClassDigest, ClassDigest
			}
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO mail(id,account,provider,from_addr,from_name,kind,subject,start,at,class,ask,why,state,seen_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			m.ID, m.Account, m.Provider, m.From, m.FromName, m.Kind, m.Subject, m.Start, m.At.UTC().Format(time.RFC3339), class, ask, why, state, now); err != nil {
			return added, err
		}
		if state == "open" {
			added++
			if class == ClassBas {
				hand = append(hand, m)
			}
		}
	}
	// What was waiting and is no longer unread has been dealt with, here or elsewhere.
	rows, err := db.QueryContext(ctx, `SELECT id FROM mail WHERE state='open'`)
	if err == nil {
		var gone []string
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil && !unread[id] {
				gone = append(gone, id)
			}
		}
		_ = rows.Close()
		for _, id := range gone {
			_, _ = db.ExecContext(ctx, `UPDATE mail SET state='done' WHERE id=?`, id)
		}
	}
	if len(hand) > 0 && w.Hand != nil {
		w.mu.Lock()
		ok := time.Since(w.lastHand) > 5*time.Minute
		if ok {
			w.lastHand = time.Now()
		}
		w.mu.Unlock()
		if ok {
			if len(hand) > 12 {
				hand = hand[:12]
			}
			w.Hand(ctx, hand)
		}
	}
	return added, nil
}

func nameOr(m Mail) string {
	if m.FromName != "" {
		return m.FromName
	}
	return m.From
}

// Run sweeps every minute until ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		c, cancel := context.WithTimeout(ctx, 45*time.Second)
		if _, err := w.Sweep(c); err != nil && ctx.Err() == nil {
			slog.Debug("assist: the owner's mail was not sorted this time", "err", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

var _ = sql.ErrNoRows
