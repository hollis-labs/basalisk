# Activity sessions

A work session is a durable record of what you are working on. The web app's **Work sessions** page creates and
changes them; the companion app (`docs/companion.md`) attaches device observations to the open one.

## Behavior

- Each person has at most one open session (status other than `ended`). It can span project switches. A paused
  session is still open; an ended session stays ended.
- Sessions belong to the signed-in person: every read and change is scoped to the verified identity subject.
  Signing out, changing the password and sign-in expiry leave sessions and their history intact.
- Each context change creates a new immutable snapshot. Project and workstream are user-declared labels (blank
  means unknown), not verified project identifiers. Lifecycle events keep the snapshot they used, the actor,
  a source and server timestamps. Manual commands record the same occurred-at and received-at time.
- Session IDs are correlation handles, not credentials. The database keeps an installation id
  (`activity_identity.installation_id`) that is independent of the username, the server address and login
  cookies, and moves with a consistent backup of the data directory. Two copies of the database running at the
  same time are not a synchronized cluster.
- Companion observations are admitted only for an active session at their reported time. Paired devices have
  independent IDs and sequence streams; observations from different devices do not establish one global order.

## API

All routes need `basalisk.read` (GET) or `basalisk.write` (POST). Cookie-authenticated writes also need the
`X-Requested-With` header (see `docs/security.md`).

- `GET /api/activity`: the installation identity and the open session, or `current: null`.
- `GET /api/activity/sessions`: newest sessions, 20 per page.
- `GET /api/activity/sessions/{id}/events`: newest events, 50 per page.
- `GET /api/activity/sessions/{id}/artifacts`: fragments saved from the session (`docs/fragments.md`).
- The two lists return `items` and, when more exist, a cursor to pass as `?before=...`. Sequence numbers order
  local records only.
- `POST /api/activity/commands` with `requestId` (16 to 128 bytes) and `action`:
  - `start` takes `title` plus optional `project` and `workstream`.
  - `context` replaces both labels; `pause`, `resume` and `end` change the lifecycle state. These take
    `sessionId` and the current `revision`.

Use a fresh random request ID for each intent and reuse it, with the same payload, for network retries: the
original result is returned. Reusing an ID with a different payload, a stale revision or a conflicting
transition returns 409. Session state, context, event and retry receipt commit atomically.

## Retention

The manual journal is kept until the data is removed; there is no session deletion UI. Raw companion
observations have their own five-minute retention (`docs/companion.md`). No events leave Basalisk.
