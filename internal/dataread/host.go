package dataread

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/hollis-labs/libs/security/auth"
)

type registered struct {
	provider   Provider
	descriptor Descriptor
}
type continuation struct {
	Binding                   [32]byte
	Expires, Observed         time.Time
	Native                    string
	Items                     []Item
	Refs                      []GetRequest
	Truncated                 bool
	TruncationReason, Ranking string
}
type Host struct {
	policy     Authorizer
	membership Membership
	providers  map[Source]registered
	mu         sync.Mutex
	cursors    map[string]continuation
	now        func() time.Time
}

// New snapshots the registry. No policy means no access, including discovery.
// There is intentionally no owner/capture-token fallback and no content cache.
func New(policy Authorizer, membership Membership, providers ...Provider) (*Host, error) {
	if len(providers) > 4 {
		return nil, ErrLimit
	}
	h := &Host{policy: policy, membership: membership, providers: map[Source]registered{}, cursors: map[string]continuation{}, now: time.Now}
	for _, p := range providers {
		d := p.Describe()
		if !validSource(d.Source) || d.AdapterVersion == "" || (d.Projection != Projection && d.Projection != TesseractProjection) || d.MaxItems != MaxItems {
			return nil, ErrInvalid
		}
		if _, ok := h.providers[d.Source]; ok {
			return nil, ErrInvalid
		}
		d.Kinds = slices.Clone(d.Kinds)
		d.VersionModes = slices.Clone(d.VersionModes)
		d.SelectionScopes = slices.Clone(d.SelectionScopes)
		d.QueryModes = slices.Clone(d.QueryModes)
		d.ReadEffects = slices.Clone(d.ReadEffects)
		d.KindVersionModes = cloneVersionModes(d.KindVersionModes)
		h.providers[d.Source] = registered{p, d}
	}
	return h, nil
}

func cloneVersionModes(in map[string][]string) map[string][]string {
	if in == nil {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = slices.Clone(v)
	}
	return out
}

func validSource(s Source) bool { return ValidID(s.Provider) && ValidID(s.Instance) }
func validGet(r GetRequest) bool {
	return validSource(r.Ref.Source()) && ValidID(r.Ref.Kind) && ValidID(r.Ref.ID) &&
		(r.Version.Mode == "latest" && r.Version.ID == "" && r.Version.Token == "" || r.Version.Mode == "revision" && ValidID(r.Version.ID) && r.Version.Token == "" || r.Version.Mode == "if_current" && r.Version.ID == "" && ValidID(r.Version.Token))
}
func (h *Host) authorize(ctx context.Context, t Target) (Grant, error) {
	if r, ok := h.providers[t.Source]; ok {
		t.Projection = r.descriptor.Projection
	}
	p, ok := auth.FromContext(ctx)
	if !ok || !p.Verified || p.ID == "" || (!p.ExpiresAt.IsZero() && !p.ExpiresAt.After(h.now())) || h.policy == nil {
		return Grant{}, ErrAccess
	}
	g, err := h.policy.Authorize(ctx, p, t)
	if err != nil || g.ID == "" || g.PolicyVersion == "" {
		return Grant{}, ErrAccess
	}
	return g, nil
}
func (h *Host) unchanged(ctx context.Context, t Target, g Grant) error {
	current, err := h.authorize(ctx, t)
	if err != nil || current != g {
		return ErrAccess
	}
	return nil
}
func (h *Host) Discover(ctx context.Context) ([]Descriptor, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := h.authorize(ctx, Target{Action: "discover"}); err != nil {
		return nil, err
	}
	out := []Descriptor{}
	for s, r := range h.providers {
		if _, err := h.authorize(ctx, Target{Action: "discover", Source: s}); err == nil {
			d := r.descriptor
			d.Kinds = slices.Clone(d.Kinds)
			d.VersionModes = slices.Clone(d.VersionModes)
			d.SelectionScopes = slices.Clone(d.SelectionScopes)
			d.QueryModes = slices.Clone(d.QueryModes)
			d.ReadEffects = slices.Clone(d.ReadEffects)
			d.KindVersionModes = cloneVersionModes(d.KindVersionModes)
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b Descriptor) int {
		if a.Source.Provider < b.Source.Provider {
			return -1
		}
		if a.Source.Provider > b.Source.Provider {
			return 1
		}
		if a.Source.Instance < b.Source.Instance {
			return -1
		}
		if a.Source.Instance > b.Source.Instance {
			return 1
		}
		return 0
	})
	return out, nil
}

func (h *Host) Get(ctx context.Context, in GetRequest) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	item, _, err := h.get(ctx, in)
	if err != nil {
		return Result{}, err
	}
	out := Result{Item: item, Projection: h.providers[in.Ref.Source()].descriptor.Projection, ObservedAt: h.now().UTC(), Consistency: "source_current_projection"}
	if !fits(out) {
		return Result{}, ErrLimit
	}
	return out, nil
}
func (h *Host) get(ctx context.Context, in GetRequest) (Item, Grant, error) {
	if !validGet(in) {
		return Item{}, Grant{}, ErrInvalid
	}
	target := Target{Action: "get", Source: in.Ref.Source(), Get: in}
	g, err := h.authorize(ctx, target)
	if err != nil {
		return Item{}, Grant{}, err
	}
	r, ok := h.providers[in.Ref.Source()]
	if !ok {
		return Item{}, Grant{}, ErrUnavailable
	}
	if !slices.Contains(r.descriptor.Kinds, in.Ref.Kind) || !slices.Contains(r.descriptor.VersionModes, in.Version.Mode) {
		return Item{}, Grant{}, ErrUnsupported
	}
	if r.descriptor.KindVersionModes != nil && !slices.Contains(r.descriptor.KindVersionModes[in.Ref.Kind], in.Version.Mode) {
		return Item{}, Grant{}, ErrUnsupported
	}
	if in.Version.Mode == "revision" && r.descriptor.RevisionRequiresHistory && !g.WholeHistory {
		return Item{}, Grant{}, ErrUnsupported
	}
	item, err := r.provider.Get(ctx, in)
	if changed := h.unchanged(ctx, target, g); changed != nil {
		return Item{}, Grant{}, changed
	}
	if err != nil {
		return Item{}, Grant{}, SafeError(err)
	}
	// Alias resolution is not authorization. Refuse a canonical identity change.
	if !validItem(item, in.Ref.Source()) || item.Ref != in.Ref {
		return Item{}, Grant{}, ErrUnavailable
	}
	if in.Version.Mode == "revision" && (item.Version.Kind != "immutable_revision" || item.Version.ID != in.Version.ID) {
		return Item{}, Grant{}, ErrVersion
	}
	if in.Version.Mode == "if_current" && (item.Version.Kind != "current_token" || item.Version.ID != in.Version.Token) {
		return Item{}, Grant{}, ErrChanged
	}
	if ctx.Err() != nil {
		return Item{}, Grant{}, ErrUnavailable
	}
	return item, g, nil
}
func validItem(it Item, source Source) bool {
	return it.Ref.Source() == source && ValidID(it.Ref.ID) && ValidID(it.Ref.Kind) && (it.Version.Kind == "immutable_revision" || it.Version.Kind == "current_token") && ValidID(it.Version.ID) && it.ProjectionVersion != "" && it.ContentTrust == "untrusted_source"
}
func fits(v any) bool { b, e := json.Marshal(v); return e == nil && len(b) <= MaxBytes }

func (h *Host) Query(ctx context.Context, in QueryRequest) (Page, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if in.Limit == 0 {
		in.Limit = 25
	}
	if !validSource(in.Source) || in.Limit < 1 || in.Limit > MaxItems || len(in.Cursor) > 128 {
		return Page{}, ErrInvalid
	}
	if in.Mode == "recall" {
		if in.Recall == nil || in.Scope != "" || in.SessionID != "" || len(in.Recall.Namespace) > 512 || len(in.Recall.Query) > 2000 || !ValidID(in.Recall.Kind) {
			return Page{}, ErrInvalid
		}
		selector := *in.Recall
		in.Recall = &selector
	} else if in.Mode == "session" {
		if !ValidID(in.SessionID) || in.Scope != "" || in.Recall != nil {
			return Page{}, ErrInvalid
		}
	} else if in.Mode != "selection" || in.SessionID != "" || in.Scope == "" || in.Recall != nil {
		return Page{}, ErrInvalid
	}
	query := in
	query.Cursor = ""
	target := Target{Action: "query", Source: in.Source, Query: query}
	g, err := h.authorize(ctx, target)
	if err != nil {
		return Page{}, err
	}
	r, ok := h.providers[in.Source]
	if !ok {
		return Page{}, ErrUnavailable
	}
	if in.Mode == "selection" && (!g.WholeSelection || !slices.Contains(r.descriptor.SelectionScopes, in.Scope)) {
		return Page{}, ErrUnsupported
	}
	if in.Mode == "recall" {
		if _, ok := r.provider.(RecallProvider); !ok || !g.WholeSelection || !slices.Contains(r.descriptor.QueryModes, "recall") {
			return Page{}, ErrUnsupported
		}
	}
	if in.Mode == "session" && h.membership == nil {
		return Page{}, ErrUnsupported
	}
	p, _ := auth.FromContext(ctx)
	upstream := ""
	if checker, ok := r.provider.(ContinuationAuthority); ok {
		upstream, err = checker.ContinuationAuthority(ctx)
		if err != nil {
			return Page{}, SafeError(err)
		}
		if upstream == "" {
			return Page{}, ErrAccess
		}
	}
	bindingRaw, _ := json.Marshal(struct {
		Principal  auth.Principal
		Grant      Grant
		Target     Target
		Adapter    string
		Projection string
		Upstream   string
	}{p, g, target, r.descriptor.AdapterVersion, r.descriptor.Projection, upstream})
	binding := sha256.Sum256(bindingRaw)
	c := continuation{Binding: binding}
	if in.Cursor != "" {
		h.mu.Lock()
		saved, exists := h.cursors[in.Cursor]
		h.mu.Unlock()
		if !exists || saved.Binding != binding || !saved.Expires.After(h.now()) {
			return Page{}, ErrCursor
		}
		c = saved
	}
	if len(c.Items) == 0 && len(c.Refs) == 0 {
		if in.Mode == "selection" || in.Mode == "recall" {
			var page NativePage
			var e error
			if in.Mode == "recall" {
				page, e = r.provider.(RecallProvider).Recall(ctx, *in.Recall, c.Native)
			} else {
				page, e = r.provider.Select(ctx, in.Scope, c.Native)
			}
			if e != nil {
				return Page{}, SafeError(e)
			}
			if len(page.Items) > MaxItems || len(page.Cursor) > 8192 || page.Cursor != "" && page.Cursor == c.Native {
				return Page{}, ErrUnavailable
			}
			c.Items, c.Native = page.Items, page.Cursor
			c.Truncated, c.TruncationReason, c.Ranking = page.Truncated, page.TruncationReason, page.Ranking
			for _, it := range c.Items {
				if !validItem(it, in.Source) || !slices.Contains(r.descriptor.Kinds, it.Ref.Kind) {
					return Page{}, ErrUnavailable
				}
			}
		} else {
			page, e := h.membership.Select(ctx, p, in.Source, in.SessionID, c.Native)
			if e != nil {
				return Page{}, SafeError(e)
			}
			if len(page.Refs) > MaxItems || len(page.Cursor) > 8192 || page.Cursor != "" && page.Cursor == c.Native {
				return Page{}, ErrUnavailable
			}
			c.Refs, c.Native = page.Refs, page.Cursor
			for _, ref := range c.Refs {
				if !validGet(ref) || ref.Ref.Source() != in.Source || ref.Version.Mode != "revision" {
					return Page{}, ErrUnavailable
				}
			}
		}
		c.Observed = h.now().UTC()
	}
	out := Page{Source: in.Source, Projection: r.descriptor.Projection, Status: "complete", Items: []Item{}, Problems: []Problem{}, ObservedAt: c.Observed, Consistency: "best_effort_pagination", SourceTruncated: c.Truncated, SourceTruncationReason: c.TruncationReason, Ranking: c.Ranking}
	if in.Mode == "recall" {
		out.Coverage = "recall_candidates"
	}
	if in.Mode == "selection" || in.Mode == "recall" {
		n := min(in.Limit, len(c.Items))
		out.Items = append(out.Items, c.Items[:n]...)
		c.Items = c.Items[n:]
	} else {
		n := min(in.Limit, len(c.Refs))
		refs := c.Refs[:n]
		grants := make([]Grant, n)
		// Authorize every candidate before the first provider read. A session
		// correlation grants neither source access nor disclosure of denied refs.
		for i, ref := range refs {
			grants[i], err = h.authorize(ctx, Target{Action: "get", Source: in.Source, Get: ref})
			if err != nil {
				return Page{}, err
			}
		}
		for _, ref := range refs {
			it, _, e := h.get(ctx, ref)
			if e == ErrAccess {
				return Page{}, e
			}
			if e != nil {
				out.Status = "partial"
				out.Problems = append(out.Problems, Problem{ref.Ref, SafeError(e).Error()})
			} else {
				out.Items = append(out.Items, it)
			}
		}
		for i, ref := range refs {
			if e := h.unchanged(ctx, Target{Action: "get", Source: in.Source, Get: ref}, grants[i]); e != nil {
				return Page{}, e
			}
		}
		c.Refs = c.Refs[n:]
		out.ObservedAt = h.now().UTC()
	}
	if err = h.unchanged(ctx, target, g); err != nil {
		return Page{}, err
	}
	if checker, ok := r.provider.(ContinuationAuthority); ok {
		current, e := checker.ContinuationAuthority(ctx)
		if e != nil {
			return Page{}, SafeError(e)
		}
		if current != upstream {
			return Page{}, ErrAccess
		}
	}
	if ctx.Err() != nil {
		return Page{}, ErrUnavailable
	}
	out.Exhausted = len(c.Items) == 0 && len(c.Refs) == 0 && c.Native == ""
	if out.Exhausted && c.Truncated {
		out.Exhausted = false
		out.Status = "partial"
	}
	if len(c.Items) > 0 || len(c.Refs) > 0 || c.Native != "" {
		// Returning a new page does not refresh its evidence. Buffered pages
		// retain their original observedAt and expire after two minutes.
		if c.Expires.IsZero() {
			c.Expires = h.now().Add(2 * time.Minute)
		}
		out.NextCursor = rand.Text()
		if !fits(out) {
			return Page{}, ErrLimit
		}
		if err = h.keep(out.NextCursor, c); err != nil {
			return Page{}, err
		}
	}
	if !fits(out) {
		return Page{}, ErrLimit
	}
	return out, nil
}

func (h *Host) keep(key string, c continuation) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for k, v := range h.cursors {
		if !v.Expires.After(h.now()) {
			delete(h.cursors, k)
		}
	}
	// Bound private cursor state; reject rather than silently truncate a page.
	bytes := 0
	for _, v := range h.cursors {
		b, _ := json.Marshal(v)
		bytes += len(b)
	}
	b, err := json.Marshal(c)
	if err != nil || len(b) > MaxBytes || bytes+len(b) > 8*MaxBytes || len(h.cursors) >= 32 {
		return ErrLimit
	}
	h.cursors[key] = c
	return nil
}

// String is intentionally only a count; providers may contain secrets.
func (h *Host) String() string { return fmt.Sprintf("dataread.Host(%d sources)", len(h.providers)) }
