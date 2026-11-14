package scratch

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Caster is where an item's text lives for everything outside Basalisk: each item is a
// named item there, caster://scratch/<id>, and an edit is a new revision. The store here
// is the working copy; the syncer writes behind it and tries again when Caster cannot be
// reached. What Caster has no place for yet (done, put away) travels as tags.

// Putter is what the syncer needs of the Tether gateway.
type Putter interface {
	Configured() bool
	Call(ctx context.Context, tool string, args map[string]any) ([]byte, error)
}

// Syncer writes items to Caster.
type Syncer struct {
	store *Store
	link  Putter
	kick  chan struct{}
	once  sync.Once
}

func NewSyncer(store *Store, link Putter) *Syncer {
	return &Syncer{store: store, link: link, kick: make(chan struct{}, 1)}
}

// Kick asks for a pass soon (an item was added or changed).
func (y *Syncer) Kick() {
	if y == nil {
		return
	}
	select {
	case y.kick <- struct{}{}:
	default:
	}
}

// Run writes behind the store until ctx ends.
func (y *Syncer) Run(ctx context.Context) {
	if y == nil || y.link == nil || !y.link.Configured() {
		return
	}
	wait := 5 * time.Second
	for {
		left, failed := y.Pass(ctx)
		switch {
		case failed:
			wait = min(wait*2, 5*time.Minute)
		case left:
			wait = 0
		default:
			wait = 5 * time.Second
		}
		if wait == 0 {
			continue
		}
		timer := time.NewTimer(wait)
		if !failed {
			// Nothing waiting: sleep until something changes (or ten minutes pass).
			timer.Reset(10 * time.Minute)
		}
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-y.kick:
			timer.Stop()
			wait = 5 * time.Second
		case <-timer.C:
		}
	}
}

// Ref is the Caster ref an item is kept under.
func Ref(id string) string { return "caster://scratch/" + id }

// Pass writes up to 25 items that Caster does not have as they are now. It reports whether
// more are waiting and whether a write failed.
func (y *Syncer) Pass(ctx context.Context) (more, failed bool) {
	rows, err := y.store.db.QueryContext(ctx, `SELECT id, owner FROM items WHERE synced_at<>updated_at ORDER BY updated_at LIMIT 26`)
	if err != nil {
		return false, true
	}
	type key struct{ id, owner string }
	var todo []key
	for rows.Next() {
		var k key
		if rows.Scan(&k.id, &k.owner) == nil {
			todo = append(todo, k)
		}
	}
	_ = rows.Close()
	if len(todo) > 25 {
		more, todo = true, todo[:25]
	}
	for _, k := range todo {
		it, ok := y.store.Get(ctx, k.owner, k.id)
		if !ok {
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := y.link.Call(callCtx, "caster_put", map[string]any{
			"name":    "scratch/" + it.ID,
			"content": Markdown("", []Item{it}),
			"mime":    "text/markdown",
			"title":   title(it.Text),
			"tags":    strings.Join(casterTags(it), " "),
			"source":  "basalisk:scratch",
		})
		cancel()
		if err != nil {
			slog.Warn("scratch: an item was not written to Caster", "id", it.ID, "err", err)
			_, _ = y.store.db.ExecContext(ctx, `UPDATE items SET sync_error=? WHERE id=?`, clip(err.Error(), 300), it.ID)
			return more, true
		}
		_, _ = y.store.db.ExecContext(ctx, `UPDATE items SET caster_ref=?, synced_at=?, sync_error='' WHERE id=? AND updated_at=?`, Ref(it.ID), it.UpdatedAt, it.ID, it.UpdatedAt)
	}
	return more, false
}

// casterTags are an item's tags in Caster: that it is scratch, its kind, its day, what it
// is tied to, and whether it is done or put away.
func casterTags(it Item) []string {
	tags := []string{"scratch", "kind:" + it.Kind, "day:" + it.Day}
	for _, l := range it.Links {
		if strings.ContainsAny(l.Value, " \t\n") {
			continue
		}
		tags = append(tags, l.Rel+":"+l.Value)
	}
	if it.Done {
		tags = append(tags, "done")
	}
	if it.Archived {
		tags = append(tags, "archived")
	}
	return tags
}

func title(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return clip(line, 120)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
