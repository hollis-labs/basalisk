package account

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hollis-labs/basalisk/internal/requestorigin"
	"golang.org/x/crypto/bcrypt"
)

const sessionCookie = "basalisk_session"
const sessionTTL = 12 * time.Hour

type API struct {
	store        *Store
	mu           sync.Mutex
	attempts     int
	window       time.Time
	pairAttempts int
	pairWindow   time.Time
}

// Handler serves JSON only. A custom header plus exact Origin validation prevents
// cross-site writes, including the unauthenticated first-run and login endpoints.
func (s *Store) Handler() http.Handler {
	a := &API{store: s}
	m := http.NewServeMux()
	m.HandleFunc("GET /api/setup", a.status)
	m.HandleFunc("POST /api/setup", a.setup)
	m.HandleFunc("POST /api/login", a.login)
	m.HandleFunc("POST /api/logout", a.authorize(a.logout))
	m.HandleFunc("GET /api/account", a.authorize(a.profile))
	m.HandleFunc("GET /api/identity", a.authorize(a.humanIdentity))
	m.HandleFunc("POST /api/assurance/prepare", a.authorize(a.prepareAssurance))
	m.HandleFunc("POST /api/assurance/finish", a.authorize(a.finishAssurance))
	m.HandleFunc("POST /api/assurance/cancel", a.authorize(a.cancelAssurance))
	m.HandleFunc("POST /api/assurance/deliver", a.authorize(a.deliverAssurance))
	m.HandleFunc("POST /api/assurance/outcome", a.authorize(a.recoverAssurance))
	m.HandleFunc("PUT /api/account", a.authorize(a.updateProfile))
	m.HandleFunc("PUT /api/preferences", a.authorize(a.preferences))
	m.HandleFunc("PUT /api/password", a.authorize(a.password))
	m.HandleFunc("GET /api/integrations", a.authorize(a.integrations))
	m.HandleFunc("POST /api/integrations", a.authorize(a.createIntegration))
	m.HandleFunc("PUT /api/integrations/{id}/credential", a.authorize(a.rotateIntegration))
	m.HandleFunc("DELETE /api/integrations/{id}", a.authorize(a.deleteIntegration))
	m.HandleFunc("GET /api/activity", a.authorize(a.activityState))
	m.HandleFunc("GET /api/activity/sessions", a.authorize(a.activitySessions))
	m.HandleFunc("GET /api/activity/sessions/{id}/events", a.authorize(a.activityEvents))
	m.HandleFunc("POST /api/activity/commands", a.authorize(a.activityCommand))
	m.HandleFunc("GET /api/devices", a.authorize(a.devices))
	m.HandleFunc("POST /api/devices/grants", a.authorize(a.deviceGrant))
	m.HandleFunc("DELETE /api/devices/grants/{id}", a.authorize(a.revokeGrant))
	m.HandleFunc("DELETE /api/devices/{id}", a.authorize(a.revokeDevice))
	m.HandleFunc("GET /api/fragment-connections", a.authorize(a.fragmentConnections))
	m.HandleFunc("PUT /api/fragment-connections/{id}", a.authorize(a.setFragmentConnection))
	m.HandleFunc("DELETE /api/fragment-connections/{id}", a.authorize(a.removeFragmentConnection))
	m.HandleFunc("GET /api/fragment-submissions", a.authorize(a.fragmentSubmissions))
	m.HandleFunc("GET /api/capture-readiness", a.authorize(a.ownerCaptureReadiness))
	m.HandleFunc("GET /api/fragment-submissions/{id}", a.authorize(a.fragmentSubmissionDetail))
	m.HandleFunc("DELETE /api/fragment-submissions/{id}", a.authorize(a.discardFragmentSubmission))
	m.HandleFunc("POST /api/fragment-submissions/{id}/retry", a.authorize(a.retryFragmentSubmission))
	m.HandleFunc("GET /api/observations", a.authorize(a.observations))
	m.HandleFunc("GET /api/activity/sessions/{id}/artifacts", a.authorize(a.artifacts))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != "GET" && r.Method != "HEAD" {
			origin := r.Header.Get("Origin")
			if r.Header.Get("X-Basalisk-Request") != "1" || (origin != "" && origin != requestorigin.Origin(r)) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				fail(w, 403, "Request origin was not accepted.")
				return
			}
		}
		m.ServeHTTP(w, r)
	})
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, message string) {
	reply(w, status, map[string]string{"error": message})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeLimit(w, r, v, 16384)
}
func decodeLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		fail(w, 415, "Expected JSON.")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		fail(w, 400, "Invalid request.")
		return false
	}
	return true
}
func token() string              { return rand.Text() }
func digest(value string) string { h := sha256.Sum256([]byte(value)); return hex.EncodeToString(h[:]) }
func cookie(w http.ResponseWriter, r *http.Request, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: value, Path: "/", HttpOnly: true, Secure: requestorigin.Secure(r), SameSite: http.SameSiteStrictMode, MaxAge: age})
}
func validPassword(p string) bool { return utf8.RuneCountInString(p) >= 12 && len(p) <= 72 }
func validProfile(name, email string) bool {
	if strings.TrimSpace(name) == "" || len(name) > 100 || len(email) > 254 {
		return false
	}
	if email != "" {
		p, e := mail.ParseAddress(email)
		if e != nil || p.Address != email {
			return false
		}
	}
	return true
}
func (a *API) limited(w http.ResponseWriter) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if time.Since(a.window) > time.Minute {
		a.window = time.Now()
		a.attempts = 0
	}
	a.attempts++
	if a.attempts > 10 {
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "Too many attempts. Try again in one minute.")
		return true
	}
	return false
}
func (a *API) authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, e := r.Cookie(sessionCookie)
		if e != nil {
			fail(w, 401, "Please sign in.")
			return
		}
		var exists int
		e = a.store.db.QueryRow("SELECT 1 FROM sessions WHERE hash=? AND expires>?", digest(c.Value), time.Now().Unix()).Scan(&exists)
		if errors.Is(e, sql.ErrNoRows) {
			fail(w, 401, "Please sign in.")
			return
		}
		if e != nil {
			fail(w, 500, "Could not read the session.")
			return
		}
		next(w, r)
	}
}
func (a *API) status(w http.ResponseWriter, r *http.Request) {
	_, err := a.store.profile()
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		fail(w, 500, "Could not read setup state.")
		return
	}
	reply(w, 200, map[string]bool{"setupRequired": errors.Is(err, sql.ErrNoRows)})
}
func (a *API) setup(w http.ResponseWriter, r *http.Request) {
	if a.limited(w) {
		return
	}
	var in struct {
		Username    string `json:"username"`
		DisplayName string `json:"displayName"`
		Password    string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if in.Username == "" || len(in.Username) > 64 || strings.ContainsAny(in.Username, " \t\n\r") || !validProfile(in.DisplayName, "") || !validPassword(in.Password) {
		fail(w, 400, "Use a username without spaces, a display name, and a password of at least 12 characters and at most 72 bytes.")
		return
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if e != nil {
		fail(w, 500, "Could not create account.")
		return
	}
	tx, e := a.store.db.Begin()
	if e != nil {
		fail(w, 500, "Could not create account.")
		return
	}
	defer tx.Rollback()
	var count int
	if e = tx.QueryRow("SELECT count(*) FROM owner").Scan(&count); e != nil {
		fail(w, 500, "Could not create account.")
		return
	}
	if count != 0 {
		fail(w, 409, "Setup is already complete.")
		return
	}
	_, e = tx.Exec("INSERT INTO owner(id,username,display_name,password_hash) VALUES(1,?,?,?)", in.Username, in.DisplayName, hash)
	t := token()
	if e == nil {
		_, e = tx.Exec("INSERT INTO sessions(hash,expires) VALUES(?,?)", digest(t), time.Now().Add(sessionTTL).Unix())
	}
	if e != nil || tx.Commit() != nil {
		fail(w, 500, "Could not create account.")
		return
	}
	cookie(w, r, t, int(sessionTTL.Seconds()))
	a.profile(w, r)
}
func (a *API) login(w http.ResponseWriter, r *http.Request) {
	if a.limited(w) {
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	var username string
	var hash []byte
	e := a.store.db.QueryRow("SELECT username,password_hash FROM owner WHERE id=1").Scan(&username, &hash)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		fail(w, 500, "Could not sign in.")
		return
	}
	if len(hash) == 0 {
		hash = []byte("$2a$10$7EqJtq98hPqEX7fNZaFWoO5bU6BRs7SsXmoRZiQF6xT1r/2ByIGV2")
	}
	passwordErr := bcrypt.CompareHashAndPassword(hash, []byte(in.Password))
	if e != nil || passwordErr != nil || in.Username != username {
		fail(w, 401, "Incorrect username or password.")
		return
	}
	t := token()
	tx, e := a.store.db.Begin()
	if e != nil {
		fail(w, 500, "Could not sign in.")
		return
	}
	defer tx.Rollback()
	_, e = tx.Exec("DELETE FROM sessions WHERE expires<=?", time.Now().Unix())
	if e == nil {
		var result sql.Result
		result, e = tx.Exec("INSERT INTO sessions(hash,expires) SELECT ?,? FROM owner WHERE id=1 AND password_hash=?", digest(t), time.Now().Add(sessionTTL).Unix(), hash)
		if e == nil {
			n, err := result.RowsAffected()
			if err != nil {
				e = err
			} else if n == 0 {
				fail(w, 401, "Password changed. Please sign in again.")
				return
			}
		}
	}
	if e != nil || tx.Commit() != nil {
		fail(w, 500, "Could not sign in.")
		return
	}
	cookie(w, r, t, int(sessionTTL.Seconds()))
	a.profile(w, r)
}
func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie(sessionCookie)
	if _, e := a.store.db.Exec("DELETE FROM sessions WHERE hash=?", digest(c.Value)); e != nil {
		fail(w, 500, "Could not sign out.")
		return
	}
	cookie(w, r, "", -1)
	reply(w, 200, map[string]bool{"ok": true})
}
func (a *API) profile(w http.ResponseWriter, r *http.Request) {
	p, e := a.store.profile()
	if e != nil {
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
func (a *API) updateProfile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DisplayName string `json:"displayName"`
		Email       string `json:"email"`
		Revision    int    `json:"revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.Email = strings.TrimSpace(in.Email)
	if !validProfile(in.DisplayName, in.Email) {
		fail(w, 400, "Enter a display name and a valid email address, or leave email blank.")
		return
	}
	result, e := a.store.db.Exec("UPDATE owner SET display_name=?,email=?,revision=revision+1 WHERE id=1 AND revision=?", in.DisplayName, in.Email, in.Revision)
	if updated(w, result, e) {
		a.profile(w, r)
	}
}
func (a *API) preferences(w http.ResponseWriter, r *http.Request) {
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
	result, e := a.store.db.Exec("UPDATE owner SET theme=?,timezone=?,revision=revision+1 WHERE id=1 AND revision=?", in.Theme, in.Timezone, in.Revision)
	if updated(w, result, e) {
		a.profile(w, r)
	}
}
func (a *API) password(w http.ResponseWriter, r *http.Request) {
	if a.limited(w) {
		return
	}
	var in struct {
		Current string `json:"currentPassword"`
		Next    string `json:"newPassword"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !validPassword(in.Next) {
		fail(w, 400, "Use a password of at least 12 characters and at most 72 bytes.")
		return
	}
	var old []byte
	if e := a.store.db.QueryRow("SELECT password_hash FROM owner WHERE id=1").Scan(&old); e != nil {
		fail(w, 500, "Could not change password.")
		return
	}
	if bcrypt.CompareHashAndPassword(old, []byte(in.Current)) != nil {
		fail(w, 400, "Current password is incorrect.")
		return
	}
	hash, e := bcrypt.GenerateFromPassword([]byte(in.Next), bcrypt.DefaultCost)
	if e != nil {
		fail(w, 500, "Could not change password.")
		return
	}
	tx, e := a.store.db.Begin()
	if e != nil {
		fail(w, 500, "Could not change password.")
		return
	}
	defer tx.Rollback()
	result, e := tx.Exec("UPDATE owner SET password_hash=? WHERE id=1 AND password_hash=?", hash, old)
	if !updated(w, result, e) {
		return
	}
	_, e = tx.Exec("DELETE FROM sessions")
	if e != nil || tx.Commit() != nil {
		fail(w, 500, "Could not change password.")
		return
	}
	cookie(w, r, "", -1)
	reply(w, 200, map[string]bool{"ok": true})
}

// Integration URLs are metadata only. Provider adapters must explicitly own
// outbound requests and credential use; saving a record never calls that URL.
func validBaseURL(raw string) bool {
	if raw == "" {
		return true
	}
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && len(raw) <= 1000
}
