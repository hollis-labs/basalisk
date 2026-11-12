# Changelog

All notable changes to Basalisk are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Releases are tagged `0.x`; there is no compatibility promise before 1.0, and breaking changes can land in any release.

## [Unreleased]

### Changed

- Rebuilt on [Substrate](https://github.com/hollis-labs/substrate): the manifest `cmd/basalisk/substrate.yaml` selects the framework modules, and accounts, sign-in, sessions, API keys and tokens are now the Substrate identity module.
- The frontend is rebuilt on the app-shell with the published design kits.
- The default port is 8085 (was 8080).
- Configuration comes from the manifest and an optional YAML file named by `SUBSTRATE_CONFIG`. The `basalisk:` block holds `allowed_hosts` and the Fragments delivery keys; see `docs/architecture.md`.
- API errors are flat `{code, message, details?}` bodies. Sign-in bodies use snake_case fields (`display_name`, `current_password`, `new_password`).
- Writes with the session cookie send `X-Requested-With` (replacing `X-Basalisk-Request`) and a same-origin `Origin`.
- `/api/` routes require the scopes `basalisk.read` (GET) and `basalisk.write` (changes).
- Activity sessions and preferences belong to the verified identity subject.

- Pinned Substrate to main 09c04e5. The app's pages come from `frontend.contributions` (`cmd/basalisk/app.ui.json`) instead of a client-side merge; the `basalisk:` defaults are in `substrate.yaml`; the data-directory paths are set in the `AfterConfig` boot hook and the pre-Substrate database guard runs as `Before["database"]`; `GET /api/account` reads the user through the identity `Service.User`. An override file may now set only `database.sqlite.path`. A relative database, key or secrets path (from the manifest or the override) is joined onto the data directory, so `database.sqlite.path: other.db` is `<data>/other.db`; an empty one gets the default name; an absolute one is kept; a relative one that climbs out of the data directory through `..` stops startup. Only the files the final configuration uses are created: an overridden database leaves no empty `<data>/basalisk.db`, and the database file is created with mode 0600 wherever it is. A relative path with a subdirectory (`db/main.db`, `keys/sign.key`) creates that directory (0700) inside the data directory.

### Removed

- The assurance and WebAuthn backend, `/api/assurance/*` and the `/assurance/` status mount. A follow-up restores them from git history.
- `GET /api/identity` (humanIdentity).
- The Folio/Chimera host and the `requestorigin` package (replaced by `internal/hostguard` and the identity module's `trusted_proxy`).
- The `BASALISK_*` environment variables except `BASALISK_DATA_DIR`, and `LISTEN_ADDR`; see the mapping in `docs/conversion-notes.md`.
- `deploy/`: the Caddy fragment and the Cerberus example (deployment descriptors belong with the operator's infrastructure).

### Breaking

- New database schema with no migration. Startup refuses a database from the earlier version: move the old data directory aside and run first-run setup again.
- Paired companion devices pair again, and integration credentials are entered again.
- API request and response shapes changed as listed in `docs/conversion-notes.md`.

## [0.1.0] - 2026-10-20

First tagged release (the baseline tag; later changes are listed under the next version).

### Security

- Go toolchain 1.26.10 (`go` directive and CI pin) for GO-2026-6809 (crypto/x509); no behavior change.

### Changed

- **Data now lives under `XDG_DATA_HOME` and configuration under `XDG_CONFIG_HOME`**, resolved with the shared `apppaths` library, instead of `XDG_STATE_HOME`. `BASALISK_DATA_DIR` still overrides the data directory and `BASALISK_CONFIG_DIR` the configuration directory. **Breaking for existing installs:** with a database in the old state directory and none in the new data directory, startup is refused with the fix named, so an empty installation is never opened silently. Move `~/.local/state/basalisk` to `~/.local/share/basalisk` (app stopped) or set `BASALISK_DATA_DIR`.
- The assurance host configuration defaults to `assurance.json` in the configuration directory when present; `BASALISK_ASSURANCE_CONFIG` still takes precedence.

### Added

- Initial public commit: single-owner Go service with an embedded React GUI (first-launch owner creation, password login, account and preference settings).
- Portable human activity sessions with immutable provenance, a paired-device companion API, and a local Swift macOS companion (`companion/`).
- Fragment capture broker: signed local XPC, device and integration grants, durable capture journals and an owner outbox. Delivery stays off until configured.
- Operation-assurance backend (WebAuthn, `operation-webauthn-v1-draft`), integration pending.
- Open-source project documents: `CHANGELOG.md`, `CONTRIBUTING.md`, `SECURITY.md`, `TRADEMARK.md`, and an example Cerberus descriptor in `deploy/`.
