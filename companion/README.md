# Basalisk Mac companion

A small SwiftUI menu-bar application for macOS 14+, built with Swift 6 tools.
It appears in the Dock and menu bar and opens its pairing window on launch.
Reopening the app brings that window forward. It stays running after its settings
window closes. This first implementation
uses NSWorkspace, without Accessibility or screen-recording authorization.

## Build and pair

On the Mac, from this directory:

```sh
./build-app.sh
open "build/Basalisk Companion.app"
```

The build requires a persistent signing identity in `BASALISK_SIGNING_IDENTITY`
or `~/.config/basalisk-companion/signing-identity`. Optional `signing-keychain`
and `signing-password-file` contain paths to the existing dedicated signing
Keychain and its private password file; never copy the password into source.
Both apps must use the same signing certificate for capture IPC. Builds use the
hardened runtime and verify the complete bundle. This is local signing, not a
notarized distribution. Switching from the older ad-hoc companion may require
one authorization of its existing pairing credential.

1. Open Basalisk's **Devices** page and create a code for this Mac.
2. Enter the server address and one-use code in the companion. For a backend
   reached over SSH, use the same local tunnel origin as your browser, e.g.
   `http://127.0.0.1:18080`. Keep the authenticated tunnel running.
3. Start a work session in Basalisk, then choose **Start capture** in the companion.
4. Switch apps. **Devices → Recent Mac activity** refreshes every ten seconds.

The Connection card distinguishes **Not paired**, **Connected**, **Paired ·
offline**, and **Pair again**. Pairing shows progress and an inline result;
expired or used codes prompt you to create a fresh one. A successful pairing
replaces the code form with connection details and a confirmation. **Activity
capture** is separate: pairing never starts recording automatically.

Basalisk currently listens on loopback; it does not expose a direct plaintext
tailnet endpoint. The companion also accepts an HTTPS origin for a future
configured HTTPS deployment. Changing servers requires a fresh pairing code.
Credentials are stored in Keychain under the exact server origin; redirects and
cookies are disabled. Codes are cleared after exchange. Revocation lives in the
Basalisk Devices screen. Local disconnect removes this origin's Keychain entry;
it does not revoke the server-side device.

## Collection and attribution

Only app name, bundle ID, activation time, and sleep/wake events are collected.
No titles, window content, URLs, clipboard, keys or screenshots. Configure exact
app bundle IDs in the comma-separated exclusions field before starting capture.
The companion excludes itself. Pause stops new capture and discards queued data;
already transmitted observations remain subject to server retention.

The companion polls session context every five seconds and attaches the last
observed context ID and its server timestamp. It collects only while the work
session is active and that context is no older than thirty seconds. If the server
is unavailable longer, new capture stops until context returns. Clocks ahead of
the host can cause observations to be rejected; this baseline does not estimate
or silently rewrite clock offsets. Basalisk checks the recorded lifecycle/context
at occurrence time and ignores mismatches, avoiding attribution to a different
project after an offline switch. This is device-reported metadata, not proof of
OS activity or authorship.

The private queue and separate recent history use an origin binding, persistent
generation/sequence, atomic file writes and a maximum of 512 observations/five
minutes each. Acknowledged observations leave the queue but remain in bounded
history. Pause clears both; exclusions remove matching entries from both.
Exact payloads are
retried; the server deduplicates by authenticated device, generation and sequence.
The server retains at most five minutes of queryable raw observations and purges
expired rows every thirty seconds. Logical deletion does not erase backup copies
or guarantee forensic removal from filesystem/WAL pages. No long-term focus log
is created; durable session lifecycle/context records remain separate.

The app starts paused on each launch. Queue storage is under the user's private
`Library/Application Support/BasaliskCompanion` directory (0700; files 0600).
Preferences hold origin, exclusions and explicit Frag capture approval. Tokens never appear there. A lost
successful pairing response requires a new code; revoke the unused device entry.

## Validation status

The release app builds and signs on the home Mac with Apple Swift 6.2.3 and has
been launched from `~/Applications/Basalisk Companion.app`. Native checks passed
for origin validation, Keychain create/read/update/delete and origin isolation,
timestamp conversion, and the Swift transport against a disposable backend
reached over a private network. That round trip covers pairing, context reads,
synthetic observations, duplicate retry deduplication, renewal and revocation.
The real UI model is also checked with isolated preferences/queue storage for
invalid-code feedback, successful connection, capture remaining paused after
pairing, and revoked access returning to a pairing-required state.
No real app activity was collected by those checks. Interactive app switching,
exclusions, pause, offline reconnect, sleep/wake and collection with the settings
window closed still need acceptance on the Mac. Go pairing/ingestion boundaries
also have automated integration/race coverage.

Run `./check.sh` on a Mac for native boundary checks. When invoking through SSH,
use `./check.sh --gui` to run in the logged-in desktop security session: direct
SSH execution cannot interact with the login Keychain. The checks require only
Command Line Tools, not XCTest or a full Xcode installation. They create and
remove a temporary Keychain item with a random test origin.

For the optional transport integration check, set `BASALISK_TEST_ORIGIN` to a
**fresh disposable** backend (possibly through an SSH tunnel). The check refuses
an existing owner installation, creates a test account and synthetic activity,
then revokes its test device. Never use your normal Basalisk instance for this.
To reach the observed Mac from another host, use SSH through a jump host you
control. Do not copy or forward the machine key.

## Frag context bridge

While capture is enabled and a work session is active, the companion publishes
`~/Library/Application Support/BasaliskCompanion/context.json` atomically (0600,
inside a 0700 directory). It contains schema version 1, backend origin/device ID,
session/context IDs, explicit project/workstream, freshness timestamps, the
preceding app, and at most 50 references into five-minute history. It contains no
credential or note content. The snapshot expires within 12 seconds and never
outlives the 30-second server context. Pause/revocation/inactive sessions remove
it. Consumers must reject missing, expired or unknown-version snapshots. This is
a same-OS-user interface, not a network API or a sandbox between trusted apps.

Frag freezes this snapshot **before** activating its window. Its own activation
is excluded from the preceding-app selection. Reopening or retrying a draft does
not replace the binding; save-time context is separate. If the companion is
unavailable, Frag saves normally with unknown context.

After FE returns a non-empty fragment ID, Frag writes a private receipt under
`~/Library/Application Support/Frag/BasaliskLinks/`. The companion submits it to
`POST /companion/artifacts` using its observation scope, and removes it only after
acknowledgment. Transient failures retry the same capture ID/payload. Permanent
validation/conflict failures become `.rejected` files with a visible companion
error. The outbox is capped at 1,000 entries and 30 days; no note text or API token
is stored there. Origin/device mismatch never sends a receipt to another pairing.

Basalisk stores the association durably and shows it under **Work sessions →
Saved fragments**. It verifies owner/device and historical draft context, rejects
conflicting retry payloads, and drops an unverifiable optional save-time context
without replacing the draft binding. Links are device-reported evidence of an FE
response, not independent server-to-server verification. FE's current intake API
returns no revision ID; it stays unknown. This uses FE's existing content/tags
contract unchanged; provenance is stored in Basalisk, not injected into FE
metadata. Raw history still expires; durable links keep their context and history
references. Links, like manual session records, have no automatic deletion UI yet.


## Integration broker foundation (not deployed)

The broker source is implemented through durable queue acceptance. FE delivery
is deliberately unavailable until the upstream scoped capture contract is
published. Do not enable a production cutover based on a queued ack.
No FE credential is resolved and no upstream call is made by this version.

Frag's optional **Use Basalisk Companion** delivery mode is frozen per draft.
It sends a bounded v1 request over the per-user Mach service
`com.hollislabs.basalisk.companion.capture`. Both XPC peers enforce the exact
other app's bundle ID and their own signing certificate's leaf fingerprint.
The companion also checks the connecting OS UID and requires explicit local
approval on every request. The signed app is launched through a per-user
LaunchAgent with a declared MachServices entry; no HTTP localhost trust shortcut
or exported bearer token is used. `install-capture-agent.sh` installs that agent
after the old companion has quit and the signed bundle is in place. Production
installation/cutover remains pending; do not start a second companion instance.

The owner must separately pair with `basalisk.fragments.create` and bind the
device to a `fragments-engine` integration in Basalisk → Devices. Existing
activity-only devices gain no permission. The companion reads connection metadata
only; FE credentials stay in Basalisk's existing shared secrets file backend.

The local `captures.json` outbox is private (0700 directory/0600 file), written
atomically and synchronized before acknowledgment. It retains at most 100 pending
notes (64 KiB each) and 1,000 total entries. Corruption blocks new acceptance and
preserves the file. No unsent note expires automatically. Origin, device,
integration, draft context and operation identity are fixed on first acceptance;
re-pairing never silently moves a note. Activity pause leaves notes untouched.
Queued notes can be reviewed/copied in Companion; an accepted server note can
be explicitly discarded through Basalisk → Fragment outbox. Discard receipts
clear local note text. Completed receipts can be dismissed locally.

Basalisk schema6 journals operations with exact retry hashes, 100 pending notes
per device, 500 globally and 10,000 total tombstones/receipts. A full store rejects
new submissions so the caller keeps its draft. Removing integrations/devices
does not cascade into notes. Owner-only detail endpoints allow recovery after
revocation. Discard clears the readable payload but retains the hash/identity so
a delayed retry cannot resurrect it. This is logical deletion, not secure erasure
of filesystem pages, WAL or backups. No artifact link is created without FE
confirmation. The FE adapter, execution/retry worker and confirmed artifact
association remain to be implemented against the external contract.

`./check-capture-bridge.sh` uses disposable signed helpers and temporary
LaunchAgents to verify expected peers connect and wrong client/server identities
are rejected; it does not touch production credentials or running apps.
`./check.sh --gui` exercises the local capture journal, and its optional
disposable-backend test covers explicit approval, binding, queue acceptance and
owner discard. The v1 native capture JSON is separate from the FE wire contract.
