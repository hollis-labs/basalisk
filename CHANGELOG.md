# Changelog

All notable changes to Basalisk are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Releases are tagged `0.x`; there is no compatibility promise before 1.0, and breaking changes can land in any release.

## [Unreleased]

## [0.1.0] - 2026-10-20

First tagged release (the baseline tag; later changes are listed under the next version).

### Security

- Go toolchain 1.26.10 (`go` directive and CI pin) for GO-2026-6809 (crypto/x509); no behavior change.

### Changed

- **Data now lives under `XDG_DATA_HOME` and configuration under `XDG_CONFIG_HOME`**, resolved with the shared `apppaths` library, instead of `XDG_STATE_HOME`. `BASALISK_DATA_DIR` still overrides the data directory and `BASALISK_CONFIG_DIR` the configuration directory. **Breaking for existing installs:** with a database in the old state directory and none in the new data directory, startup is refused with the fix named, so an empty installation is never opened silently. Move `~/.local/state/basalisk` to `~/.local/share/basalisk` (app stopped) or set `BASALISK_DATA_DIR`.
- The assurance host configuration defaults to `assurance.json` in the configuration directory when present; `BASALISK_ASSURANCE_CONFIG` still takes precedence.

### Added

- Initial public commit: single-owner Go service with an embedded React GUI (first-launch owner creation, password login, account and preference settings).
- Portable human activity sessions with immutable provenance, a paired-device companion API, and a local Swift macOS companion (`companion/`).
- Fragment capture broker: signed local XPC, device and integration grants, durable capture journals and an owner outbox. Delivery stays off until configured.
- Operation-assurance backend (WebAuthn, `operation-webauthn-v1-draft`), integration pending.
- Open-source project documents: `CHANGELOG.md`, `CONTRIBUTING.md`, `SECURITY.md`, `TRADEMARK.md`, and an example Cerberus descriptor in `deploy/`.
