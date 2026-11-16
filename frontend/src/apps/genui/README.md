# GenUI workspace

This isolated Desktop app presents reviewed account-preference surfaces using the shared GenUI kit and the existing ADE hub. It does not add an agent loop or register another copy of the server's show-pattern tool.

The host creates the form, immutable approval and stored-result list/detail. Form edits request a review; approval and confirmation use the native action protocol. Confirmation is an authenticated one-use acknowledgment, not reauthentication or MFA. The host owns current identity, grants, revision checks, durable action records and execution.

Discovery contains routing metadata. The app loads original surface bytes, admits the single declarative renderer through design-bindings, and recovers a recorded action before rendering ready controls. Window arguments retain only the surface ID. Grants, proofs, envelopes and drafts are never placed in window persistence. Uncertain outcomes stay unresolved; polling only discovers surfaces and never repeats an action. Dismissal does not cancel an operation.

ADE exposes observation, retained-surface selection and focus. It cannot submit, approve, acknowledge or change preferences. The workspace uses inline host presentation; the wider Desktop shell migration is separate work.

## Verification

`tests/adoption.test.tsx` uses the real shared kit with synthetic HTTP responses to exercise exact draft serialization, confirmation, result navigation, reconnect-before-render, unknown outcomes, invalid raw JSON, session loss, routing-only persistence and ADE refusal. These are component adoption checks, not proof of backend identity or durable execution. The paired application acceptance must use actual isolated identity, store and HTTP services.

Run the focused suite from `frontend` with Vitest, Testing Library React and jsdom available:

```sh
npx vitest run --config src/apps/genui/tests/vitest.config.ts
npm run typecheck
npx biome check src/apps/genui src/api/genui.ts
npx eslint src/apps/genui src/api/genui.ts
npm run build
```

The workspace requires the reviewed GenUI package with matching app-shell and design-bindings dependencies. Local integration overlays do not establish a published package pin; normal dependency and test-tool declarations are required before landing.
