# Conversion notes: gaps and friction

Found while moving the frontend onto app-shell 0.1.0 and the BOM-pinned kits. Owner-facing.

## Framework gaps

1. **No seam for an app to contribute UI.** `substrate frontend` reads `ui.json` only from catalogue
   modules (`catalog.Find`), and an app is not a module. Workaround: hand-written
   `src/app.contributions.json` merged client-side in `src/manifest.ts`. A real fix is an app-level
   contribution file named in `substrate.yaml` (e.g. `frontend.contributions: ui.json`) that the builder
   validates and merges like a module's.
2. **`substrate frontend` writes `"items": null`** when no module contributes items (its `merge`
   returns a nil slice). app-shell's `parseFrontendManifest` rejects it (`Array.isArray`), so the
   shell would show "The app cannot start". Worked around with `?? []` in `manifest.ts`. Fix in
   `frontend/frontend.go` (return `[]Item{}`).
3. **The builder CLI needs the app's `go.sum` complete.** `npm run manifest` (`go run
   .../cmd/substrate frontend`) failed on the in-progress port with a missing `gopkg.in/yaml.v3` entry
   (the app does not import the `bom` package itself). The generated manifest checked in here was
   produced with the same builder from a scratch module that replaced Substrate with the local
   checkout, from this app's `substrate.yaml`. Regenerate it once the backend `go.sum` is settled.
4. **No sign-out / user-menu region.** The shell has `nav.footer` items that only navigate and no
   header/user slot that is not a docked drawer, so sign-out is a footer page with one button.
   kit-account's `WhoamiBadge` expects an app-shell slot, but `slot` only toggles a `dock.tab`.
5. **identity ships no UI.** Its `ui.json` is routes only: no sign-in, setup, password, sessions, keys
   or pairing-code surfaces. kit-account has `ApiTokenManager`/`NewTokenDisclosure`, but nothing wires
   them to `/api/identity/keys` or `/api/identity/pairing-codes`. Basalisk keeps its own sign-in
   and setup screens, and the Change password form. Sessions/keys/pairing screens were not
   in the old app and were not added.
6. **Sign-in happens outside the shell** by necessity (the shell has no unauthenticated mode); each app
   must rebuild this screen.
7. **`settings.region` is not usable for app preferences.** It renders only labels of admin-manifest
   groups (scope kind `app`) and no editor, so theme/timezone still use kit-settings'
   `SettingsRenderer` inside the Account page.
8. **Passing state to pages.** `PageProps` carries only `surface`, `apiBase` and `resolveRenderer`,
   so the signed-in profile reaches pages through an app React context.
9. **Page title duplication.** The shell draws an `h1` from the surface title, so the existing page
   headers were demoted to `h2` (kept their own flavor heading).
10. **Dock attributes are not applied** by app-shell 0.1.0 (its README says so); unused here.

## Packaging and versions

- `app-shell` and `design-bindings` publish raw `.ts` (`main: ./src/index.ts`, imports ending `.ts`).
  tsc fails with TS5097 until `allowImportingTsExtensions` is set (done in `tsconfig.json`; consumers
  need `noEmit`). app-shell has no `source.css`, so Tailwind needs
  `@source "../node_modules/@hollis-labs/app-shell/src"` (in `src/index.css`) or its classes are unstyled.
- `kit-settings` 0.2.0 depends on design-components/design-tokens `^0.4`, so npm installs a nested 0.4
  copy under `node_modules/@hollis-labs/kit-settings` beside the pinned 0.5 (the BOM's known
  `kit_exceptions`). Two copies of the components ship in the bundle until kit-settings is released on 0.5.
- kit-account 0.1.0's README still says "Private 0.0.0; not published", and its install notes cite
  design-components 0.1.1. The package works as published.
- kit-dashboard is still needed only for `theme.css` and `applyTheme`/`getInitialTheme`; its `NavRail` and
  `PageHeader` are replaced by the shell. design-bindings and plugin-host-ui are direct pins only to keep
  app-shell's transitive versions at the BOM's exact versions.
- `npm audit` reports vite 7.3.6 (dev server only, launch-editor advisories) and a dev-tool
  minimatch; the versions are the pre-existing pins and were not changed.
- Vite prints "use client" directive warnings from `@base-ui/react`; harmless.

## Behaviour differences caused by the new backend contract

- Changing the password no longer signs the current session out (identity revokes the other sessions
  and answers 204); the UI says so.
- Profile (name, email) saves go to `PUT /api/identity/profile`; preferences to `PUT /api/preferences`
  with the account `revision`. Each save is followed by `GET /api/account`, so the profile revision is
  not a concurrency guard for name/email any more.
- Client URLs are now real paths (`/devices`, ...) instead of in-memory state, so the Go host needs the SPA
  fallback for non-API paths.
- Not touched (outside `frontend/`, `internal/webui`, `third_party`): `chimera-provenance.json`, `licenses/`
  and root docs may still mention Chimera or `third_party/kit-account`.
