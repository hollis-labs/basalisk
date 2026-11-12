# Companion pairing and observation API

The Swift macOS companion lives in `companion/` (build and pairing steps: `companion/README.md`). It collects
only app name, bundle ID, activation time and sleep/wake events, and only while a work session is active
(`docs/activity.md`). The protocol below is fixed: the companion app speaks these paths and they do not change.

## Identity

Companion devices are not Substrate identities. They use the libs `security/pairing` store (protocol v1, pinned
in the Substrate BOM) and present their own device token. The `/auth/` and `/companion/` mounts are open to the
auth guard because the handlers verify that token; a browser session never authenticates them. Basalisk does
not mint mesh tokens for devices.

## Owner routes

Signed-in, scope `basalisk.read` or `basalisk.write`, cookie writes with `X-Requested-With`. Device
credentials cannot use these routes.

- `POST /api/devices/grants`: create a one-use pairing grant `{label, allowFragments?, profile?}`. `profile` is
  `companion` (the default) or `capture` (`docs/fragments.md`). `DELETE /api/devices/grants/{id}` revokes one.
- `GET /api/devices` lists devices; `DELETE /api/devices/{id}` revokes one.
- `GET /api/observations`: the most recent 100 observations still inside retention.

Signing out or changing a password does not revoke paired devices; revoke each device here.

## Device routes

| Route | Scope | Purpose |
|---|---|---|
| `POST /auth/pair/exchange` | none (grant code) | exchange a pairing code for a device credential |
| `POST /auth/renew` | device token | renew a credential that is still valid |
| `GET /companion/context` | `basalisk.activity.read` | current session and context, server `asOf`, `retentionSeconds` |
| `POST /companion/observations` | `basalisk.observations.write` | submit observations |
| `POST /companion/artifacts` | `basalisk.observations.write` | save-receipt artifacts (`docs/fragments.md`) |
| `GET /companion/capture-capabilities`, `GET /companion/capture-readiness`, `POST /companion/capture-context`, `GET /companion/fragment-connection`, `GET` and `POST /companion/fragments`, `GET /companion/fragments/{id}`, `POST /companion/fragments/{id}/retry`, `POST /companion/fragments/{id}/stop` | `basalisk.fragments.create` (see `docs/fragments.md`) | capture and delivery |

- The `/auth/` routes require `Hollis-Pairing-Protocol: 1`. Exchange and renew bodies are limited to 4 KiB.
  Exchange allows ten attempts per minute for the process and answers 429 beyond that.
- Pairing codes expire after five minutes; device credentials after thirty days, and can renew while valid. After
  expiry or revocation the device pairs again. These are the pairing library's defaults; the app does not
  override them.
- Every `/companion/` response is `Cache-Control: no-store`. A request with a foreign `Origin` or
  `Sec-Fetch-Site: cross-site` is refused with 403.
- Request bodies are limited to 16 KiB, except capture requests (`docs/fragments.md`).

## Observations

`POST /companion/observations` takes a `generation` (16 to 100 bytes) and 1 to 20 `events`. Each event has
`sequence`, `occurredAt`, `kind` (`app.activated`, `system.sleep` or `system.wake`), `bundleId`, `appName`,
`activitySessionId`, `contextId` and `contextAsOf`.

- The context must be at most 30 seconds older than the event, and an event more than 30 seconds in the future
  is rejected.
- A repeated sequence with the same payload is accepted; reuse with different data returns 409.
- Events for an unknown or ended session, a different context, or a session that is not active are acknowledged
  as ignored. An event for a session owned by someone else is refused with 403.
- A device cannot choose another device's ID or the server's receipt time.

Observations are returned for five minutes and purged every thirty seconds. That is logical retention, not
forensic erasure of SQLite pages or backups.
