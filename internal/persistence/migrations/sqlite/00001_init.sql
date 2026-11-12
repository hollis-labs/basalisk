-- The Basalisk schema, squashed to one migration (decision: clean break from the
-- pre-Substrate layout). Basalisk is SQLite only.
--
-- Not here, on purpose:
--   * accounts, passwords and login sessions: the identity module's identity_* tables;
--   * companion devices and pairing grants: created by the libs security/pairing store.
--
-- Until the first release this is the ONLY migration: a schema change edits this file,
-- because there are no deployed databases to migrate. After the first release changes
-- are new numbered files.

-- +goose Up

-- Per-user preferences, keyed by the identity user id. A user with no row has the defaults.
CREATE TABLE app_preferences (
    user_id  TEXT PRIMARY KEY,
    theme    TEXT NOT NULL DEFAULT 'dark',
    timezone TEXT NOT NULL DEFAULT 'UTC',
    revision INTEGER NOT NULL DEFAULT 1
);

-- Integration metadata. The credential lives in the secrets resolver at secret_ref.
CREATE TABLE integrations (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    provider   TEXT NOT NULL,
    base_url   TEXT NOT NULL,
    secret_ref TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE companion_observations (
    device_id   TEXT NOT NULL,
    generation  TEXT NOT NULL,
    sequence    INTEGER NOT NULL,
    payload_hash TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    received_at TEXT NOT NULL,
    kind        TEXT NOT NULL,
    bundle_id   TEXT NOT NULL,
    app_name    TEXT NOT NULL,
    session_id  TEXT NOT NULL,
    context_id  TEXT NOT NULL,
    context_as_of TEXT NOT NULL,
    PRIMARY KEY (device_id, generation, sequence)
);
CREATE INDEX companion_history ON companion_observations (occurred_at);

CREATE TABLE companion_artifacts (
    sequence     INTEGER PRIMARY KEY AUTOINCREMENT,
    device_id    TEXT NOT NULL,
    capture_id   TEXT NOT NULL,
    session_id   TEXT NOT NULL,
    payload_hash TEXT NOT NULL,
    payload      TEXT NOT NULL,
    received_at  TEXT NOT NULL,
    source       TEXT NOT NULL DEFAULT 'frag.fe-response (device-reported)',
    UNIQUE (device_id, capture_id)
);
CREATE INDEX companion_artifacts_session ON companion_artifacts (session_id, sequence);

-- A device is bound to one integration by the user who owns the device; owner_id is that
-- user's principal subject, which the delivery worker needs to check the device's grant.
CREATE TABLE fragment_connections (
    device_id      TEXT PRIMARY KEY,
    integration_id TEXT NOT NULL REFERENCES integrations (id) ON DELETE CASCADE,
    owner_id       TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);

CREATE TABLE fragment_submissions (
    sequence            INTEGER PRIMARY KEY AUTOINCREMENT,
    device_id           TEXT NOT NULL,
    capture_id          TEXT NOT NULL,
    integration_id      TEXT NOT NULL,
    payload_hash        TEXT NOT NULL,
    payload             TEXT NOT NULL,
    state               TEXT NOT NULL,
    code                TEXT NOT NULL,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    manifest            TEXT NOT NULL DEFAULT '',
    delivery_origin     TEXT NOT NULL DEFAULT '',
    delivery_issuer     TEXT NOT NULL DEFAULT '',
    delivery_subject    TEXT NOT NULL DEFAULT '',
    upstream_capture_id TEXT NOT NULL DEFAULT '',
    receipt             TEXT NOT NULL DEFAULT '',
    fragment_id         TEXT NOT NULL DEFAULT '',
    fragment_revision_id TEXT NOT NULL DEFAULT '',
    attempts            INTEGER NOT NULL DEFAULT 0,
    next_attempt        INTEGER NOT NULL DEFAULT 0,
    lease_token         TEXT NOT NULL DEFAULT '',
    lease_until         INTEGER NOT NULL DEFAULT 0,
    may_have_sent       INTEGER NOT NULL DEFAULT 0,
    UNIQUE (device_id, capture_id)
);
CREATE INDEX fragment_submissions_due ON fragment_submissions (state, next_attempt, lease_until);

CREATE TABLE capture_context_bindings (
    id         TEXT PRIMARY KEY,
    device_id  TEXT NOT NULL,
    capture_id TEXT NOT NULL,
    origin     TEXT NOT NULL,
    draft_at   TEXT NOT NULL,
    payload    TEXT NOT NULL,
    UNIQUE (device_id, capture_id)
);

-- Human work sessions, independent of login sessions. user_id is the identity principal
-- subject of the person who owns the session; installation_id stays local to this database.
CREATE TABLE activity_identity (
    singleton       INTEGER PRIMARY KEY CHECK (singleton = 1),
    installation_id TEXT NOT NULL
);
INSERT INTO activity_identity VALUES (1, 'bas_' || lower(hex(randomblob(16))));

CREATE TABLE activity_contexts (
    id          TEXT PRIMARY KEY,
    project     TEXT NOT NULL,
    workstream  TEXT NOT NULL,
    source      TEXT NOT NULL,
    recorded_at TEXT NOT NULL
);

CREATE TABLE activity_sessions (
    sequence   INTEGER PRIMARY KEY AUTOINCREMENT,
    id         TEXT NOT NULL UNIQUE,
    user_id    TEXT NOT NULL,
    title      TEXT NOT NULL,
    status     TEXT NOT NULL CHECK (status IN ('active', 'paused', 'ended')),
    revision   INTEGER NOT NULL,
    context_id TEXT NOT NULL REFERENCES activity_contexts (id),
    started_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    ended_at   TEXT NOT NULL
);
CREATE UNIQUE INDEX activity_one_open ON activity_sessions (user_id) WHERE status != 'ended';

CREATE TABLE activity_events (
    sequence    INTEGER PRIMARY KEY AUTOINCREMENT,
    id          TEXT NOT NULL UNIQUE,
    session_id  TEXT NOT NULL REFERENCES activity_sessions (id),
    actor_id    TEXT NOT NULL,
    kind        TEXT NOT NULL,
    revision    INTEGER NOT NULL,
    context_id  TEXT NOT NULL REFERENCES activity_contexts (id),
    source      TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    received_at TEXT NOT NULL
);
CREATE INDEX activity_events_session ON activity_events (session_id, sequence);

CREATE TABLE activity_commands (
    request_id  TEXT PRIMARY KEY,
    fingerprint TEXT NOT NULL,
    result      TEXT NOT NULL
);

-- +goose Down
DROP TABLE activity_commands;
DROP TABLE activity_events;
DROP TABLE activity_sessions;
DROP TABLE activity_contexts;
DROP TABLE activity_identity;
DROP TABLE capture_context_bindings;
DROP TABLE fragment_submissions;
DROP TABLE fragment_connections;
DROP TABLE companion_artifacts;
DROP TABLE companion_observations;
DROP TABLE integrations;
DROP TABLE app_preferences;
