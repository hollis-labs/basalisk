// Package dataread is the transport-neutral, fail-closed read boundary. It does
// not verify credentials or issue grants: transports stamp verified auth facts,
// and an application-supplied Authorizer evaluates current resource policy.
package dataread

import (
	"context"
	"errors"
	"time"

	"github.com/hollis-labs/libs/security/auth"
)

const MaxBytes = 1 << 20
const MaxItems = 50

// This projection contains a source Markdown preview as well as display text.
// It is not a body-free summary; policy must authorize every projected field.
const Projection = "reader-preview.v1"
const TesseractProjection = "tesseract-text.v1"

var (
	ErrAccess      = errors.New("access_required")
	ErrInvalid     = errors.New("invalid_request")
	ErrUnsupported = errors.New("unsupported")
	ErrUnavailable = errors.New("unavailable")
	ErrNotFound    = errors.New("not_found")
	ErrVersion     = errors.New("version_unavailable")
	ErrChanged     = errors.New("version_changed")
	ErrCursor      = errors.New("cursor_invalid_or_expired")
	ErrLimit       = errors.New("limit_exceeded")
)

type Source struct {
	Provider string `json:"provider"`
	Instance string `json:"instance"`
}
type Ref struct {
	Provider string `json:"provider"`
	Instance string `json:"instance"`
	Kind     string `json:"kind"`
	ID       string `json:"id"`
}

func (r Ref) Source() Source { return Source{r.Provider, r.Instance} }

type VersionRequest struct {
	Mode  string `json:"mode"`
	ID    string `json:"id,omitempty"`
	Token string `json:"token,omitempty"`
}
type Version struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type GetRequest struct {
	Ref     Ref            `json:"ref"`
	Version VersionRequest `json:"version"`
}
type QueryRequest struct {
	Source Source `json:"source"`
	// selection is a source-owned selection; session is server-confirmed membership.
	Mode      string          `json:"mode"`
	Scope     string          `json:"scope,omitempty"`
	SessionID string          `json:"sessionID,omitempty"`
	Limit     int             `json:"limit,omitempty"`
	Cursor    string          `json:"cursor,omitempty"`
	Recall    *RecallSelector `json:"recall,omitempty"`
}

// RecallSelector is deliberately narrow: one concrete namespace and domain,
// optionally ranked lexical text. No broad backend query or selector passthrough.
type RecallSelector struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Query     string `json:"query,omitempty"`
}
type Item struct {
	Ref     Ref     `json:"ref"`
	Version Version `json:"version"`
	// The Reader aggregate can change independently of the immutable content revision.
	ProjectionVersion string   `json:"projectionVersion"`
	Title             string   `json:"title"`
	Summary           string   `json:"summary"`
	PreviewMarkdown   string   `json:"previewMarkdown"`
	ContentTrust      string   `json:"contentTrust"`
	Namespace         string   `json:"namespace,omitempty"`
	BodyMarkdown      string   `json:"bodyMarkdown,omitempty"`
	Score             *float64 `json:"score,omitempty"`
}
type Result struct {
	Item        Item      `json:"item"`
	Projection  string    `json:"projection"`
	ObservedAt  time.Time `json:"observedAt"`
	Consistency string    `json:"consistency"`
}
type Problem struct {
	Ref  Ref    `json:"ref"`
	Code string `json:"code"`
}
type Page struct {
	Source                 Source    `json:"source"`
	Projection             string    `json:"projection"`
	Status                 string    `json:"status"`
	Items                  []Item    `json:"items"`
	Problems               []Problem `json:"problems"`
	ObservedAt             time.Time `json:"observedAt"`
	Consistency            string    `json:"consistency"`
	NextCursor             string    `json:"nextCursor,omitempty"`
	Exhausted              bool      `json:"exhausted"`
	Coverage               string    `json:"coverage,omitempty"`
	Ranking                string    `json:"ranking,omitempty"`
	SourceTruncated        bool      `json:"sourceTruncated,omitempty"`
	SourceTruncationReason string    `json:"sourceTruncationReason,omitempty"`
}
type Descriptor struct {
	Source                  Source              `json:"source"`
	AdapterVersion          string              `json:"adapterVersion"`
	Kinds                   []string            `json:"kinds"`
	VersionModes            []string            `json:"versionModes"`
	SelectionScopes         []string            `json:"selectionScopes"`
	Projection              string              `json:"projection"`
	MaxItems                int                 `json:"maxItems"`
	Consistency             string              `json:"consistency"`
	QueryModes              []string            `json:"queryModes,omitempty"`
	ReadEffects             []string            `json:"readEffects,omitempty"`
	KindVersionModes        map[string][]string `json:"kindVersionModes,omitempty"`
	RevisionRequiresHistory bool                `json:"revisionRequiresHistory,omitempty"`
}

// Target is constructed by the host, never decoded from client authority claims.
// Query authorization must include permission to see the selection's membership.
type Target struct {
	Action     string
	Source     Source
	Get        GetRequest
	Query      QueryRequest
	Projection string
}
type Grant struct {
	// Nonempty fingerprints of the current grant AND policy decision. They must
	// change on revocation, policy/redaction changes, or source reconfiguration.
	ID            string
	PolicyVersion string
	// Source selection is allowed only when every candidate and projected field
	// is authorized upstream. A post-filter of a broad Reader query is insufficient.
	WholeSelection bool
	// Some sources can safely retrieve an exact revision only by scanning the
	// item's authorized history. This is permission for those candidates too,
	// not merely permission to expose one requested historical revision.
	WholeHistory bool
}
type Authorizer interface {
	// A successful grant authorizes Target.Projection in full and has already fulfilled
	// mandatory audit/redaction/egress obligations. Deny/ask/unfulfilled or
	// field-restricted decisions return an error; this host cannot widen them.
	Authorize(context.Context, auth.Principal, Target) (Grant, error)
}
type AuthorizeFunc func(context.Context, auth.Principal, Target) (Grant, error)

func (f AuthorizeFunc) Authorize(c context.Context, p auth.Principal, t Target) (Grant, error) {
	return f(c, p, t)
}

// NativePage is internal-only. Native cursors and extra page items never cross
// a transport boundary; Host keeps them in a bounded, expiring cursor store.
type NativePage struct {
	Items            []Item
	Cursor           string
	Truncated        bool
	TruncationReason string
	Ranking          string
}
type RecallProvider interface {
	Recall(context.Context, RecallSelector, string) (NativePage, error)
}

// ContinuationAuthority rechecks upstream credentials before a buffered page
// can be returned. A changed fingerprint invalidates the cursor. Providers
// without this seam rely on the host policy's source-grant revocation checks.
type ContinuationAuthority interface {
	ContinuationAuthority(context.Context) (string, error)
}
type Provider interface {
	Describe() Descriptor
	Get(context.Context, GetRequest) (Item, error)
	Select(context.Context, string, string) (NativePage, error)
}
type MembershipPage struct {
	Refs   []GetRequest
	Cursor string
}
type Membership interface {
	// Select verifies the human session's ownership independently of source read
	// grants. It must return only server-confirmed, revision-pinned receipts.
	Select(context.Context, auth.Principal, Source, string, string) (MembershipPage, error)
}

func SafeError(err error) error {
	for _, e := range []error{ErrAccess, ErrInvalid, ErrUnsupported, ErrUnavailable, ErrNotFound, ErrVersion, ErrChanged, ErrCursor, ErrLimit} {
		if errors.Is(err, e) {
			return e
		}
	}
	return ErrUnavailable
}

func ValidID(s string) bool {
	if len(s) == 0 || len(s) > 255 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
