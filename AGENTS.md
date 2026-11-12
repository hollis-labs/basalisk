# Basalisk

Personal dashboard: a React frontend, a Go backend on the [Substrate](https://github.com/hollis-labs/substrate) framework, and a macOS companion app.
Not: a place for framework code (that is Substrate), a multi-tenant service, or anything kept for compatibility with the pre-Substrate layout.

## Start here

- `cmd/basalisk/substrate.yaml` — the manifest: modules and config. The shell (config, logging, health, HTTP host, auth guard, identity, secrets, database, CLI skeleton) comes from here. `cmd/basalisk/substrate_wiring.go` is GENERATED; edit the manifest and run `make generate`.
- `cmd/basalisk/` — `main.go` (commands), `config.go` (points module config at the data directory, runs the legacy guard), `serve.go` (the handler chain and background workers).
- `internal/api` — HTTP and domain: owner routes under `/api/`, companion routes under `/auth/` and `/companion/`, the fragment delivery worker. One route table registers handlers and scopes.
- `internal/persistence` — the one SQLite migration (the schema) and the guard against the pre-Substrate layout. `internal/activity`, `hostguard`, `settings`, `appdirs`, and the `dataread`/`feread`/`tesseractread`/`fecapture` adapters.
- `docs/architecture.md` — layout, auth model, scopes, data, config keys. `docs/conversion-notes.md` — gaps, old-to-new mapping, operator and frontend notes.
- `frontend/` and `internal/webui/` — the React app and its embed (`webui.Assets()`).
- `companion/` — the macOS app. Its protocol (`/auth/pair/exchange`, `/auth/renew`, `/companion/*`) must not change.

## Commands

```sh
make test vet lint                  # go test -race, go vet, golangci-lint
make pins                           # go.mod pins match the Substrate BOM
make scan                           # secrets / hosts / private paths, tree and history
make generate                       # regenerate the wiring after editing substrate.yaml
make all                            # build the UI, then the binary with the UI embedded
```

## Boundaries

- Substrate supplies mechanism; Basalisk owns data and process. When something does not fit, add it to Substrate, remove it from the app, or record it under Gaps in `docs/conversion-notes.md`.
- Identity is the framework's. Accounts, sign-in, sessions, keys and tokens are the identity module; the auth guard enforces `auth.mode: enforce`. Do not rebuild them here. Companion devices are the exception: they use the libs `security/pairing` store and authenticate their own tokens.
- Every `/api/` route Basalisk serves is in the route table in `internal/api/api.go` with a scope (`basalisk.read` for GET, `basalisk.write` for changes). A handler without a scope fails closed; `scopes_test.go` keeps the inventory.
- The owner of domain data is the verified Principal subject from the request context, never a request field. Preferences, activity sessions and companion devices are per person; the rest is shared (single tenant).
- Never return stored API keys, secret references, password hashes or session secrets from any API. Integration credentials go through the secrets resolver.
- Pins are hand-edited only in Substrate's `bom/bom.yaml`; `go.mod` follows the Substrate version it requires (`make pins`).
- The schema is one migration, edited in place until the first release; there is no data migration from the old layout. The app is SQLite only.
- The repo is public. No secrets, internal hosts, internal addresses or personal paths in files, commits or CI (`make scan`). Tags and releases are the owner's decision.
- Preserve `.folio.yaml` and `chimera-provenance.json` as provenance until the owner decides.
- Build the UI before the binary (`make all`). Use disposable data directories (`BASALISK_DATA_DIR`) for browser tests so the user's first-run account is never created by an agent.
