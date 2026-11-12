# Basalisk frontend

React SPA on the Hollis Labs GUI framework: `@hollis-labs/app-shell` driven by a Substrate
frontend manifest, with the published kits. Pins are exact and match `bom/bom.yaml` `kits:` in
`hollis-labs/substrate` (app-shell 0.1.0, kit-account 0.1.0, kit-settings 0.2.0, kit-dashboard 0.6.0,
design-tokens 0.5.0, design-components 0.5.1, design-app-runtime 0.5.0, design-bindings 0.2.0,
plugin-host-ui 0.3.0). Do not use `^`; bump them together with the Substrate BOM.

```sh
npm ci
npm run typecheck && npm run lint
npm run build      # writes ../internal/webui/dist, embedded by internal/webui
npm run dev        # Vite; proxies /api, /auth, /companion to 127.0.0.1:8085
```

## How the manifest is consumed

- `npm run manifest` runs `substrate frontend` for `../cmd/basalisk/substrate.yaml` and writes
  `src/generated/frontend.manifest.json` (checked in; regenerate when modules or Substrate change).
  It holds the routes and kits of the modules the binary links, and Basalisk's own pages.
- Basalisk's own pages are declared in `cmd/basalisk/app.ui.json`, named by `frontend.contributions`
  in `substrate.yaml`: a `ui.json`-shaped list of `nav.item` and `surface` items. The builder
  validates it against Substrate's `ui.Default` vocabulary and merges it into the generated manifest
  (module `basalisk`); `src/manifest.ts` only reads the result.
- `src/App.tsx` boots (`GET /api/setup`, `GET /api/account`), shows sign-in or first-run setup outside
  the shell, then renders `ManifestShell` with `pages` keyed by surface key and `icons` keyed by the
  manifest's icon names. The shell owns navigation and layout; there is no router, only
  `history.pushState`. The Go host must answer unknown non-API paths with `index.html`.

## API client

`src/api/account.ts` sends `X-Requested-With: XMLHttpRequest`, uses the cookie session, and reads
the flat `{code, message}` error body. A 401 other than `invalid_login` signs the user out.
Identity calls (`/api/setup`, `/api/login`, `/api/password`, `/api/identity/profile`) use the
identity module's snake_case fields; app routes keep their camelCase shapes.

See `CONVERSION_NOTES.md` for framework gaps found during the conversion.
