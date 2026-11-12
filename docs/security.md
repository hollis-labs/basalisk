# Data and security

## Where data lives

All durable data is under one private data directory: `$XDG_DATA_HOME/basalisk` (else
`~/.local/share/basalisk`), or `BASALISK_DATA_DIR` when set. Basalisk creates it with mode 0700 and tightens an
existing one.

| Path | What |
|---|---|
| `basalisk.db` (0600) | SQLite: accounts, sessions, preferences, integration metadata, activity, device and capture records |
| `identity-signing.key` (+ `.jwks.json`) | Ed25519 key that signs minted tokens |
| `secrets/` | Integration credentials (the secrets module's managed file store) |

Back up the entire directory with the app stopped, and protect the backup as a credential. Losing the signing
key invalidates minted tokens, not sessions. There is no password recovery or account reset: do not delete the
database to reset a password unless you mean to discard everything. Test against a separate
`BASALISK_DATA_DIR`.

Startup refuses a database from the pre-Substrate version rather than migrating or opening an empty
installation; see `docs/conversion-notes.md`.

## Accounts and sessions

Accounts, passwords, sessions, API keys and tokens belong to Substrate's identity module (see Substrate's
`docs/identity.md`): bcrypt password hashes, hashed session and key secrets, the `basalisk_session` cookie
(HttpOnly, SameSite=Strict, 12 hours by default via `identity.session_ttl`), and a lockout after five wrong
passwords per username and address for five minutes.

- **CSRF.** A cookie-authenticated write must carry an `X-Requested-With` header and a same-origin `Origin`.
  A request with its own `Authorization` header is exempt.
- **Scopes.** `basalisk.read` for GET and `basalisk.write` for changes on every `/api/` route Basalisk serves.
  A route without a declared scope is refused (`internal/api/scopes_test.go`).
- **Errors** are flat JSON, `{code, message, details?}`.
- **Hosts.** The server accepts only loopback hosts plus the names in `basalisk.allowed_hosts`, which are
  believed only from a loopback proxy sending a single `X-Forwarded-Proto: https`
  (`internal/hostguard`). Other forwarded headers are stripped before routing.
- **Preferences** reject a stale revision instead of overwriting another session's save.

## Credentials

Integration credentials go through the secrets resolver and are stored as files in `secrets/` (mode 0600 inside
a 0700 tree). They are not encrypted at rest: the OS user and privileged processes can read them. Secret values
and storage references are never returned by any API, and there is no reveal endpoint. Removing or replacing a
stored key does not revoke it at the provider.

## Companion devices

Device tokens are separate from sessions and are limited to the companion routes (`docs/companion.md`).
Device records, observations and capture data belong to the person who paired the device, except as noted in
`docs/architecture.md` (single tenant).
