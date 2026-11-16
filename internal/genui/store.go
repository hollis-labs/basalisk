package genui

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	contract "github.com/hollis-labs/substrate/genui"
	"github.com/hollis-labs/substrate/genui/gateway"
)

type repository struct{ db *sql.DB }

func encode(v any) ([]byte, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	return contract.CanonicalJSON(b)
}
func (r *repository) Put(ctx context.Context, s gateway.Surface) error {
	b, e := encode(s)
	if e != nil {
		return e
	}
	_, e = r.db.ExecContext(ctx, "INSERT OR IGNORE INTO genui_surfaces(namespace,actor,id,generation,snapshot) VALUES(?,?,?,?,?)", s.Scope.Namespace, s.Scope.Actor, s.Envelope.ID, s.Generation, b)
	if e != nil {
		return e
	}
	var old []byte
	e = r.db.QueryRowContext(ctx, "SELECT snapshot FROM genui_surfaces WHERE namespace=? AND actor=? AND id=?", s.Scope.Namespace, s.Scope.Actor, s.Envelope.ID).Scan(&old)
	if e != nil {
		return e
	}
	if !bytes.Equal(old, b) {
		return gateway.ErrInvalid
	}
	return nil
}
func (r *repository) Get(ctx context.Context, s gateway.Scope, id string) (gateway.Surface, error) {
	var b []byte
	var superseded bool
	e := r.db.QueryRowContext(ctx, "SELECT snapshot,superseded FROM genui_surfaces WHERE namespace=? AND actor=? AND id=?", s.Namespace, s.Actor, id).Scan(&b, &superseded)
	if errors.Is(e, sql.ErrNoRows) {
		return gateway.Surface{}, gateway.ErrNotFound
	}
	if e != nil {
		return gateway.Surface{}, e
	}
	var out gateway.Surface
	e = json.Unmarshal(b, &out)
	out.Superseded = superseded
	return out, e
}
func (r *repository) Supersede(ctx context.Context, s gateway.Scope, id, generation string) error {
	v, e := r.db.ExecContext(ctx, "UPDATE genui_surfaces SET superseded=1 WHERE namespace=? AND actor=? AND id=? AND generation=?", s.Namespace, s.Actor, id, generation)
	if e != nil {
		return e
	}
	n, e := v.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return gateway.ErrNotFound
	}
	return nil
}
func (r *repository) Load(ctx context.Context, k gateway.Key) (gateway.Record, error) {
	var b []byte
	e := r.db.QueryRowContext(ctx, "SELECT record FROM genui_actions WHERE namespace=? AND actor=? AND id=?", k.Scope.Namespace, k.Scope.Actor, k.ActionID).Scan(&b)
	if errors.Is(e, sql.ErrNoRows) {
		return gateway.Record{}, gateway.ErrNotFound
	}
	if e != nil {
		return gateway.Record{}, e
	}
	var v gateway.Record
	e = json.Unmarshal(b, &v)
	return v, e
}
func (r *repository) Begin(ctx context.Context, k gateway.Key, v gateway.Record) (gateway.Record, bool, error) {
	v.Revision = 1
	b, e := encode(v)
	if e != nil {
		return v, false, e
	}
	req, e := encode(v.Request)
	if e != nil {
		return v, false, e
	}
	result, e := r.db.ExecContext(ctx, "INSERT OR IGNORE INTO genui_actions(namespace,actor,id,revision,digest,request,claimed,record) VALUES(?,?,?,?,?,?,?,?)", k.Scope.Namespace, k.Scope.Actor, k.ActionID, v.Revision, v.Digest, req, v.Claimed, b)
	if e != nil {
		return v, false, e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return v, false, e
	}
	out, e := r.Load(ctx, k)
	return out, n == 1, e
}
func (r *repository) CompareAndSwap(ctx context.Context, k gateway.Key, rev uint64, v gateway.Record) (gateway.Record, bool, error) {
	v.Revision = rev + 1
	b, e := encode(v)
	if e != nil {
		return v, false, e
	}
	req, e := encode(v.Request)
	if e != nil {
		return v, false, e
	}
	result, e := r.db.ExecContext(ctx, "UPDATE genui_actions SET revision=?,claimed=?,record=? WHERE namespace=? AND actor=? AND id=? AND revision=? AND digest=? AND request=? AND (claimed=0 OR ?=1)", v.Revision, v.Claimed, b, k.Scope.Namespace, k.Scope.Actor, k.ActionID, rev, v.Digest, req, v.Claimed)
	if e != nil {
		return v, false, e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return v, false, e
	}
	out, e := r.Load(ctx, k)
	return out, n == 1, e
}

type SurfaceInfo struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	ActionID string `json:"actionId,omitempty"`
}

func (r *repository) list(ctx context.Context, s gateway.Scope) ([]SurfaceInfo, error) {
	rows, e := r.db.QueryContext(ctx, "SELECT snapshot FROM genui_surfaces WHERE namespace=? AND actor=? ORDER BY rowid DESC LIMIT 100", s.Namespace, s.Actor)
	if e != nil {
		return nil, e
	}
	var snapshots [][]byte
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			break
		}
		snapshots = append(snapshots, b)
	}
	if e == nil {
		e = rows.Err()
	}
	_ = rows.Close()
	if e != nil {
		return nil, e
	}
	out := make([]SurfaceInfo, 0, len(snapshots))
	for _, b := range snapshots {
		var v gateway.Surface
		if e = json.Unmarshal(b, &v); e != nil {
			return nil, e
		}
		info := SurfaceInfo{ID: v.Envelope.ID, Title: v.Envelope.Title}
		choices, e := contract.Choices(v.Envelope)
		if e != nil {
			return nil, e
		}
		for _, c := range choices {
			a := c.CallFunction.Args.Action.ID
			if _, e = r.Load(ctx, gateway.Key{Scope: s, ActionID: a}); e == nil {
				info.ActionID = a
				break
			} else if !errors.Is(e, gateway.ErrNotFound) {
				return nil, e
			}
		}
		out = append(out, info)
	}
	return out, nil
}
