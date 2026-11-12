package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
)

var errNoCaller = errors.New("api: the request carries no verified caller")

// ErrNoUser means the caller is not a signed-in person (an API key or service token has no
// account), or the person is gone or disabled.
var ErrNoUser = errors.New("api: no such user")

// User is the part of a person's sign-in identity that the account view shows. The identity
// module owns it; Basalisk only reads it.
type User struct {
	Username    string
	DisplayName string
	Email       string
}

// UserDirectory reads the identity module's user record.
type UserDirectory interface {
	User(ctx context.Context, userID string) (User, error)
}

// IdentityUsers reads users from the identity module's table. The module exposes no
// lookup, so this is a read-only query over its documented identity_users table (see
// docs/conversion-notes.md, Gaps).
type IdentityUsers struct{ DB *sql.DB }

// User returns the active user with the identity user id (no principal prefix).
func (d IdentityUsers) User(ctx context.Context, userID string) (User, error) {
	var u User
	var disabled int
	err := d.DB.QueryRowContext(ctx, "SELECT username,display_name,email,disabled FROM identity_users WHERE id=?", userID).Scan(&u.Username, &u.DisplayName, &u.Email, &disabled)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && disabled != 0) {
		return User{}, ErrNoUser
	}
	return u, err
}

// Profile is the account view: the sign-in identity composed with the stored preferences.
type Profile struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	Theme       string `json:"theme"`
	Timezone    string `json:"timezone"`
	Revision    int    `json:"revision"`
}

const principalUserPrefix = "user:"

// userID is the identity user id behind the caller, "" for a caller that is not a person.
func userID(r *http.Request) string {
	p, ok := caller(r)
	if !ok {
		return ""
	}
	return strings.TrimPrefix(p.ID, principalUserPrefix)
}

func (a *API) profile(r *http.Request) (Profile, error) {
	p, ok := caller(r)
	if !ok || !strings.HasPrefix(p.ID, principalUserPrefix) {
		return Profile{}, ErrNoUser
	}
	id := userID(r)
	u, err := a.users.User(r.Context(), id)
	if err != nil {
		return Profile{}, err
	}
	out := Profile{Username: u.Username, DisplayName: u.DisplayName, Email: u.Email, Theme: "dark", Timezone: "UTC", Revision: 1}
	err = a.svc.db.QueryRowContext(r.Context(), "SELECT theme,timezone,revision FROM app_preferences WHERE user_id=?", id).Scan(&out.Theme, &out.Timezone, &out.Revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Profile{}, err
	}
	return out, nil
}

// account answers who the caller is and their preferences. Display name and email are edited
// through the identity module (PUT /api/identity/profile), not here.
func (a *API) account(w http.ResponseWriter, r *http.Request) {
	p, err := a.profile(r)
	if errors.Is(err, ErrNoUser) {
		fail(w, 403, "This needs a signed-in person.")
		return
	}
	if err != nil {
		fail(w, 500, "Could not load account.")
		return
	}
	reply(w, 200, p)
}

func updated(w http.ResponseWriter, result sql.Result, err error) bool {
	if err != nil {
		fail(w, 500, "Could not save changes.")
		return false
	}
	n, err := result.RowsAffected()
	if err != nil {
		fail(w, 500, "Could not save changes.")
		return false
	}
	if n == 0 {
		fail(w, 409, "This account changed in another session. Reload before saving.")
		return false
	}
	return true
}

// preferences saves the theme and time zone with an optimistic revision check. A person who
// has never saved has the defaults at revision 1.
func (a *API) preferences(w http.ResponseWriter, r *http.Request) {
	if _, err := a.profile(r); errors.Is(err, ErrNoUser) {
		fail(w, 403, "This needs a signed-in person.")
		return
	} else if err != nil {
		fail(w, 500, "Could not load account.")
		return
	}
	var in struct {
		Theme    string `json:"theme"`
		Timezone string `json:"timezone"`
		Revision int    `json:"revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	_, e := time.LoadLocation(in.Timezone)
	if (in.Theme != "dark" && in.Theme != "light" && in.Theme != "system") || in.Timezone == "" || in.Timezone == "Local" || len(in.Timezone) > 100 || e != nil {
		fail(w, 400, "Choose a valid theme and IANA timezone, such as America/Chicago.")
		return
	}
	id := userID(r)
	if _, e = a.svc.db.ExecContext(r.Context(), "INSERT OR IGNORE INTO app_preferences(user_id) VALUES(?)", id); e != nil {
		fail(w, 500, "Could not save changes.")
		return
	}
	result, e := a.svc.db.ExecContext(r.Context(), "UPDATE app_preferences SET theme=?,timezone=?,revision=revision+1 WHERE user_id=? AND revision=?", in.Theme, in.Timezone, id, in.Revision)
	if updated(w, result, e) {
		a.account(w, r)
	}
}
