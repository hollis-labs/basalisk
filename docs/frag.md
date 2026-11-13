# Frag

Quick capture into the Fragments Engine (FE), native to Basalisk. ⌥⇧F (command `frag.capture`)
opens a capture box over the desktop (headed `FRAG // BASALISK`, so it is never mistaken for the
standalone Frag app): text, comma-separated tags, ⌘↩ to commit, Esc to hide it. The status line
names where a capture is: "queued in Basalisk", then "saved to Fragments Engine →" and its chip.
⌘⇧P (or the footer switch) shows the note rendered as markdown, references live, and back; the
choice is remembered. The rendering is only a view: what is stored and sent is exactly the text
typed, markdown or plain, spaces and newlines included. The clip box's note works the same way.
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

## Context

When a draft begins, the box notes the moment: the active work session and the window that had
focus (its app and title, and what the app can say: the folder of a Files window, the file of a
Notes window, the page of a Browser window, the directory of the terminal). A kept draft keeps
the moment it began. At save the window is read again, and the server reads the session itself.

The server keeps a draft session only if it is the owner's, and records the save session from
its own state, never from the request. The context is stored with the queued capture
(`ownerContext`, shown in `GET /api/frag/captures`) and never sent to FE. Once FE confirms the
capture, the fragment is listed in the session's artifacts
(`/api/activity/sessions/{id}/artifacts`), under the draft's session or, without one, the
session it was saved in. While a draft belongs to a session the status line says so.

## A saved fragment as a reference

A saved fragment is a cite reference of kind `fragments.fragment`: `tach:fragment/<id>` (also as
a Markdown link target) and links to FE's reader (`https://<fe>/sysop/reader/<id>`) become a chip
with a preview card. Ids are 64 hex characters, so a bare id is never taken. The capture box
shows the chip once a capture is saved, and so does the outbox.

The server resolves it from FE's reader (`GET /v1/reader/items/{id}`) with the delivery
credential: a token exchanged for `fragments-engine.read`, or in personal mode the integration's
key. It does not go through the Tether gateway. The cite routes exist only when the gateway is
configured (`basalisk.tether_mcp`) for the gateway kinds; fragments need only a delivery mode, and
`/api/cite` lists only the kinds this process can resolve. FE serves the reader to loopback
peers only, so this works where FE is reached through a proxy on its own host.

## Clipping a page

In a Browser window, the ✂ button or ⌥⇧C (command `frag.clip`) extracts the page and opens the
box in clip mode: the title (editable), the address, the selection if there is one, a preview of
the body, a note and tags (prefilled with `link`). ⌘↩ queues it like any capture.

The extraction is fe-clipper's (Readability, turndown and its site adapters for YouTube, Reddit,
X, Pinterest and Instagram), bundled into `frontend/public/clip/extract.js` by
`scripts/build-clip-bundle.sh` from fe-clipper commit `beb449a270e061d5fc1c0df6e00997542fd49cf8`
(licenses in `licenses/`; never edit the bundle, change the pin and rebuild). The window runs it
in the page through the view's `eval` over the page channel: the window acting for the person,
not an agent API call, so it is not written to the shell log.

The capture goes to FE as the same `fe.capture.v1` page manifest fe-clipper sends
(`source_registration_id` `browser-capture`, the page's submitted and canonical addresses, the
same item key), so a page clipped here and one clipped from the extension are one source. What
is sent is the selection when there is one (as the document body, with no separate highlight),
and the whole page otherwise, never edited; the box says which ("sending: selection" or "sending:
whole page") and shows it. The note is a `capture_note` annotation. No media yet. The capture id belongs to the page,
note, selection, title and tags, so clipping the same page with the same note again is the same
capture.

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
POST /api/frag/captures   {captureId, content, tags, draftAt, submittedAt,
                           context?: {draft?: {session?, window?}, save?: {window?}},
                           page?: {title, submittedUrl, canonicalUrl, markdown, selection, ...}, note?}
                          -> 202 queued | 200 the same capture | 409 the id has other text
GET  /api/frag/captures   -> {items: [status + content, tags, context]}, newest first, 50
```

`context` is up to 16 KiB of JSON kept with the row and never sent to FE.
