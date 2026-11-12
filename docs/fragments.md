# Fragments: capture, delivery and provenance

Notes captured on a paired device are queued in Basalisk and delivered to a Fragments Engine (FE) integration.
The path is: capture app, signed local companion, paired Basalisk device, FE's public capture API. Delivery is
optional and off until configured. The Swift companion and its local capture bridge are described in
`companion/README.md` (see its Frag context bridge section for freshness, privacy and retry semantics).

## Configuration

Delivery is configured in the `basalisk:` block of the file named by `SUBSTRATE_CONFIG` (these were the
`BASALISK_*` environment variables before the Substrate conversion).

| Key | Value |
|---|---|
| `basalisk.fe_integration_id` | ID of the integration (provider `fragments-engine`) the worker serves |
| `basalisk.public_origin` | Stable `https://` origin of this Basalisk, used in the source URL sent to FE |
| `basalisk.tether_issuer_url` | HTTPS origin of the credential issuer |
| `basalisk.tether_subject` | The stable service subject the capture credential is issued for |
| `basalisk.tether_parent_ref` | Secret reference of a dedicated capture-only enrollment parent |

The worker runs only when all five are set. Setting some of them, an unknown key or an invalid value stops
startup. The integration's stored API key is not used by this worker. The capture credential is requested for
audience `fragments-engine` with the single scope `fragments-engine.capture` (900 seconds).

## Delivery

- One configured integration is served per process. Issuer requests use normal TLS validation, refuse
  redirects and have a ten-second limit. Each worker attempt has a forty-second deadline and a two-minute
  durable lease, so another process can reclaim only an expired lease.
- Transient failures back off from five seconds to five minutes and never delete the note. Authorization,
  identity, conflict and invalid-receipt failures wait for owner review; the outbox retries after correction
  (`POST /api/fragment-submissions/{id}/retry`).
- Before sending, Basalisk fixes a random upstream capture ID, the source URL and the complete manifest in
  SQLite. The source URL points at the signed-in submission detail route
  (`/api/fragment-submissions/{sequence}` under `public_origin`); FE does not fetch it.
- A capture request may be up to 128 KiB (content up to 64 KiB, at most 32 tags).
- The submission is stored with a payload hash: a repeated capture ID with the same payload returns the original
  result, a different payload conflicts.

## Outbox

- `GET /api/fragment-submissions` (newest first, paged), `GET /api/fragment-submissions/{id}`,
  `POST /api/fragment-submissions/{id}/retry` and `DELETE /api/fragment-submissions/{id}`.
- Deleting is explicit. Before any send it discards the note; after a possible send it stops further delivery
  and keeps a tombstone (`owner_stopped_delivery`) because FE may already hold a copy. It never deletes remote
  content. It is refused with 409 while a delivery is active or the note is already saved.
- Logical deletion is not secure erasure of the WAL or backups.
- `GET /api/fragment-connections` lists, and `PUT` or `DELETE /api/fragment-connections/{id}` sets or removes,
  the integration a device delivers to. `GET /api/capture-readiness` reports per-device state.

## Provenance

- **Frag session provenance.** The companion publishes a private, short-lived context snapshot for the capture
  app (`companion/README.md`). Basalisk stores the draft and save context with the submission, sends it to FE
  in `document.extensions["provider.basalisk"]` as correlation evidence (not authorization), and shows
  **Saved fragments** within each work session (`GET /api/activity/sessions/{id}/artifacts`).
- Receipts a device reports itself (`POST /companion/artifacts`) are stored with the source
  `frag.fe-response (device-reported)`.

## Nil v2 capture metadata

`POST /companion/fragments` accepts optional `metadata` with `schemaVersion: 1` and a required
`client: {name, version}`. It can carry a title, a kind (`note`, `todo` or `scratch`), projects, contexts, a
priority (`A`, `B` or `C`), a due date, typed mention and link references and the applied preset identity.
Markdown stays in `content`; HTML is not accepted as metadata. The fields are client assertions, never grants
or instructions to fetch links. The worker preserves them in
`document.extensions["provider.basalisk"].capture_metadata` and uses the supplied title in FE. Requests without
metadata keep their original shape. A retry must repeat the complete operation, including metadata.

## Independent capture apps

A capture app (such as Nil) pairs separately from the Mac companion; do not reuse the companion's credentials.

- In Devices, **Capture app (Nil)** creates a grant with `basalisk.activity.read` and
  `basalisk.fragments.create` only, so it cannot publish focus observations. The grant API takes
  `profile: "capture"`; omitting it keeps the companion profile. Give the app its own fragment connection after
  pairing.
- `GET /companion/capture-capabilities` reports the supported metadata and context versions, limits and paths.
  It does not report delivery readiness (use `GET /companion/capture-readiness`).
- At the start of a draft, `POST /companion/capture-context` with `{captureID}` returns a durable `bindingID`,
  the server `draftAt` and an optional frozen context. Repeating the capture ID returns the original binding.
  Submit `contextBindingID` and the exact `draftAt` with the fragment, and omit raw `draft` and `saveContext`;
  Basalisk loads the snapshot itself. Device, capture, origin and timestamp must match. Context cannot be added
  after submission.
- Offline or unavailable context may be omitted; paused or no-session bindings carry no snapshot. A binding
  made on reconnection describes that later time, not when an offline draft began.
- Bindings survive restarts and session changes, and are bounded at 1,000 per device and 10,000 in total.
  Nothing deletes bindings automatically.

## Tests

`go test -race ./...` covers the worker with simulated peers. Setting `BASALISK_TEST_FE_BINARY` to a
scoped-capture-capable FE executable additionally runs a test against a real FE process with disposable
databases and generated credentials; it never contacts a production FE or issuer.
