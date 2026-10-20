# Contributing

How a change gets from your clone into `main`. This is deliberately short: most of what you need is already written somewhere closer to the thing it describes, and this page points at it rather than keeping a second copy that drifts.

## Before your first change

`README.md` has the run and build steps. Run `lefthook install` — a tracked config installs no hooks by itself, and a clone that skips it has no commit or push checks and says nothing about it.

`AGENTS.md` is the fastest orientation to the layout and the boundaries that are not obvious from reading the code. It is written for an agent working in the repo, which makes it unusually direct about the things that bite.

## The sequence

1. **Branch.** `<type>/<short-slug>`, where the type matches the change — `feat`, `fix`, `docs`, `chore`. Nothing enforces this; it is what the history does.
2. **Change one thing.** A branch carrying two unrelated changes costs the reviewer the ability to accept one and question the other.
3. **Run the checks** when the change is done: `make test vet`, and from `frontend/` the `typecheck` and `lint` scripts if you touched the UI.
4. **Push, and open a pull request** against `main`.

Commit subjects follow the conventional-commit shape — a type, an optional scope, a colon, then the summary.

## What a pull request should carry

The reviewer was not there when you made the decisions. State what the change does, what it deliberately leaves alone, and the evidence that it works — the commands you ran and what came back, not a claim that it passes. If a number appears in the description, put the command that produced it beside it.

A change to authentication, session, origin or secret handling should name the test that proves it holds, and say that the test fails without the change.

## The one that cannot be undone

**Do not point tests or experiments at your real data directory.** Setup creates the owner once and closes permanently, and there is no account reset. Use a disposable `BASALISK_DATA_DIR`, and never create or modify the owner account of an instance you care about from a test or an agent.

## Things that surprise people

- **`internal/webui/dist/.gitkeep` must stay.** `go:embed` fails to compile without a match. Do not delete it and do not commit the built bundle beside it. Build the UI before the binary with `make all`.
- **No secrets or real config in the repository.** Operator descriptors, environment files and data live outside the checkout; only `*.example.*` files are tracked.
- **`.folio.yaml` is scaffold provenance.** Leave it as generated; record intentional dependency customization in `chimera-provenance.json`.
- **`third_party/kit-account` is an unpublished source snapshot** with its own provenance notes.

## What this does not cover

- **Which change is worth making.** There is no roadmap commitment here by design; that conversation happens in issues.
- **Release.** Basalisk has no tagged releases yet.
