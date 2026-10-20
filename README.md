# Basalisk

A personal operating system for your data, tools, and connections. This first
slice is a single-owner, local web application: Go serves an embedded React GUI
through Hollis Labs Chimera.

> **Pre-release.** Basalisk is unreleased and has no outside consumers. It is
> built in the open: the code, the docs, and this README describe what exists
> today, not a pitch for what is planned. Interfaces, data formats and behavior
> change without notice, and there are no compatibility guarantees, support
> channel or tagged releases yet. It is a single-owner application with no
> multi-user model; do not expose it beyond a network you control.

## Run

Requires Go 1.26.9+ and Node 22.12+.

```sh
make all
./basalisk
```

Open **http://127.0.0.1:8080**. On an empty instance, the GUI asks for a display
name, username, and password. Creating the owner closes setup permanently for
that data store and signs you in. No default account or password is installed.

`LISTEN_ADDR` changes the port, for example `127.0.0.1:8085`. The server requires
a loopback address and rejects non-loopback Host headers by default.
`BASALISK_HTTPS_PROXY=1` additionally accepts exactly `basalisk.nanite.cloud`
from a loopback proxy that supplies a single `X-Forwarded-Proto: https` value.
The proxy must preserve Host and Origin and overwrite the protocol header.
This supplies the trusted HTTPS context for secure, host-only session cookies
and exact-origin checks. Other forwarded headers are stripped before routing;
they never choose caller identity, host, or WebAuthn origin. Local HTTP remains
available for development. The flag does not install a proxy or activate passkeys.

The agreed permanent browser origin is **https://basalisk.nanite.cloud**, with
WebAuthn RP ID **basalisk.nanite.cloud**, initially reachable only on the private
network. `deploy/Caddyfile.fragment` is prepared for the existing private Caddy
wildcard site. Set its `BASALISK_UPSTREAM` to the managed service's loopback
address when deploying. `deploy/cerberus.example.yaml` is a generic
[Cerberus](https://github.com/hollis-labs/cerberus) descriptor for running the
installed binary as a supervised service; copy it to your own config directory
and fill in your paths. Your real descriptor, environment and data stay out of
this repository.

## Available now

- First-launch owner creation; password login and logout.
- Account administration: display name, optional email, appearance, timezone.
- Password changes with current-password confirmation and session revocation.
- API-key integrations: name, provider, optional HTTPS base URL, write-only key
  entry, key replacement, and confirmed deletion. Timestamps use your timezone.
- Responsive desktop/mobile shell using the Hollis Labs design system.
- Work sessions: start, set project/workstream context, pause, resume, and end;
  immutable context history with paginated session and event lists.
- Devices: one-use pairing codes, revocation, and recent companion observations.
  Swift menu-bar companion source is in `companion/`. Native release build,
  launch, Keychain and transport checks pass on the home Mac; interactive
  collection and reconnect acceptance remain to be completed.

Integration records are credential inventory, not active provider adapters.
Saving does not contact the provider or verify the key. Removing or replacing a
stored key does not revoke it at the provider. No OAuth connection flow, outbound
API call or third-party sync is implemented yet. Paired-device credentials are
limited to the companion's context-read and observation-write endpoints.

## Operation assurance backend (integration pending)

`internal/assurance` implements the Basalisk side of the
shared `operation-webauthn-v1-draft` contract. It uses the published
`github.com/hollis-labs/libs/security/webauthn v0.1.0` and its SQLite store.
The ordinary password login remains available. `GET /api/identity` returns the
persisted `activity_identity.installation_id` as `authority` and `user_id` as
`human_subject`, labeled `password-session`. Neither value is a credential or
operation approval. Browser and future CLI handoffs resolve through the same
current account-session lookup; this slice does not add a CLI login command.

The executable configures operation assurance only when
`BASALISK_ASSURANCE_CONFIG` names a valid host JSON configuration and
`BASALISK_HTTPS_PROXY=1`. `Store.ConfigureAssuranceHost` installs the authenticated
Tachyon item/delivery/outcome client, Tether current-trust client and status JWT
verifier. Configuration refuses a different installation authority, inconsistent
issuer pins, shared reader/parent file references and silent RP/origin changes.
The loopback listener is not itself a TLS server. No configuration or production
credential files are installed by this source change.

Once configured, the account handler supplies these session-protected routes:

| Route | Body / behavior |
|---|---|
| `POST /api/assurance/prepare` | `attempt_id`, `item_id`, `expected_revision`, `key_ref`; loads the authoritative item and returns its operation, server binding, and WebAuthn options |
| `POST /api/assurance/finish` | `completion_id`, `assertion` (original browser credential JSON); returns completion reference and trusted verification time after durable verification |
| `POST /api/assurance/cancel` | `completion_id`; revokes only this session's ceremony/completion, without resolving or withdrawing a Tachyon item |
| `POST /api/assurance/deliver` | `completion_id`; sends the saved item/completion/revision to Tachyon using the initiating session and a current completion |
| `POST /api/assurance/outcome` | `completion_id`; reads the saved item's authoritative Tachyon outcome; a current session for the same stable human suffices after proof/original-session expiry |

These routes return 503 until configured. The existing exact-Origin/custom-header
checks still apply. Prepare and finish must use the same current owner session
and configured HTTPS origin. The selected public credential belongs to that
stable human; the browser cannot supply the operation, authority, or human.
The returned operation is the source of approval presentation. No ceremony UI,
CLI handoff, or Tachyon browser-session migration is installed in this slice.

Canonical operation bytes use RFC 8785 JCS, independently of go-hitl's envelope
fingerprint. The domain-separated challenge binds the operation digest, item,
operation revision, audience, human, selected key, decision, random nonce, proof
ID, and lifetime. Expiry is at most five minutes from prepare and no later than
operation, approval or initiating-session expiry. Exactly-at-expiry fails.

The shared SQLite transaction spends the ceremony and commits the counter and
original verified assertion together. The separately prepared immutable binding
already exists before verification. Together these form the durable completion.
Exact finish retries recover that same receipt and do not advance counters again.
Before the shared store prunes any committed receipt, a retaining wrapper archives
it; archive failure prevents pruning, and a failed CAS can archive only an already
committed receipt. Historical evidence stays retained but cannot authorize after
expiry, logout, password change, key revocation, or ceremony cancellation.
Permanent key tombstones prevent re-enrollment from reviving old proofs.

Successful prepare responses are retained for exact retry. An ambiguous failure
before options were retained requires an explicit new `attempt_id`; this creates
a new nonce/challenge. Retrying an incomplete attempt fails closed. Ten attempts
per human per minute bound initiation. Archive and challenge-ledger retention
must not be pruned as ordinary short-lived session data.

`Service.StatusHandler` is a separate, explicitly configured read-only surface:
`POST /assurance/v1/status` with `{"completion_id":"...","query_id":"..."}`.
It requires an injected `StatusAuthenticator` on every call and an exact
server-owned caller-to-executing-audience mapping. A success contains the echoed
query ID, authority/RP/origin, check time, immutable completion, and its one public
credential in the shared library's `User` representation. It does not expose
sessions, private keys, unrelated credentials, or management routes. Missing,
expired or revoked completions refuse; a completion reference is never bearer
authority. The combined lookup checks credential, ceremony and completion state.

`StatusClient` pins an HTTPS origin, refuses redirects, bounds responses, uses
host-supplied workload authentication, and requires a fresh query-ID echo.
`CompletionVerifier` exercises an independent consumer locally: every invocation
fetches current status and verifies the shared profile and signature. Its lookup
input matches Tachyon's internal completion-verifier fields; go-hitl extension
fields are at the top level of `RequestJSON`. This internal package is not an
importable cross-application SDK. Tachyon and Tether must install their own HTTP
adapters to this contract and the shared WebAuthn module, using authoritative
snapshots at decision and consume. Each caller still owns current operation
state, requester authorization and once-only execution.

The owner selected **Tether-issued workload JWTs**. `NewTetherStatusAuth` verifies
the real Ed25519 wire format through shared `security/auth v0.1.0`, using:

- Exact configured issuer and HTTPS JWKS URL; successful fresh key fetch on
  every request, explicit HTTP cache revalidation, no redirects or bootstrap key.
- Audience `basalisk-status` and exactly `basalisk-status.assurance_read`.
  Tether requires the scope prefix to match the audience, so the earlier
  `basalisk.assurance_read` proposal cannot be minted for this audience.
- Exact server-configured service/device subjects and kinds, with direct
  `par == sub`, no agent/operator/session token, and a server-owned list of
  operation audiences each caller may inspect. Literal subjects await enrollment.
- Required `kid`, `jti`, `iat`, strict expiry with no clock leeway, at most
  15-minute token lifetime, and bounded exact JSON parsing.
- A required `CurrentWorkloadTrust` adapter that checks current token/issuer
  revocation, principal/parent lifecycle, and current scope authorization on
  every call. It must fail closed on unavailable or stale status.

`CurrentTrustClient` implements Tether's published draft
`POST /auth/workload-trust` contract: a separate stored device reader, sole
`tether.workload_trust_read` scope, `Tether-Protocol: 1`, fresh 32-byte query nonce,
verified workload metadata (never the queried bearer), exact echo, `active:true`,
bounded no-store JSON and a five-second deadline. The reader file is read through
shared `tokenfile` custody every time; removal, unsafe permissions, symlinks or
rotation cannot retain an earlier usable reader. Missing current trust never
falls back to the old maintenance-grade denylist. The separate
`tachyon-enrollment` bootstrap audience is never status access.

`TachyonClient` consumes the authenticated
`/api/approvals/trusted/v1/{item,complete,outcome}` contract. Item/outcome reads
use fresh query echoes; delivery carries references only. It parses outcomes
through published `libs/mesh/core v0.8.0` HITL types and checks item/revision/state
consistency. The outbound parent is distinct from the trust reader and is read
through shared private token-file custody. Exchange asks for audience
`tachyon-assurance` and exactly one route scope: `tachyon-assurance.items_read`,
`tachyon-assurance.completions_deliver`, or `tachyon-assurance.outcomes_read`.
Short JWTs are reused until near expiry, with rotation/removal invalidating the
local cache, to respect Tether's bounded mint ledger. Recipients still verify
current trust on every use. No automatic denial-refresh loop or delivery retry
creates a new approval.

Tether's expanded current-trust/mint support for these Tachyon target scopes,
supported caller/reader enrollment, and live interoperability are activation
dependencies. The executable returns no-store JSON 503 for `/assurance/*` when
configuration is absent; configured routes fail closed when credentials or peers
are unavailable. They never fall through to the SPA.

Host configuration shape (all pin and path values below are placeholders):

```json
{
  "authority": "bas_REPLACE_WITH_EXISTING_INSTALLATION_ID",
  "origin": "https://basalisk.nanite.cloud",
  "rp_id": "basalisk.nanite.cloud",
  "audiences": ["tether"],
  "authentication": {
    "issuer": "REPLACE_WITH_VERIFIED_ISSUER",
    "jwks_url": "https://issuer.example/.well-known/jwks.json",
    "callers": [
      {"subject":"REPLACE_WITH_TACHYON_SUBJECT","kind":"device","audiences":["tether"]},
      {"subject":"REPLACE_WITH_TETHER_SUBJECT","kind":"device","audiences":["tether"]}
    ]
  },
  "current_trust": {
    "origin":"https://issuer.example",
    "issuer":"REPLACE_WITH_VERIFIED_ISSUER",
    "reader_token_file":"/private/REPLACE_TRUST_READER"
  },
  "tachyon": {
    "origin":"https://tachyon.example",
    "exchange": {
      "origin":"https://issuer.example",
      "issuer":"REPLACE_WITH_VERIFIED_ISSUER",
      "parent_token_file":"/private/REPLACE_TACHYON_PARENT",
      "subject":"REPLACE_WITH_BASALISK_SUBJECT",
      "kind":"device"
    }
  }
}
```

Commission the actual issuer pins, application/source allowlists and separate
private credentials through the supported lifecycle before installing this file.
Malformed/partial configuration fails startup. The configuration contains no
token values and is never exposed through the account API.

Enrollment calls require a separate host `EnrollmentPolicy` at both begin and
finish. There is no permissive first-key bootstrap, password-only enrollment,
credential import, or network enrollment route. The owner must complete the
supported first-key lifecycle before real ceremonies can work. Passkey private
keys stay with authenticators; no production key was enrolled by this change.

A verified completion is not a Tachyon decision. Delivery must use the same
`{ItemID, CompletionID, ExpectedRevision}` after response loss. After proof
freshness elapses, look up Tachyon's authoritative outcome; do not create a new
approval or credential to infer whether delivery succeeded. This backend does
not implement an automatic delivery worker; the authenticated deliver/outcome
routes support explicit delivery and recovery. Denial remains a Tachyon policy;
Basalisk cancellation never grants execution. Cerberus keeps its existing handoff.

Tests exercise real shared-library registration/assertion verification with
synthetic authenticators, restart and archive durability, concurrent completion,
counter policy, exact expiry, mutation, current revocation, status outages and
lost-response recovery. The public-only interoperability fixture comes from
libs commit `a38183e8`, `mesh/core/hitl/examples/operation-proof-v1/vectors.json`.
All assertions run through the actual verifier; consumer-owned authorization and
HITL descriptor cases remain explicit fixture preconditions, not a claim that
Tether's execution integration is implemented. Hardware/browser acceptance,
cross-service enrollment and live delivery remain outstanding. Additional TLS
HTTP tests verify real Ed25519 status JWTs, signing-key removal, issuer outages,
revocation refusals, expiry during current lookup, concurrent independent reads,
and JWT confusion cases. Proxy tests cover secure cookies and foreign/downgraded
origins. The network adapter tests exercise the real clients against TLS peer
simulators, signed JWT exchange and shared HITL outcomes, including credential
rotation, mint reuse, current-trust refusal, stale echoes, and recovery after
ambiguous delivery/expired proof. Upstream lifecycle state is simulated in these
tests; live deployment and cross-service acceptance are not claimed.

## Activity-session foundation

Open **Work sessions** to create a durable activity session. Initially the owner
has one open session, which can span project switches. Signing out, password
changes, and login expiry leave that activity session and its history intact.
Pause records a paused state; manual context changes remain available. Companion
observations are admitted only for an active session at their reported occurrence.

Session IDs are correlation handles, **not credentials**. The database retains
stable installation and human IDs, independent of username, server address, and
login cookies. They move with a consistent backup of the data directory. This
does not make two concurrently running copies of the database a synchronized
cluster. Paired devices have independent IDs and generation/sequence streams;
observations from different Macs do not establish one global focus order.

Each context change creates a new immutable snapshot. Project/workstream values
are user-declared labels; blank means unknown, and these labels are not verified
Tether project IDs. Lifecycle records retain the snapshot they used, the human
actor, source (`basalisk.web`), and server timestamps. Because these are manual
commands, occurred-at and received-at are the same. Native observation ingestion
records device time and server receipt time separately and remains device-reported.

The authenticated API uses the existing cookie and request-origin protection:

- `GET /api/activity`: stable identity and the open session, or `current: null`.
- `GET /api/activity/sessions`: newest sessions, 20 per page.
- `GET /api/activity/sessions/{id}/events`: newest events, 50 per page.
- Both lists return `items` and optional `nextBefore`; pass that value as
  `?before=...` for the next page. Sequence numbers order local records only.
- `POST /api/activity/commands`: `requestId` (16–128 bytes) and `action`.
  `start` takes `title` plus optional `project`/`workstream`. Other actions take
  `sessionId` and the current `revision`: `context` replaces both context labels;
  `pause`, `resume`, and `end` change lifecycle state. An ended session stays ended.

Use a fresh random request ID for each intent, and reuse that ID and payload for
network retries. The original result is returned on retries; use GET for current
state. Reusing an ID with a different payload, stale revisions, and conflicting
transitions return 409. Session state, context, event, and retry receipt commit
atomically. Database schemas v1/v2/v3 upgrade to v4 without changing account records;
newer unsupported schemas are rejected.

This local journal is the source for a future shared-event-bus adapter, not a new
portfolio envelope. No events leave Basalisk yet. The Swift companion source
stays on the observed Mac while Basalisk can run elsewhere through an SSH tunnel.
Remote HTTPS hosting, mesh JWT identity mapping and shared-bus
publication/consumption remain planned. The manual journal
is retained until data removal; there is no session deletion UI or automatic
retention of manual lifecycle records yet. Raw native observations have separate
five-minute retention.

## Companion pairing and observation API

See [companion/README.md](companion/README.md) for Mac build/pairing instructions,
collection controls, offline behavior and current validation limits.

The shared `security/pairing` module is pinned to the immutable draft-contribution
revision in [libs PR #92](https://github.com/hollis-labs/libs/pull/92), without a
local module replacement. It supplies protocol v1 and the standalone enrollment
store. This app does not mint mesh JWTs or automatically map devices to Tether's
registry. Tether remains the mesh token issuer; that adoption is separate.

Owner-authenticated endpoints create/revoke grants (`/api/devices/grants`), list
and revoke devices (`/api/devices`), and show the most recent 100 observations
(`/api/observations`). Device credentials cannot enter these account routes.

Native clients use `POST /auth/pair/exchange` and `POST /auth/renew` with
`Hollis-Pairing-Protocol: 1`, as documented by the shared module. Exchange has a
4 KiB body limit and ten attempts per minute. Codes expire in five minutes.
Device credentials expire after thirty days and can renew while valid; devices
must pair again after expiry/revocation. Password changes revoke browser sessions;
revoke a paired Mac separately on the Devices screen.

`GET /companion/context` requires `basalisk.activity.read`, returning current
session/context and a server `asOf` timestamp. `POST /companion/observations`
requires `basalisk.observations.write`, accepting a generation plus up to twenty
events within the 16 KiB request limit. Each event includes sequence, occurredAt,
kind, bundleId, appName, activitySessionId, contextId and contextAsOf. The client
currently sends ten events per batch. A fresh device lookup inside the ingestion
transaction serializes revocation with writes. Duplicate sequence/payload is
accepted; conflicting reuse returns 409. Expired or inactive/mismatched-session
observations are acknowledged as ignored. Context may be at most thirty seconds
old, and timestamps more than thirty seconds ahead are rejected. No device can
submit a different device ID or choose the server's receipt time.

Observations are queried for five minutes and purged every thirty seconds. This
is logical retention, not forensic erasure of SQLite pages or backups. Only
explicit user-started capture and fresh active-session context produce records.

## Data and security

Basalisk follows the XDG base directory layout, resolved by the shared
`apppaths` library:

| Kind | Default | Override |
| --- | --- | --- |
| Data (database, secrets, backups) | `$XDG_DATA_HOME/basalisk`, else `~/.local/share/basalisk` | `BASALISK_DATA_DIR` |
| Configuration | `$XDG_CONFIG_HOME/basalisk`, else `~/.config/basalisk` | `BASALISK_CONFIG_DIR` |

The assurance host configuration is `assurance.json` in the configuration
directory when that file exists; `BASALISK_ASSURANCE_CONFIG` names a different
file. Keep operator configuration there, never in the checkout.

The app creates a private data directory (0700) and SQLite database (0600).
Earlier versions kept data in `$XDG_STATE_HOME/basalisk`. If a database is there
and none exists in the data directory, Basalisk refuses to start rather than
open an empty installation and reopen owner setup. Move the directory (stop the
app first) or set `BASALISK_DATA_DIR` to keep using the old one.

SQLite stores the owner profile, bcrypt password hash, hashed session tokens,
integration metadata, and activity identity/session/context/journal records.
Cookies are HttpOnly and SameSite=Strict; authentication sessions
expire after 12 hours and survive process restarts. Password changes revoke all
sessions. Mutations require a custom request header and reject foreign Origins.
Login/setup/password attempts share an in-process throttle. Profile/preferences
updates reject stale revisions rather than overwriting another session's save.

Credentials use `github.com/hollis-labs/libs/security/secrets` with an app-scoped
managed file store under the data directory. This backend uses permission-limited
**plaintext files**, not encryption at rest; the current OS user and privileged
processes can read them. Secret values and storage references are never included
in API responses. There is no credential-reveal endpoint. This is an initial
local storage choice; an OS keychain or another supported resolver backend can
replace it as deployment needs develop.

Stop the app before backing up the **entire data directory**, including secrets.
Protect backups as credentials. There is no password recovery or account reset
workflow yet; do not delete the database to reset a password unless you intend
to discard the owner and metadata. Test with a separate `BASALISK_DATA_DIR`.

## Hollis Labs foundations

Folio generated this project with `app-dashboard` 2.1.0, `gui_host=chimera`, and
`base_path=/`. `.folio.yaml` preserves the original scaffold inputs/digests.
The maintained Chimera Go host remains at Folio's pinned revision.

The app extends the original frozen GUI recipe with published design-kit 0.5.x
packages and kit-settings 0.2.x. The lockfile records exact installed versions;
`chimera-provenance.json` distinguishes this customization from the original
recipe. Account forms consume the private, unpublished `kit-account` snapshot in
`third_party/kit-account`, with its MIT license and source provenance. `npm ci`
installs that source package reproducibly; no sibling checkout is required.

`kit-account` supplies controlled UI, not user authentication. Basalisk owns its
owner/session backend. The shared Hollis Labs auth library handles service bearer
auth, so it is not used as a password-login implementation. Future Hollis Labs
consumers should call public service APIs through app-owned adapters and resolve
credentials only on the server. Integration base URLs are metadata until an
adapter explicitly owns requests and its outbound access policy.

## Development and verification

```sh
cd frontend
npm ci
npm run typecheck
npm run lint
npm run build
cd ..
make test
make vet
make build
```

`make ui-dev` runs Vite with API proxying to `127.0.0.1:8080`; `make run` serves
the existing embedded assets and backend. `make all` rebuilds both for normal use.
If this app is registered with Cerberus, use its resource deploy/start lifecycle.

Go tests exercise setup races, session expiry, restart persistence, password
changes, request-origin rejection, stale edits, login throttling, and secret
write/replace/delete behavior without disclosure. Browser verification used a
disposable data directory and exercised setup, profile/preferences persistence,
key management, password change, login/logout, and a 390px mobile viewport.

## Frag session provenance

The companion now keeps separate five-minute local focus history and publishes a
private, short-lived context snapshot for Frag. Frag binds that snapshot before
taking focus, preserves it through project changes/retries, and queues a receipt
only after a confirmed FE fragment ID. Basalisk stores those associations in
schema v4 and displays **Saved fragments** within each work session. See the
[bridge contract](companion/README.md#frag-context-bridge) for freshness, privacy,
retention and retry semantics. FE's existing intake payload is unchanged;
provenance lives in Basalisk and an unavailable companion never blocks a note.

## Scoped Frag delivery

The broker path is Frag → signed local companion → paired Basalisk device → FE's
public capture API. Schema v7 adds immutable outbound manifests, acceptance
receipts, retry scheduling and worker leases. Delivery is disabled until all of
these operator settings are supplied:

| Setting | Value |
| --- | --- |
| `BASALISK_FE_INTEGRATION_ID` | ID of the explicitly bound `fragments-engine` integration |
| `BASALISK_PUBLIC_ORIGIN` | Stable Basalisk HTTPS origin, or an explicit loopback HTTP origin |
| `BASALISK_TETHER_ISSUER_URL` | Trusted HTTPS Tether issuer origin |
| `BASALISK_TETHER_SUBJECT` | Enrolled stable Basalisk service subject |
| `BASALISK_TETHER_PARENT_REF` | Shared-secrets reference to a dedicated capture-only enrollment parent, e.g. `file:///private/path/to/enrollment-parent` |

Partial settings fail startup. The integration's legacy API key is never used by
this worker. The parent is resolved for every attempt; the issued 15-minute JWT
stays in memory. Its audience is `fragments-engine` and its sole requested scope
is `fragments-engine.capture`. Parent rotation takes effect on the next attempt;
identity/issuer changes cannot retarget an already prepared capture. Production
setup depends on Tether service enrollment.

This first adapter serves one configured integration per process. FE and issuer
requests refuse redirects, use normal TLS validation and have ten-second limits.
Each worker attempt has a forty-second deadline and a two-minute durable lease.
Transient failures back off from five seconds to five minutes without deleting
notes. Authorization, identity, conflict and invalid-receipt failures pause for
owner review; the outbox provides explicit retry after correction.

Before sending, Basalisk fixes a globally random upstream capture ID, source URL,
service subject, FE origin and complete manifest in SQLite. The source URL points
to the existing owner-authenticated submission detail endpoint; FE does not fetch
it. Original draft/save context travels in `document.extensions.provider.basalisk`
and remains correlation evidence, not authorization. An active work session is
optional. Acceptance and completion are separate durable stages: lost responses
replay the exact original manifest or completion key. Only confirmed completion
publishes the FE link and atomically records a server-confirmed session artifact.
Existing device-reported artifact receipts retain their original attribution.

Outbox deletion is explicit. Before any send it discards the note; after a possible
send it stops further delivery and warns that FE may already hold a copy. It never
claims to delete remote content. Active deliveries must finish or release their
lease before this action can proceed. Saved notes remain in the authenticated
submission detail endpoint. Logical deletion is not secure erasure of WAL/backups.

Run the ordinary race suite with `make test vet`. To additionally exercise a real
FE executable with disposable databases and generated test credentials:

```sh
BASALISK_TEST_FE_BINARY=/absolute/path/to/fragments-engine make test vet
```

### Nil v2 capture metadata

`POST /companion/fragments` accepts optional `metadata` with `schemaVersion: 1`
and a required `client: {name, version}`. It carries a title, note/todo/scratch
kind, projects, contexts, priority, due date, typed mention/link references and
the applied preset identity/revision. Markdown stays in `content`; HTML is a
rebuildable Nil cache and is not accepted as capture metadata. The broker treats
these fields as client assertions, never grants or instructions to fetch links.

The worker preserves metadata in
`document.extensions["provider.basalisk"].capture_metadata` and uses the supplied
title in FE. Existing requests without metadata retain their original shape.
Retries must preserve the complete submitted operation, including metadata.

Independent Nil pairing/context is separate from this companion; do not
reuse the Swift companion's credentials. Source implementation does not imply
the live broker has been deployed.

### Independent capture apps

In Devices, select **Capture app (Nil)** to create a grant with
`basalisk.activity.read` and `basalisk.fragments.create`. It cannot publish focus
observations. The owner grant API accepts `profile: "capture"`; omitted profiles
retain the original companion behavior. Assign the app its own FE connection
after pairing with the shared protocol.

`GET /companion/capture-capabilities` reports supported metadata/context versions,
limits and paths under the app's capture credential. It does not report delivery
readiness. At new draft initiation, `POST /companion/capture-context` with
`{captureID}` returns a durable `bindingID`, server `draftAt`, and optional frozen
context. Repeating that capture ID returns the original binding. Submit
`contextBindingID` and the exact returned `draftAt` with the fragment request;
omit raw `draft`/`saveContext`. Basalisk retrieves the snapshot itself. Device,
capture, origin and timestamp must match. Context cannot be added retroactively
after submission.

Unavailable/offline context can be omitted, and paused/no-session bindings carry
no snapshot. A new context response on reconnection describes that later time,
not when an offline draft originally began. Native app history is unavailable to
capture apps until an explicit sharing contract exists. Bindings survive restart
and session changes; schema 8 retains them with a bound of 1,000 per device and
10,000 per installation. No automatic binding deletion is implemented.

The test owns and cleans up its loopback listeners and FE child process. It checks
lost acceptance/completion responses, persisted provenance, principal attribution
and revoked-service rejection. It never contacts the production FE or Tether.
