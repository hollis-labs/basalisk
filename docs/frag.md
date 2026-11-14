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
is sent is the whole page as the body, never edited, and the selection, when there is one, as a
`highlight` annotation (`{kind: highlight, text, selector: {exact}}`), exactly as fe-clipper
sends it. The box says so ("sending: whole page + highlight") and shows the selection. Until
2026-11-13 a selection was sent as the body instead; Chrispian chose to match the extension,
pending a deeper look he wants later. The note is a `capture_note` annotation. The capture id belongs to the page,
note, selection, title and tags, so clipping the same page with the same note again is the same
capture.

### Before the extraction

Two kinds of page give fe-clipper's adapters less than they show, so the clip gets them
ready first (`frontend/src/apps/frag/prepare.ts`). If anything fails, or takes more than
about four seconds, the page is clipped as it is: never worse than without this.

- **YouTube**: after navigating within YouTube (a click from search or a recommendation),
  the page's serialized state (`ytInitialPlayerResponse`, `ytInitialData`) still describes
  the page first loaded. The adapter reads that state, so it found no captions for the video
  shown (`youtube.captions_unavailable`). The extension has the same limit.
  - The clip acts only on a `youtube.com/watch` page whose state is not for the current
    video id; a video loaded directly is left alone.
  - It fetches the current watch address from the page (same origin, nothing else).
  - It takes the two state objects out of that HTML as JSON and checks that the player
    state is for this video id.
  - It puts them first in `<head>` as non-executing `type="application/json"` script
    elements, which the adapter reads by id, and removes them after the extraction in every
    case. Nothing from the fetched page is executed or rendered.
  - When fe-clipper's adapter gains this itself, this part can go.
- **Reddit**: on a post's page (`/comments/`), comments render as the post scrolls in. The
  clip waits up to about three seconds for the first one before extracting.

### Media and provider metadata

What fe-clipper's adapters find besides the text goes too, mapped as fe-clipper's
`envelope.ts` maps it (`internal/api/frag_media.go`):

- **Media**: images (the page's hero, then the article's images, with alt text, caption and
  nearest heading), Pinterest pins, Instagram galleries, video posters and subtitles. A video
  stream (YouTube, Instagram) is a reference, never fetched. The box says what goes along
  ("sending: whole page + 3 images").
- **Provider metadata**: `provider.*` extensions, such as a YouTube transcript and chapters
  or an X or Reddit thread's structure. Each goes under `document.extensions` or
  `extraction.extensions` by its placement. Keys named like credentials, and such
  parameters in addresses, are removed first.

FE answers each media variant with an instruction. For `request_upload` the delivery worker
fetches the file itself:

- no cookies, no referrer, no FE token and nothing else of the owner's;
- http and https only, no credentials in the address;
- 30 seconds for the whole fetch, at most 5 redirects;
- at most 64 MiB, or FE's own `max_asset_bytes` (`GET /v1/capabilities`) when smaller,
  counted on the bytes read, whatever length the response declares;
- **public addresses only**, checked on the address actually connected to on every hop:
  loopback, private, link-local, tailnet (100.64/10), unique-local, unspecified,
  multicast and their IPv4-mapped forms are refused, because the addresses come from a web
  page;
- ports 80 and 443 only, plus the page's own port when its address named one;
- the type is decided from the bytes.

Files go four at a time. A clip with many files (a captioned video has one per caption
track) may take more than one delivery attempt (40 s each). Each file's outcome is saved
with the row as soon as it is known, and the next attempt goes on at once with the rest
(outbox code `media_in_progress`), so no file is fetched twice. No file is started with
less than 15 s left in an attempt. After 20 attempts that end with files still to send,
those files are reported as failed and the capture completes. An empty answer is a failure,
as in fe-clipper. YouTube's caption addresses answer empty to a fetch without the player's
own token, so captions arrive as failed variants; the extension gets the same.

It then PUTs each file to FE with its `Digest` and completes the capture with one outcome per
variant: `uploaded`, `already_available`, `deferred`, `not_available` or `failed`. A file
that cannot be had is a failed outcome, and FE marks the capture `partial`. The bytes are
held in memory for the upload and never written down; what is kept at rest is still the
markdown and the media's addresses. A failure is recorded as a plain reason, never with the
address.

The upload goes only to the path FE gave for that variant on the configured FE origin
(`/v1/captures/{id}/assets/{variant}/content`); an instruction pointing elsewhere makes the
receipt invalid. Once FE confirms the capture, the outbox says how the media went ("Media: 3
files sent, 1 file kept as a link, 1 file not sent (the media answered 404)"). The counts are
kept with the row's receipt.

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

## Known limits

- **Trusted Types.** The extraction runs in the page's own JavaScript world (the view's
  `eval`). WebKit on macOS 15 has no Trusted Types, so this is fine today. A page that
  requires them (YouTube does) makes the bundle's `innerHTML` writes fail where they are
  enforced: headless Chrome already shows `capture.extraction_failed` there. A future WebKit
  that enforces them would need the bundle evaluated in a named `WKContentWorld`, as an
  extension's content script is.
- **Subtitles.** A captioned YouTube clip carries one subtitles variant per caption track,
  each fetched and uploaded, as the extension does (31 on one test video).
