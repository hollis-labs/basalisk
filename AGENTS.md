# Basalisk

Personal app scaffolded with Folio `app-dashboard`, `gui_host=chimera`.
Go serves the embedded React frontend. The initial slice is a single-owner,
loopback-only app. Run it from an installed binary; keep config, descriptors and data outside the checkout (see CONTRIBUTING.md).

- Reuse Hollis Labs libraries and public APIs as a consumer. Do not change
  sibling repositories as part of routine Basalisk work.
- Preserve `.folio.yaml` as original scaffold provenance. Record intentional
  dependency customization in `chimera-provenance.json` and the lockfile.
- `third_party/kit-account` is an unpublished MIT source snapshot; its provenance
  explains the package-export and Tailwind adaptations. Prefer a published
  release when available, without silently replacing source.
- Identity, sessions, validation, and persistence are backend-owned. Account and
  settings kits are controlled presentation components.
- Never return stored API keys, secret references, password hashes, or session
  hashes from APIs. Use the shared secrets resolver at the adapter boundary.
- Build UI before the Go binary: `make all`. Verify with frontend typecheck/lint
  and `make test vet`. Use disposable data directories for browser tests so the
  user's first-run account is never created by an agent.
