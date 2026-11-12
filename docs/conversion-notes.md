# Conversion notes: Basalisk onto Substrate

This records what changed when the Go backend moved onto Substrate, what the framework did not cover, and what an
operator and the frontend need to do. It is a clean break: no shims, no fallbacks, no data migration.

## Gaps

Things Substrate did not provide, or choices that were ambiguous. Each says what was done; the owner routes them.

1. **`libs/security/pairing` pin: resolved.** It was missing from the Substrate BOM, so `substrate pins check --app .`
   failed. The one-line BOM entry landed in Substrate main (1ceedec) and `go run github.com/hollis-labs/substrate/cmd/substrate
   pins check --app .` now passes (`app pins match the bom`). Pairing is pinned in the BOM like any other lib.
2. **The manifest cannot carry the app's own config: resolved.** `config:` used to accept only catalogue modules, so
   the `basalisk:` block had no defaults in `substrate.yaml` and lived in `internal/settings`. Substrate (#8) now
   accepts an app block; the defaults (all empty) are in `cmd/basalisk/substrate.yaml` and the app reads them from
   `app.Config["basalisk"]`.
3. **The `config` override merged one level deep: resolved.** An override of `database.sqlite` replaced the whole
   block and had to repeat `path`. Substrate (#6) now deep-merges the `SUBSTRATE_CONFIG` file, so overriding only
   `database.sqlite.path` (or only one other nested key) leaves its siblings alone.
4. **No hook to resolve run-time paths: resolved.** The data directory is only known at run time (XDG or
   `BASALISK_DATA_DIR`), but the manifest compiles to a constant. Before, `cmd/basalisk/config.go` rewrote the
   generated config JSON before `boot.Start`. Substrate (#9) adds `boot.StartWith` with an `AfterConfig` hook, which
   runs after the `SUBSTRATE_CONFIG` override is applied; Basalisk's hook resolves the data directory and anchors the
   database path, identity key file and secrets directory in it. A path from the override that is absolute is the
   operator's choice and is kept; a relative one (including the manifest default) is replaced with the data-dir
   path. `effectiveDatabasePath` is gone.
5. **No pre-migration hook for the legacy guard: resolved.** The database module migrates as soon as it opens the
   file, so the guard used to run in `main` before `boot.Start` and re-read the `SUBSTRATE_CONFIG` file to judge the
   file that would really be opened, duplicating the config module's merge rule. It is now `Before["database"]`, a
   hook on the boot sequence that reads the path from the merged config the module will read. Tests in
   `cmd/basalisk/boot_test.go` cover an old-layout directory and an override that moves the database.
6. **The identity module had no user lookup: resolved.** `GET /api/account` needs username, display name and email,
   and the Principal carries only the display name, so `internal/api.IdentityUsers` read the module's
   `identity_users` table directly. Substrate (#7) adds `Service.User(ctx, id)`, and `IdentityUsers` now wraps it;
   a disabled or unknown user maps to `ErrNoUser`. Basalisk no longer touches the table.
7. **`/api/data/*` was never served.** The old `dataread` host's HTTP handler was "intentionally not mounted until
   enrollment and caller policy are available", and nothing called `NewReadHost`. The packages (`dataread`, `feread`,
   `tesseractread`, `fecapture`) and `NewReadHost` are ported with their tests, but no `/api/data/*` route is
   registered, so none has a scope. Mounting it would be a new feature that needs a caller policy.
8. **"Single tenant" versus per-person ownership.** The brief says everyone with the scopes sees the same data, and
   also that the owner id is the verified Principal subject. Both hold, with this line: integrations, fragment
   connections and submissions, observations and readiness are shared; preferences, activity sessions and companion
   devices are per person (the companion already matched sessions to the device's owner). `fragment_connections`
   gained an `owner_id` column so the delivery worker can check the device's grant without a global owner.
9. **The secrets module's resolver can write.** `secrets.Resolver(app)` is a libs `*Resolver` with `Set` and `Delete`,
   so integration credentials use it as is (`secrets.state_dir` is the old `<data>/secrets`). No gap.
10. **Config directory dropped.** `BASALISK_CONFIG_DIR` only located the assurance config, which is gone, so the
    config directory has no consumer and was removed rather than kept as dead code.
12. **Package split.** `internal/account` became `internal/api` with `Store` reduced to `Service`; schema, accounts,
    sessions and assurance left it. The owner and companion handlers and the delivery worker still share one package
    because they share the device, submission and artifact types. Splitting them is a cleanup, not done here.

## Old to new

### Environment and flags

| Old | New |
|---|---|
| `LISTEN_ADDR` (default `127.0.0.1:8080`) | `httphost.addr` (default `127.0.0.1:8085`); must be loopback |
| `BASALISK_HTTPS_PROXY=1` | `identity.trusted_proxy: true` plus `basalisk.allowed_hosts: [<proxied host>]` |
| `BASALISK_DATA_DIR` | unchanged |
| `BASALISK_CONFIG_DIR`, `BASALISK_ASSURANCE_CONFIG` | removed |
| `BASALISK_FE_INTEGRATION_ID` | `basalisk.fe_integration_id` |
| `BASALISK_PUBLIC_ORIGIN` | `basalisk.public_origin` |
| `BASALISK_TETHER_ISSUER_URL` | `basalisk.tether_issuer_url` |
| `BASALISK_TETHER_SUBJECT` | `basalisk.tether_subject` |
| `BASALISK_TETHER_PARENT_REF` | `basalisk.tether_parent_ref` |
| (new) | `SUBSTRATE_CONFIG`: YAML file holding any override above |

The canonical proxy host was compiled in; it is now whatever `basalisk.allowed_hosts` lists.

### Paths and files

| Old | New |
|---|---|
| `$XDG_DATA_HOME/basalisk/basalisk.db` | same path, new schema |
| `<data>/secrets/` | same |
| (none) | `identity-signing.key` and `identity-signing.key.jwks.json` in `<data>` |
| owner id: a random `usr_` id in `activity_identity` | the verified Principal subject (`user:<id>`); only `installation_id` stays app-local |
| legacy `$XDG_STATE_HOME/basalisk` guard | removed |

### HTTP

| Old | New |
|---|---|
| `POST /api/setup {username, displayName, password}` | `POST /api/setup {username, display_name, password}` (username: lowercase letters, digits, `.`, `-`, `_`; 2-64) |
| `POST /api/login`, `POST /api/logout` | same paths; logout answers 204 |
| `PUT /api/password {currentPassword, newPassword}` | `PUT /api/password {current_password, new_password}` |
| `PUT /api/account {displayName, email, revision}` | `PUT /api/identity/profile {display_name, email}` (no revision) |
| `GET /api/account` | same path, same shape |
| `PUT /api/preferences` | same path, same shape and checks |
| `GET /api/identity`, `/api/assurance/*`, `/assurance/` | removed |
| `/auth/pair/exchange`, `/auth/renew`, `/companion/*` | unchanged paths and behaviour |
| everything else under `/api/` | unchanged paths |
| (new) | `/api/identity/*`, `/api/token`, `/api/pair`, `/api/identity/jwks`, `GET /healthz` |

## For the frontend

- **Errors** are `{code, message, details?}`, not `{error}`. Read `message`.
- **Writes** must send `X-Requested-With` (any non-empty value) on every `POST`, `PUT` and `DELETE`; the old
  `X-Basalisk-Request: 1` is gone. Browsers send `Origin` themselves and it must be the page's own.
- **Sign-in** bodies use `display_name`, `current_password`, `new_password`. Responses from setup and login are the
  identity user: `{id, username, display_name, email, scopes}`. Use `GET /api/account` for the view model.
- **The preferences revision** now moves only when preferences are saved. Saving a profile does not change it.
- **Scopes**: a user without `basalisk.read` gets 403 on every API route; without `basalisk.write` on every change.
- **Dev proxy**: the backend default port is 8085.

## For the operator

- **First deploy needs the old data directory moved aside.** The schema changed and there is no migration. Startup
  stops with `move the old data directory aside` when it finds an `owner` table or a non-zero `user_version`.
  Move `$XDG_DATA_HOME/basalisk` (or `BASALISK_DATA_DIR`), start, and run first-run setup again. Paired companion
  apps pair again; stored integration credentials are entered again.
- Health is `GET /healthz` (database included).
- Secrets: the `secrets/` directory and the signing key are private to the user. Losing the signing key ends minted
  tokens, not sessions.
- Behind a local TLS proxy: set `basalisk.allowed_hosts`, `identity.trusted_proxy: true` and, if the page's origin
  differs from the proxied Host, `identity.allowed_origins`, in the `SUBSTRATE_CONFIG` file. The proxy must reach
  the backend over loopback and send `X-Forwarded-Proto: https`.
- Sign-in rate limiting is identity's (five failures per username and address, five minutes), replacing the old
  ten-per-minute global limiter. Device pairing keeps its ten-per-minute limit.
- Sessions last 12 hours (`identity.session_ttl`), as before.
- Pins live in Substrate's BOM. Never edit the versions in `go.mod` by hand; `make pins` checks them.
