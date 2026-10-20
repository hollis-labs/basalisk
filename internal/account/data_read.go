package account

import (
	"context"
	"strconv"

	"github.com/hollis-labs/basalisk/internal/dataread"
	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/libs/security/pairing"
)

// ReadSource is explicit host configuration, never a client-supplied URL or
// inferred pairing grant. Instance identity belongs to Provider; IntegrationID
// and CaptureOrigin bind historical confirmed receipts to that instance. Moving
// an endpoint requires an explicit reviewed mapping, not an automatic URL join.
type ReadSource struct {
	Provider      dataread.Provider
	IntegrationID string
	CaptureOrigin string
}
type confirmedMembership struct {
	store    *Store
	bindings map[dataread.Source]ReadSource
}

// NewReadHost builds the disabled-by-default foundation. No command-line/env
// configuration currently calls this; tests provide disposable read grants.
// A nil policy never exposes even provider discovery. Local owner login and
// companion capture grants do not imply a source read grant.
func (s *Store) NewReadHost(policy dataread.Authorizer, sources ...ReadSource) (*dataread.Host, error) {
	m := &confirmedMembership{store: s, bindings: map[dataread.Source]ReadSource{}}
	providers := make([]dataread.Provider, 0, len(sources))
	for _, source := range sources {
		if source.Provider == nil {
			return nil, dataread.ErrInvalid
		}
		d := source.Provider.Describe()
		origin, err := pairing.Origin(source.CaptureOrigin)
		if err != nil || origin != source.CaptureOrigin || !dataread.ValidID(source.IntegrationID) || d.Source.Provider != "fragments-engine" {
			return nil, dataread.ErrInvalid
		}
		m.bindings[d.Source] = source
		providers = append(providers, source.Provider)
	}
	return dataread.New(policy, m, providers...)
}

func (m *confirmedMembership) Select(ctx context.Context, p auth.Principal, source dataread.Source, session, cursor string) (dataread.MembershipPage, error) {
	if !p.Verified || p.ID == "" {
		return dataread.MembershipPage{}, dataread.ErrAccess
	}
	binding, ok := m.bindings[source]
	if !ok {
		return dataread.MembershipPage{}, dataread.ErrUnavailable
	}
	var owner string
	if err := m.store.db.QueryRowContext(ctx, "SELECT user_id FROM activity_sessions WHERE id=?", session).Scan(&owner); err != nil || owner != p.ID {
		return dataread.MembershipPage{}, dataread.ErrAccess
	}
	before := int64(1 << 62)
	if cursor != "" {
		var err error
		before, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || before <= 0 {
			return dataread.MembershipPage{}, dataread.ErrCursor
		}
	}
	// Both independently stored records must agree. Device-reported legacy
	// artifacts, unconfirmed saves, sessionless captures, and captures sent to a
	// different origin cannot become source membership. Deduplicate by immutable
	// revision, since two captures may point to the same canonical FE resource.
	rows, err := m.store.db.QueryContext(ctx, `WITH confirmed AS (
	 SELECT MAX(a.sequence) AS sequence,s.fragment_id,s.fragment_revision_id
	 FROM companion_artifacts a JOIN fragment_submissions s
	 ON a.device_id=s.device_id AND a.capture_id='broker:'||s.upstream_capture_id
	 WHERE a.session_id=? AND a.source='basalisk.fe-response (server-confirmed)'
	 AND s.state='saved' AND s.integration_id=? AND s.delivery_origin=?
	 AND s.fragment_id<>'' AND s.fragment_revision_id<>''
	 AND json_extract(s.payload,'$.draft.sessionID')=a.session_id
	 AND json_extract(a.payload,'$.fragmentID')=s.fragment_id
	 AND json_extract(a.payload,'$.fragmentRevisionID')=s.fragment_revision_id
	 GROUP BY s.fragment_id,s.fragment_revision_id
	) SELECT sequence,fragment_id,fragment_revision_id FROM confirmed
	WHERE sequence<? ORDER BY sequence DESC LIMIT 51`, session, binding.IntegrationID, binding.CaptureOrigin, before)
	if err != nil {
		return dataread.MembershipPage{}, dataread.ErrUnavailable
	}
	defer rows.Close()
	out := dataread.MembershipPage{Refs: []dataread.GetRequest{}}
	var last int64
	for rows.Next() {
		var seq int64
		var id, revision string
		if err = rows.Scan(&seq, &id, &revision); err != nil {
			return dataread.MembershipPage{}, dataread.ErrUnavailable
		}
		if len(out.Refs) == dataread.MaxItems {
			out.Cursor = strconv.FormatInt(last, 10)
			break
		}
		if !dataread.ValidID(id) || !dataread.ValidID(revision) {
			return dataread.MembershipPage{}, dataread.ErrUnavailable
		}
		out.Refs = append(out.Refs, dataread.GetRequest{Ref: dataread.Ref{Provider: source.Provider, Instance: source.Instance, Kind: "fragment", ID: id}, Version: dataread.VersionRequest{Mode: "revision", ID: revision}})
		last = seq
	}
	if rows.Err() != nil {
		return dataread.MembershipPage{}, dataread.ErrUnavailable
	}
	return out, nil
}
