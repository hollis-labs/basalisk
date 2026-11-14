# Collab: working on things with the agent

Drafts, artifacts under review and diagrams are things the owner and the built-in agent work
on together, visually. They share one primitive, the **mark**: something pointed at, plus a
note. The owner selects what they mean and says what about it; the agent reads the marks,
answers them, adds its own and proposes changes as new revisions, which are shown as a
comparison with the one before. There is no approval flow in the views; that is done in chat.

## Where it is kept: contract `collab/v1`

Caster, namespace `collab`, through the Tether gateway (`basalisk.tether_mcp`). Every save is
a revision, which is what makes before/after possible. Nothing is kept in Basalisk.

- **Document** `caster://collab/<kind>.<slug>`, kind `draft`, `artifact` or `diagram`.
  Front matter: `collab: v1`, `kind`, `title`, `lang` (a draft), `project`, `ref` (what an
  artifact points at: `file:<root>:<path>`, a web address, `fragment:<id>`), `created`. The
  body is a draft's text, a diagram's JSON, an artifact's notes. Written with source
  `basalisk:owner` or `basalisk:agent`.
- **Marks** `caster://collab/<kind>.<slug>.marks`: JSON `{collab: "v1", marks: [...]}`. A
  mark: `id`, `rev` (the revision it was made on), `anchor`, `kind` (`comment`, `question`,
  `suggestion` with `replace`), `text`, `by` (`user` or `agent`), `state` (`open`, `done`,
  `declined`), `at`, `reply`.
- **Anchor**, by what is pointed at: a passage (`quote`, `prefix`, `line_start`,
  `line_end`); a region of an image, page or mock (`x`, `y`, `w`, `h` as fractions, `page`);
  a time in a video (`time`, seconds); a diagram `node` or `edge`; a mock element
  (`selector`).

A save carries the revision the edit began on; when the document moved on, nothing is
written (409) and the window offers the newer one. Marks are read again immediately before
each write. A text mark is found again in a later revision by its quote (the nearest
occurrence to its old line); when the passage was rewritten it is shown as detached.

## Routes

`GET|POST /api/collab`, `GET|PUT /api/collab/{name}` (`?rev=` for an older revision),
`GET /api/collab/{name}/log`, `POST /api/collab/{name}/marks` (`op`: add, update, remove).

## The agent

Tools (`frontend/src/collab/agent.ts`): `collab_list`, `collab_read` (the text with line
numbers and every mark) run at once; `collab_mark`, `collab_reply`, `collab_write` ask
first, like any write. The focused collab window tells the agent which document it shows
(`collab_document` in the desktop snapshot), so "this draft" needs no name.

## Drafts

Code, content and plans. Review shows the text by line: select a passage, then comment, ask
or suggest a replacement. Accepting a suggestion writes a new revision. Edit changes the
text (⌘S saves). Compare shows any revision against the latest. "Draft: start one from what
this window shows" makes a draft from the focused window's selection.
