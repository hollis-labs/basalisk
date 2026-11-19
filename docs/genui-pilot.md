# Preferences GenUI pilot

The pilot presents three reviewed patterns: an editable preferences form, an
immutable before/after approval, and a selected list/detail result. The existing
account preference validator and revision CAS own the effect. An intervening
ordinary preferences update makes an old review stale; it is never overwritten.

The GenUI gateway and contract use a normal published Substrate dependency and
its BOM. The renderer is the published `@hollis-labs/genui` package, pinned at
`0.1.0` with the matching shared UI packages in the frontend lockfile. A fresh
empty-cache public-registry install verifies the normal dependency graph; no
replacement, workspace or local package overlay is required. Basalisk release
and deployment remain separate.

## Authority and persistence

Every browser route requires a verified user, the explicit `basalisk.genui.*`
operation scope, and a fresh identity user lookup. Disabled users and removed
current grants refuse even when an earlier JWT still verifies. Reads/show also
require `basalisk.read`; actions/acknowledgments require `basalisk.write`.

The approval requires a short-lived, exact-action, once-only host acknowledgment.
It is not reauthentication, MFA, or stronger presence. Higher-risk operations are
unavailable. The browser supplies no trusted runtime, generation, owner or policy.
Proofs are never included in discovery or recovered original calls.

The same account mutex fences admission and the ordinary preferences writer.
Short SQL writes persist immutable surfaces, action identity, revisions and
permanent claims. No SQL transaction spans gateway callbacks. The actual
preferences revision CAS still arbitrates competing database writers. Current
identity is checked again after the claim and before the effect; this is a
point-in-time check, not instantaneous distributed revocation.

A deadline or SQLite commit error does not prove physical rollback. A late or
uncertain effect stays claimed and returns an unknown native response. Recovery
reads the stored original call and result; it cannot execute or release a claim.
The HTTP test uses a permitting driver COMMIT stall and proves that a physically
committed late change remains unknown and cannot replay after reopen.

## Routes

| Route | Scope | Shape |
|---|---|---|
| POST `/api/genui/surfaces` | `basalisk.genui.show` | `{"pattern":"form","params":{}}` |
| GET `/api/genui/surfaces` | `basalisk.genui.read` | `{surfaces:[{id,title,actionId?}]}` |
| GET `/api/genui/surfaces/{id}` | `basalisk.genui.read` | Original native envelope |
| POST `/api/genui/actions` | `basalisk.genui.act` | Native Call and AgentFunctionResponse |
| GET `/api/genui/actions/{id}?functionCallId=...` | `basalisk.genui.read` | `{request?,response}` with fresh correlation |
| POST `/api/genui/confirm` | `basalisk.genui.confirm` | Original native Call → `{confirmation}` |

Discovery is routing metadata only. Recovery retains the actual selected choice
and form draft, including after a process restart; no localStorage state chooses
an action. A successful form/approval returns `result.surfaceId` for the next
persisted reviewed surface. Unknown results offer recovery, never implicit retry.

The server agent's `genui_show_pattern` accepts only `payload_json`. The private
active run and persisted run owner select the account; the host reloads that
user. Presentation uses LevelAct and has no mutation/confirmation operation or
legacy standing grant. Inner JSON stays text through the map-based tool boundary.

A separate POST `/api/genui/producer/surfaces` uses a constructor-enrolled verified
service subject mapped to one owner and a show-only scope. No enrollment is
configured by default. It cannot act or confirm, even with a broad service key.
This is a registered producer adapter, not full plugin-host runtime integration.

The app remains unreleased and follows its single in-place migration convention.
This preparation uses fresh disposable databases; it performs no live migration,
cutover, credential provisioning, or deployment.
