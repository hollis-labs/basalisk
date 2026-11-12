# Basalisk

A personal dashboard for your data, tools and connections: a Go backend on the
[Substrate](https://github.com/hollis-labs/substrate) framework, an embedded React frontend, and a macOS
companion app.

> **Pre-release.** Basalisk is unreleased and has no outside consumers. It is built in the open: the code, the
> docs and this README describe what exists today, not a pitch for what is planned. Interfaces, data formats and
> behavior change without notice, and there are no compatibility guarantees, support channel or tagged releases
> yet. Do not expose it beyond a network you control.

## Build and run

Requires Go 1.26.10 (the `go` line in `go.mod`) and Node 22.

```sh
make all        # builds the frontend into internal/webui/dist, then the ./basalisk binary with it embedded
./basalisk      # same as ./basalisk serve
```

Open **http://127.0.0.1:8085**. On an empty instance the page asks for a display name, username and password.
That creates the first account (it holds every scope), closes setup, and signs you in. No default account is
installed.

- The server listens on `127.0.0.1:8085` and refuses a non-loopback address. A remote client reaches it through
  an authenticated tunnel or a local TLS proxy.
- Data lives in `$XDG_DATA_HOME/basalisk` (else `~/.local/share/basalisk`), or in `BASALISK_DATA_DIR` when set.
  The directory is private (0700) and holds `basalisk.db` (0600), `identity-signing.key` and `secrets/`.
- Other commands: `basalisk version`, `basalisk config` (effective config, redacted) and `basalisk health`.
- Configuration is the manifest `cmd/basalisk/substrate.yaml` plus an optional YAML file named by the
  `SUBSTRATE_CONFIG` environment variable. A key in that file overrides the manifest. For example:

  ```yaml
  httphost:
    addr: 127.0.0.1:8085
  identity:
    session_ttl: 12h
  basalisk:
    allowed_hosts: [basalisk.example.test]   # served behind a local TLS proxy; also set identity.trusted_proxy: true
  ```

  `docs/architecture.md` lists every key and its default.
- There is no migration from the pre-Substrate version. If startup stops with "move the old data directory
  aside", do that and run first-run setup again (see `docs/conversion-notes.md`).

## What is available now

- **Account and preferences**: first-run setup, sign-in and sign-out, password change, display name and email,
  theme and time zone.
- **Integrations**: API-key records with a name, provider and optional HTTPS base URL; write-only key entry,
  replacement and deletion. A record is credential inventory only: saving does not contact the provider.
- **Work sessions**: start, set project and workstream context, pause, resume and end; immutable context
  history and paged session and event lists (`docs/activity.md`).
- **Devices**: one-use pairing codes, revocation, and recent companion observations. The Swift menu-bar
  companion is in `companion/` (`docs/companion.md`).
- **Fragments delivery**: notes captured through a paired device are queued, shown in an outbox with retry and
  discard, and delivered to a Fragments Engine integration when the delivery settings are set
  (`docs/fragments.md`).
- **Capture readiness**: `GET /api/capture-readiness` reports, per device, its authorization, Fragments
  connection, delivery worker state and queue counts.

## What is not

- **No provider adapters.** There is no OAuth flow, outbound provider call or third-party sync. The only
  outbound delivery is the optional Fragments worker.
- **No passkeys or operation assurance.** The WebAuthn backend was removed in the Substrate conversion; a
  follow-up restores it from git history.
- **`/api/data/*` is not served.** The reader packages (`internal/dataread`, `feread`, `tesseractread`,
  `fecapture`) are present with their tests, but no route is registered for them.
- No multi-tenant model, password recovery or account reset, and no encryption at rest (`docs/security.md`).

## Auth model

- Accounts, sessions, API keys and tokens belong to Substrate's identity module; the app runs with
  `auth.mode: enforce`, so every `/api/` route needs a verified credential and its scope.
- A browser holds the `basalisk_session` cookie (HttpOnly, SameSite=Strict); sign-in lasts 12 hours.
- Cookie-authenticated writes must send an `X-Requested-With` header and a same-origin `Origin`. Requests that
  carry their own `Authorization` header (API keys, tokens) are exempt.
- Routes need `basalisk.read` (GET) or `basalisk.write` (changes). The first account holds `*`.
- Companion devices are not identity users: they pair with a one-use code and present their own device token on
  `/auth/*` and `/companion/*`.
- Errors are flat JSON, `{code, message, details?}`.

## Develop

```sh
make test vet lint     # go test -race, go vet, golangci-lint
make pins              # go.mod pins match the Substrate BOM
make scan              # secrets / hosts / private paths, tree and history
make generate          # regenerate the wiring after editing cmd/basalisk/substrate.yaml
make ui-dev            # Vite dev server, proxies /api, /auth and /companion to 127.0.0.1:8085 (run `make run` too)
```

Use a disposable `BASALISK_DATA_DIR` for experiments: setup creates the first account once, and there is no
account reset.

## Documentation

- [`docs/architecture.md`](docs/architecture.md): layout, request path, auth, scopes, data, config keys.
- [`docs/conversion-notes.md`](docs/conversion-notes.md): the move to Substrate, gaps, old-to-new mapping.
- [`docs/activity.md`](docs/activity.md): work sessions.
- [`docs/companion.md`](docs/companion.md): device pairing and the observation API.
- [`docs/fragments.md`](docs/fragments.md): capture, delivery and provenance.
- [`docs/security.md`](docs/security.md): data and security notes.
- [`companion/README.md`](companion/README.md), [`frontend/README.md`](frontend/README.md).
- [`AGENTS.md`](AGENTS.md), [`CONTRIBUTING.md`](CONTRIBUTING.md), [`SECURITY.md`](SECURITY.md),
  [`CHANGELOG.md`](CHANGELOG.md).

## License

See [`LICENSE`](LICENSE) and [`TRADEMARK.md`](TRADEMARK.md).
