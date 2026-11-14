package scratch

import (
	"context"
	"encoding/json"
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
		// An item found in Caster keeps the name it has there.
		name := "scratch/" + it.ID
		if rest, ok := strings.CutPrefix(it.CasterRef, "caster://"); ok && rest != "" {
			name = rest
		}
		_, err := y.link.Call(callCtx, "caster_put", map[string]any{
			"name":    name,
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
		_, _ = y.store.db.ExecContext(ctx, `UPDATE items SET caster_ref=?, synced_at=?, sync_error='' WHERE id=? AND updated_at=?`, "caster://"+name, it.UpdatedAt, it.ID, it.UpdatedAt)
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

// Import reads back items that are in Caster's scratch namespace and not here: ones an
// agent or another machine put there. Small text only (a document parked in the namespace
// is not a scratch item). They arrive as already written, so they are not written back.
func (y *Syncer) Import(ctx context.Context, owner string) (int, error) {
	if y == nil || y.link == nil || !y.link.Configured() {
		return 0, nil
	}
	raw, err := callAll(ctx, y.link, "caster_ls", map[string]any{"ns": "scratch"})
	if err != nil {
		return 0, err
	}
	var list struct {
		Items []struct {
			Ref     string `json:"ref"`
			Title   string `json:"title"`
			Updated string `json:"updated"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return 0, err
	}
	n := 0
	for _, li := range list.Items {
		name, ok := strings.CutPrefix(li.Ref, "caster://scratch/")
		if !ok || name == "" {
			continue
		}
		var have int
		_ = y.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE id=? OR caster_ref=?`, name, li.Ref).Scan(&have)
		var skipped int
		_ = y.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM passed WHERE ref=?`, li.Ref).Scan(&skipped)
		if have > 0 || skipped > 0 {
			continue
		}
		got, err := y.link.Call(ctx, "caster_get", map[string]any{"ref": li.Ref, "max_bytes": maxImport + 1})
		if err != nil {
			return n, err
		}
		var item struct {
			Content   string `json:"content"`
			State     string `json:"state"`
			Size      int    `json:"size"`
			Truncated bool   `json:"truncated"`
		}
		if json.Unmarshal(got, &item) != nil || item.State != "resolved" || item.Truncated || item.Size > maxImport || strings.TrimSpace(item.Content) == "" {
			// Not scratch (too large, unreadable): remembered, so it is not fetched each time.
			_, _ = y.store.db.ExecContext(ctx, `INSERT OR IGNORE INTO passed(ref) VALUES(?)`, li.Ref)
			continue
		}
		at, perr := time.Parse(time.RFC3339Nano, li.Updated)
		if perr != nil {
			at = time.Now()
		}
		if err := y.store.adopt(ctx, owner, name, li.Ref, item.Content, at); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// maxImport is the largest text read back from Caster as a scratch item.
const maxImport = 8 << 10

// callAll is Call that reads the whole of a large result when the link can (the gateway
// cuts a long list and hands out the rest on request; tetherlink.Client.CallAll).
func callAll(ctx context.Context, link interface {
	Call(ctx context.Context, tool string, args map[string]any) ([]byte, error)
}, tool string, args map[string]any) ([]byte, error) {
	if all, ok := link.(interface {
		CallAll(ctx context.Context, tool string, args map[string]any) ([]byte, error)
	}); ok {
		return all.CallAll(ctx, tool, args)
	}
	return link.Call(ctx, tool, args)
}
