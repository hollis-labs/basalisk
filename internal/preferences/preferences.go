// Package preferences owns the account preference validation and revision CAS.
package preferences

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

var ErrInvalid = errors.New("invalid preferences")
var ErrConflict = errors.New("preferences revision changed")

type Value struct {
	Theme    string `json:"theme"`
	Timezone string `json:"timezone"`
	Revision int    `json:"revision"`
}
type Store struct {
	DB     *sql.DB
	owners sync.Map
}

func New(db *sql.DB) *Store { return &Store{DB: db} }

// Lock serializes admission with the ordinary preferences writer. No SQL
// transaction remains open while a gateway journal or surface callback runs.
func (s *Store) Lock(owner string) func() {
	v, _ := s.owners.LoadOrStore(owner, &sync.Mutex{})
	m := v.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}
func Validate(v Value) error {
	_, e := time.LoadLocation(v.Timezone)
	if (v.Theme != "dark" && v.Theme != "light" && v.Theme != "system") || v.Timezone == "" || v.Timezone == "Local" || len(v.Timezone) > 100 || e != nil {
		return ErrInvalid
	}
	return nil
}
func (s *Store) Read(ctx context.Context, owner string) (Value, error) {
	v := Value{Theme: "dark", Timezone: "UTC", Revision: 1}
	e := s.DB.QueryRowContext(ctx, "SELECT theme,timezone,revision FROM app_preferences WHERE user_id=?", owner).Scan(&v.Theme, &v.Timezone, &v.Revision)
	if errors.Is(e, sql.ErrNoRows) {
		e = nil
	}
	return v, e
}

// UpdateLocked requires Lock for owner. The SQL CAS is still authoritative if
// another process writes. A commit error is uncertain; callers must not retry.
func (s *Store) UpdateLocked(ctx context.Context, owner string, v Value) error {
	if e := Validate(v); e != nil {
		return e
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback() }()
	if _, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO app_preferences(user_id) VALUES(?)", owner); e != nil {
		return e
	}
	r, e := tx.ExecContext(ctx, "UPDATE app_preferences SET theme=?,timezone=?,revision=revision+1 WHERE user_id=? AND revision=?", v.Theme, v.Timezone, owner, v.Revision)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit()
}
func (s *Store) Update(ctx context.Context, owner string, v Value) error {
	unlock := s.Lock(owner)
	defer unlock()
	return s.UpdateLocked(ctx, owner, v)
}
