# Architecture

Basalisk is a personal dashboard: a React frontend, a Go backend and a macOS companion app. The backend is a
[Substrate](https://github.com/hollis-labs/substrate) app: `cmd/basalisk/substrate.yaml` selects the framework
modules, the generated `substrate_wiring.go` links them, and `internal/` is Basalisk's own code. Substrate
supplies mechanism (config, logging, health, HTTP host, auth guard, identity, secrets, database); Basalisk owns
its data and its process. Basalisk is SQLite only.

## Layout

| Path | What |
|---|---|
| `cmd/basalisk/substrate.yaml` | The manifest: modules and their config. Edit it, then `make generate`. |
| `cmd/basalisk/substrate_wiring.go` | Generated. Do not edit. |
| `cmd/basalisk/main.go`, `config.go`, `serve.go` | `basalisk` / `basalisk serve` (the default), `basalisk version` (and the skeleton's `config` and `health`). `config.go` holds the boot hooks (`AfterConfig` points the module config at the data directory; `Before["database"]` refuses a pre-Substrate database); `serve.go` assembles the handler chain and runs the background workers. |
| `internal/persistence` | The one goose migration (the schema) and the startup guard for the pre-Substrate layout. `persistencetest` opens a migrated database for tests. |
| `internal/api` | The HTTP layer and the domain it serves: owner routes (`/api/...`), companion routes (`/auth/...`, `/companion/...`), the fragment delivery worker, capture, artifacts, observations. |
| `internal/activity` | Human work sessions: commands, journal, context. |
| `internal/hostguard` | Host check (DNS rebinding) and forwarding-header hygiene. |
| `internal/settings` | The app's own `basalisk:` config block. |
| `internal/appdirs` | The data directory: where it is, and making it private. |
| `internal/dataread`, `feread`, `tesseractread`, `fecapture` | Read and capture adapters for Fragments Engine and Tesseract. |
| `internal/webui` | The embedded frontend (`Assets()`), built from `frontend/`. |
| `companion/` | The macOS companion app (Swift). Its protocol is unchanged. |

## Request path

```
client -> hostguard -> identity.Middleware -> httphost
                                               |- auth guard (verifies the credential, enforces the route scope)
                                               |    |- /api/* identity routes (the module's)
                                               |    |- /api/* owner routes (internal/api)
                                               |- /auth/*, /companion/*  (open to the guard; device tokens)
                                               |- /healthz, and the SPA for everything else
```

## Auth model

- `auth.mode: enforce`. Every `/api/` route needs a verified credential and the scope it declares. Nothing in
  the manifest or environment weakens it; only an explicit `SUBSTRATE_CONFIG` override can.
- The identity module is the issuer and the user store. First run: `GET /api/setup` reports `setup_required`,
  `POST /api/setup {display_name, username, password}` makes the owner (scope `*`), closes setup and signs in.
  The module also serves login, logout, password, profile, sessions, users, API keys, pairing codes,
  `/api/token` and `/api/identity/jwks` (see Substrate `docs/identity.md`). A browser keeps the
  `basalisk_session` cookie (HttpOnly, SameSite=Strict); sign-in lasts 12 hours.
- A cookie-authenticated write must carry an `X-Requested-With` header and a same-origin `Origin`.
  Requests with their own `Authorization` header (keys, tokens) are exempt.
- The owner of domain data is the verified Principal subject (`user:<id>`), read from the request context, never
  from a request body.
- Single tenant. Anyone signed in with the right scopes sees the same integrations, fragment connections and
  submissions, observations and readiness. Three things belong to the person: preferences, activity
  sessions, and companion devices (a device is paired by one person and acts for them).
- Companion devices are not Substrate identities. They keep using the libs `security/pairing` store: a signed-in
  person creates a grant, the macOS app exchanges the code at `/auth/pair/exchange`, and then presents the
  device token itself. The companion mounts are open to the guard because the handlers verify that token; a
  browser session never authenticates them.

## Scopes

Declared in one route table (`internal/api/api.go`), which registers both the handler and the scope, so a route
cannot exist without one. `scopes_test.go` proves the table and the inventory.

| Scope | Routes |
|---|---|
| `basalisk.read` | every `GET` under `/api/` that Basalisk serves |
| `basalisk.write` | every `PUT`, `POST` and `DELETE` that Basalisk serves |

The owner holds `*`. Identity's own routes carry `authenticated` or `basalisk.admin`. Companion devices hold
`basalisk.activity.read`, `basalisk.observations.write` and `basalisk.fragments.create`, checked by the
companion handlers.

Owner routes: `GET /api/account`, `PUT /api/preferences`, `/api/integrations` (GET, POST, `PUT /{id}/credential`,
`DELETE /{id}`), `/api/activity` (GET, `/sessions`, `/sessions/{id}/events`, `/sessions/{id}/artifacts`,
`POST /commands`), `/api/devices` (GET, `POST /grants`, `DELETE /grants/{id}`, `DELETE /{id}`),
`/api/fragment-connections` (GET, `PUT|DELETE /{id}`), `/api/fragment-submissions` (GET, `GET|DELETE /{id}`,
`POST /{id}/retry`), `GET /api/capture-readiness`, `GET /api/observations`.

## Data

One goose migration, `internal/persistence/migrations/sqlite/00001_init.sql`, applied by the database module.

| Tables | Owner |
|---|---|
| `app_preferences` (theme, timezone, revision, by identity user id) | Basalisk |
| `integrations` (metadata; the credential is in the secrets store, never in a response) | Basalisk |
| `companion_observations`, `companion_artifacts`, `fragment_connections`, `fragment_submissions`, `capture_context_bindings` | Basalisk |
| `activity_identity`, `activity_contexts`, `activity_sessions`, `activity_events`, `activity_commands` | Basalisk |
| `identity_users` (read through `Service.User`), `identity_sessions`, `identity_credentials`, `identity_pairing_codes` | identity module |
| `hollis_pairing_*` | libs `security/pairing` |

`GET /api/account` returns `{username, displayName, email, theme, timezone, revision}`: the identity user plus the
preferences row (defaults `dark`, `UTC`, revision 1 until the first save). `PUT /api/preferences` keeps its
optimistic revision check, theme `dark|light|system` and IANA time zone validation. Display name and email are
edited with the identity module's `PUT /api/identity/profile`.

Startup refuses a database with the pre-Substrate layout (an `owner` table, or `PRAGMA user_version > 0`) and tells
the operator to move the old data directory aside. There is no migration.

## Files

Under the data directory (`$XDG_DATA_HOME/basalisk`, or `BASALISK_DATA_DIR`), mode 0700: `basalisk.db` (0600),
`identity-signing.key` (0600, plus its public `.jwks.json`) and `secrets/` (integration credentials). These are the
default names; a path from the override replaces them. Startup creates only what the final configuration uses: with
`database.sqlite.path` pointed elsewhere there is no empty `basalisk.db`. The database file is created 0600 wherever it
is, so an absolute path in a directory of its own choosing is still private; SQLite gives its `-wal`/`-shm` files the
mode of the database. A relative path with a subdirectory (`db/main.db`, `keys/sign.key`) creates that directory
(0700) inside the data directory, and only there. For an absolute path outside the data directory nothing is created
except the database file: its directory must already exist.

## Config

The manifest (`cmd/basalisk/substrate.yaml`) sets the defaults. The binary anchors the three data paths
(`database.sqlite.path`, `identity.key_file`, `secrets.state_dir`) in the data directory in the `AfterConfig` boot hook,
after the override is applied: a relative path (the manifest default or one from the override) is joined onto the data
directory (`path: other.db` becomes `<data>/other.db`), an empty one becomes the default name, and an absolute path is
kept as is. A relative path that climbs out of the data directory through `..` stops startup. Any key can be
overridden in a YAML file named by `SUBSTRATE_CONFIG`, which is deep-merged over the manifest, so overriding only `database.sqlite.path` is enough.

| Key | Default | Meaning |
|---|---|---|
| `httphost.addr` | `127.0.0.1:8085` | Listen address. Must be loopback. |
| `auth.mode` | `enforce` | Leave it. |
| `identity.key_file` | `<data>/identity-signing.key` | Token signing key. Relative: joined onto `<data>`; absolute: kept. |
| `identity.session_ttl` | `12h` | Sign-in lifetime. |
| `identity.trusted_proxy` | `false` | Believe `X-Forwarded-Proto` from the local TLS proxy (for the Secure cookie). |
| `identity.allowed_origins` | none | Extra origins whose cookie-authenticated writes are accepted. |
| `identity.secure_cookies` | `auto` | `auto`, `always` or `never`. |
| `secrets.state_dir` | `<data>/secrets` | Credential store. Relative: joined onto `<data>`; absolute: kept. |
| `database.sqlite.path` | `<data>/basalisk.db` | Database file. Relative: joined onto `<data>`; absolute: kept. |
| `basalisk.allowed_hosts` | none | Host names served besides loopback, exactly as sent (with a port if the client sends one). Needs a loopback TLS proxy sending `X-Forwarded-Proto: https`. |
| `basalisk.fe_integration_id` | none | Integration the fragment delivery worker serves. |
| `basalisk.public_origin` | none | Canonical `https://` origin of this Basalisk. |
| `basalisk.tether_issuer_url` | none | HTTPS origin of the capture credential issuer. |
| `basalisk.tether_subject` | none | Principal the capture credential is minted for. |
| `basalisk.tether_parent_ref` | none | Secret reference of the dedicated enrollment parent. |

The worker runs only when the five delivery keys are set; setting some of them, or a bad value, stops startup.

## What was dropped, and why

- Owner, password and login-session code, the rate limiter and the cookie: the identity module does this now.
- `X-Basalisk-Request` and the origin check: replaced by identity's `X-Requested-With` and same-origin rule.
- `internal/requestorigin`: re-expressed as `internal/hostguard` plus identity's `trusted_proxy`.
- `internal/assurance` (WebAuthn, Tachyon), `/api/assurance/*`, the `/assurance/` status mount and `GET /api/identity`:
  removed. A follow-up restores WebAuthn from git history on top of the identity module.
- The Folio/Chimera host. Substrate's `httphost` serves the SPA and the API.
- The legacy state-directory guard, `BASALISK_CONFIG_DIR`, `BASALISK_ASSURANCE_CONFIG`, `BASALISK_HTTPS_PROXY`,
  `LISTEN_ADDR`: see `conversion-notes.md`.
