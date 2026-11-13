// Package api is Basalisk's HTTP surface over its own data: the owner-facing /api routes
// (preferences, integrations, activity, devices, fragment capture administration) and the
// device-facing /auth and /companion routes the macOS companion speaks.
//
// Who may call is not decided here. Sign-in, sessions, keys and the route guard are
// Substrate's (the identity and auth modules); the owner routes declare a scope each
// (see routes.go) and read the verified caller from the request context. The companion
// routes are open to the guard because they authenticate device tokens themselves.
package api

import (
	"database/sql"
	"errors"
	"sync"

	"github.com/hollis-labs/libs/security/pairing"
	"github.com/hollis-labs/libs/security/secrets"

	"github.com/hollis-labs/basalisk/internal/activity"
)

// Service is what the handlers share: the database handle, the credential resolver, the
// activity journal, the device store and the delivery worker's observable state. It owns no
// schema (that is internal/persistence) and no accounts (that is the identity module).
type Service struct {
	db         *sql.DB
	secrets    *secrets.Resolver
	activity   *activity.Store
	pairing    *pairing.Store
	deliveryMu sync.RWMutex
	delivery   deliveryActivity
	fragMode   string // how owner captures are delivered (frag.go)
}

// New builds the Service over a migrated database. Companion devices keep their own tables,
// which the pairing store creates.
func New(db *sql.DB, resolver *secrets.Resolver) (*Service, error) {
	if db == nil || resolver == nil {
		return nil, errors.New("api: a database and a secrets resolver are required")
	}
	devices, err := pairing.Open(db, pairing.Config{Audience: "basalisk", Scopes: []string{scopeContext, scopeObserve, scopeCapture}})
	if err != nil {
		return nil, err
	}
	return &Service{db: db, secrets: resolver, activity: activity.New(db), pairing: devices}, nil
}
