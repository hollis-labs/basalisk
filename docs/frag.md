# Frag

Quick capture into the Fragments Engine (FE), native to Basalisk. ⌥⇧F (command `frag.capture`)
opens a capture box over the desktop: text, comma-separated tags, ⌘↩ to commit, Esc to hide it.
The draft is kept in this browser's local storage until it is committed, so hiding the box or
reloading the page loses nothing.

## The path of a capture

1. ⌘↩ queues the capture on this Basalisk server (`POST /api/frag/captures`) in the same
   durable queue as a paired device's notes (`fragment_submissions`), with
   `device_id = "owner:<user>"` and no integration yet. Once it is queued the draft is done: a
   capture is never lost, and capturing works offline.
2. The delivery worker binds it to the default FE integration and sends it as an
   `fe.capture.v1` manifest to `POST /v1/captures`, then `/v1/captures/{id}/complete`, with the
   same retries, receipts and idempotency as device notes. The capture appears in the Fragment
   outbox (marked "Frag ·"), where it can be reviewed, retried or discarded.
3. FE receives the content and tags exactly as typed. Anything Basalisk knows about the moment
   (the work session, the focused window) stays in Basalisk, in the queued row.

The capture id belongs to one text: it stays the same while the content and tags do, so a
second ⌘↩ answers with the same row (200), and the same id with different text is refused
(409; the box then takes a new id).

## Delivery modes

- **Exchange**, when the five delivery keys are configured (`fe_integration_id`,
  `public_origin`, `tether_issuer_url`, `tether_subject`, `tether_parent_ref`; see
  docs/fragments.md): the credential is exchanged at the Tether issuer for a short-lived
  scoped token, as for device notes.
- **Personal**, when they are not: the owner's Frag captures go out with the FE integration's
  own stored key (Settings › Integrations, provider `fragments-engine`, the FE base URL and the
  API key), and the principal is `local-user`. Setup is adding that integration; with exactly
  one, it is used. Paired devices' notes are never sent this way; they need the exchange.

The key is read through the secrets resolver at delivery time and is never logged, stored in a
row or sent to the page. `GET /api/frag` reports the mode and the integration in use.

## API

```
GET  /api/frag            -> {mode: exchange|personal|off, integration: {id, name} | null}
POST /api/frag/captures   {captureId, content, tags, draftAt, submittedAt, context?}
                          -> 202 queued | 200 the same capture | 409 the id has other text
GET  /api/frag/captures   -> {items: [status + content, tags, context]}, newest first, 50
```

`context` is up to 16 KiB of JSON kept with the row and never sent to FE.
