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

## Viewer

An artifact under review: an image, a video, a recording, a PDF or an HTML mock, opened from
a file (`ref: file:<root>:<path>`). Drag a box on a picture, or click for a pin, and say what
about it. A video or a page is marked the same way after **Mark**, which lays a sheet over it
so the drag is not taken by its own controls; a video's marks remember the moment and are
drawn only near it. **Note** marks the whole thing. The artifact is made in Caster when it is
first marked; opening the same file again finds it. Pictures, video and sound open here from
Files; "Review: mark up the file this window shows" brings a PDF or a mock here.

Files are served as the Files app serves them: an opaque origin, no script. A mock is shown
as it renders without script. The agent reads an artifact's marks as regions (fractions of
the width and height) and times, and can look at the pixels (below).

## Diagram

Architecture and flow drawings, made to be read: for understanding a complex system, and to
show it to others (explainer docs, videos). The body is JSON the agent can read and write:

```json
{"diagram": "v1", "theme": "light",
 "legend": {"flow": "automated progression", "human": "human decision"},
 "groups": [{"id": "exec", "label": "Agentic execution", "subtitle": "Ticket selection and repair", "tone": "blue"}],
 "nodes": [{"id": "dispatch", "label": "Dispatcher agent", "subtitle": "selects eligible tickets",
            "kind": "agent", "group": "exec", "step": 1, "tone": "blue", "tag": "pipeline run",
            "items": ["Fix batch 1", "Fix batch 2"], "highlight": true, "note": "…", "x": 286, "y": 60}],
 "edges": [{"id": "e1", "from": "backlog", "to": "dispatch", "label": "eligible", "style": "flow"}]}
```

Only `id` and `label` are needed; everything else is presentation, and a diagram with none of
it draws plainly.

- **Nodes.** `kind`: service, store, queue, client, external, actor, agent, step (cards with a
  default icon and tone), decision (a diamond), start and end (pills), note (a sticky note).
  `subtitle` is a second, monospace line; `icon` a lucide name (the inspector lists them);
  `tone` (blue, amber, red, green, violet, teal, pink, slate) colours the card; `step` draws a
  numbered badge; `tag` a small pill in the corner; `items` inner rows; `highlight` marks the
  node the diagram is about; `w` a wider card (120 to 600).
- **Lanes** (`groups`): titled panels around their nodes, with a `subtitle` and a `tone`.
  Dragging a lane moves its nodes.
- **Edges** are routed at right angles between ports on the facing sides (`fromSide`,
  `toSide` choose them), avoiding other nodes where a simple route does, with the label as a
  chip on the line. `style` says what an edge is: flow, human, feedback, repair, async, data,
  muted; each has its colour and dash, and the **legend** under the drawing explains the
  styles in use (`legend` renames them; `legend: false` hides it). `both` draws two arrows.
- **Look** (`theme`): app (the default) draws in the desktop theme's colours (its background,
  surfaces, text and accent, its terminal colours as the tones) and follows it when it changes;
  dark, light (for documents) and neon (for video) are fixed looks, kept with the diagram so an
  export looks the same. `background` (#rrggbb) gives the canvas its own colour; `grid` is
  lines (a faint blueprint grid on the drawing's plane, the default), dots or none; both are set
  in the panel when nothing is selected. The colours live in `apps/diagram/theme.ts`, the
  Diagram's theme layer (listed as such in `eslint.config.js`): the drawing is plain SVG with
  its colours written in, and its shadows are shapes, not filters (WebKit draws a filtered
  shape blurred when scaled).
- **Layout.** A diagram whose nodes have no `x` and `y` is laid out when it opens: ranked
  along the flow (loops left out), each lane in its own band of columns, a long lane wrapping
  back like a snake after three columns. **Tidy →** and **Tidy ↓** do the same to any diagram
  (left to right, top to bottom). Ids are stable; the comparison goes by id.

The window: the canvas fills it, and the controls float over it: the title and the list's
button top left, the actions top right, zoom and fit bottom left. The list of diagrams is a
drawer; the inspector and the marks open under the actions, 10 px from them and from the edges,
when a node, an edge or a lane is selected, or with the marks button, and Esc closes them. Double-click for a node, drag a node
to move it, drag from the dot on a selected node to another node for an edge, Delete removes
what is selected; the wheel zooms and dragging the background pans; the floating controls
zoom and fit (it fits itself on open). ⌘S saves a revision.

**Compare** draws any revision against what is on the canvas: added in green, changed in
amber (any of the fields above), removed in dashed red at its old place, only moved as
dotted. Diagrams are not functional; they are a picture of how things fit, kept current by
saving a revision when the thing changes.

The agent's `collab_write` carries a short guide to writing one well: lanes for stages or
places, a few words per label, steps along the main path, one highlight, tone for meaning,
two- or three-word edge labels, and no positions so the layout is done for it.

## Looking: the agent sees what is pointed at

A tool can hand the model pictures with its text (`withImages` in
`frontend/src/agent/tools.ts`). The page posts them with the tool result; the server holds
them in memory by call (`internal/agent/images.go`) and puts the pictures of the last three
such results into the request as image blocks. An older look stays in the thread as its line
of text, and pictures are not kept across a restart: the agent looks again.

- `desktop_look` (runs at once, like any read): a picture of one window, the focused window
  or the whole desktop, as the desktop app draws it (`GET /api/shell/snapshot`, cropped to
  the window and scaled to at most 1568 pixels a side). Marks drawn in the Viewer or on a
  diagram are in the picture. A Browser window's page is a separate native view and is not.
  It needs the desktop app.
- `collab_look`: an image under review from the file itself, with the open region marks
  drawn and numbered, or one mark's region close up.

What a look sends: the pixels of that window go to the model provider with the request, as
the text of a window already does when the agent reads it.
