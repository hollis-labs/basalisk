# Work

One engine of typed items, links and saved views, kept in Caster, with three apps as
presets over it: **Content** (ideas to published), **Planner** (the strategy level above
Torque) and **Writer** (books, TTRPG settings and campaigns). The design is in Tesseract
(`project/basalisk/knowledge/designs`, `design_work_engine_content_planner_writer_20261114`);
the task is CW-20261114-0076. It generalizes Story (`docs/story.md`), which stays on
`story/v1` for now.

Built so far: step 1 (the engine, Board, Detail, the Content app). Calendar, Outline and
Writer, Canvas, Flow, Planner, and rounds and agents follow.

## Where it is kept

- **Caster**, through the Tether gateway (`basalisk.tether_mcp`), like Story and scratch.
  Every save is a new revision; an item's history is its revisions (`caster_log`).
- A namespace per workspace, `w-<workspace>`, so a large book does not slow other
  listings. Caster makes a namespace on its first put; its name follows the same rule as
  item names (`^[A-Za-z0-9][A-Za-z0-9._-]*$`), so no fallback is needed.
- The workspaces are listed in the namespace `work`, one index item each.
- Basalisk keeps a working copy in `work.db`, beside the app's database, for quick views and
  for reading when Caster is away. Nothing lives only there once it is in Caster. Without a
  gateway the app shows what the copy holds and cannot write.

## The contract, `work/v1`

Workspace slugs are lowercase letters, digits and `-`. Item and view ids are 8 characters of
`[a-z0-9]`, made once, never reused, never renumbered.

- **Workspace index** `caster://work/<workspace>`: front matter and a body saying what the
  workspace is.

  ```
  ---
  work: v1
  workspace: hollis-channel
  title: Hollis channel
  app: content            # content | planner | writer
  preset: content         # the kind set it started from
  ns: w-hollis-channel    # where its items are
  owners: []              # Tether addresses
  created: "2026-11-14T17:13:57Z"
  ---
  Videos and articles about the lab.
  ```

- **Kind** `caster://w-<workspace>/kind.<kind>`: plain YAML. Copied from the preset when the
  workspace is made, so the workspace describes itself; it can be edited there.

  ```
  kind: video
  label: Video
  plural: Videos
  icon: clapperboard      # a lucide name
  stages: [idea, outline, draft, review, scheduled, published]   # may be empty (a reference kind)
  fields:
    - {key: publish_on, label: Publish on, type: date}   # text | number | date | enum | ref | refs | url | bool
    - {key: channel, label: Channel, type: ref}
  sections: [Summary, Outline, Script, Notes]   # the body's headings, in order
  children: []            # kinds it may hold, in order; empty: not a container
  color: red
  ```

- **Item** `caster://w-<workspace>/i.<id>`: front matter and a markdown body under the
  kind's section headings.

  ```
  ---
  work: v1
  id: z22gidco
  kind: video
  title: Lab tour
  stage: outline
  fields: {publish_on: "2026-12-01"}
  parent: ""              # an item id, or empty
  children: []            # the order of what it holds (the outline order)
  links: []               # [{rel, to}]: contains, depends-on, leads-to, appears-in, uses, relates
  material: []            # [{ref, caption}]: references, never copies
  published: []           # [{at, where, url, note}]: the publication log
  tags: []
  task: ""                # a Torque id (Planner, optional)
  cut: true               # only when cut: out of the views, still in Caster
  ---
  ## Summary
  ## Outline
  ## Script
  ## Notes
  ```

  `cut` is an addition to the design's contract: a cut item has `cut: true` and is taken out
  of its parent's `children`; restoring removes the key and puts it back at the end.

- **View** `caster://w-<workspace>/v.<id>`: plain YAML: `view` (`board | calendar | outline |
  canvas | flow`), `title`, `kinds` (what it shows; empty means every kind), `filter`,
  `group_by` (board: `stage` or an enum field), `date_field` (calendar), `root` (outline),
  `rel` (flow), `layout` (canvas and flow).

- **Round** `caster://w-<workspace>/r.<item-id>.<n>`: as in `story/v1` (step 7).

Keys the app does not know are kept as they are when it writes an item again, in the front
matter and inside `fields`; sections with other headings and text before the first heading
stay as they are.

## Presets

- **Content**: `idea`, `article`, `video`, `post`, `game`, `series` (holds articles, videos,
  posts and games), `channel` (a reference kind: where things go out). Stages
  `idea → outline → draft → review → scheduled → published`. Fields `publish_on` (date),
  `channel` (ref), `format`, `length`. A new Content workspace gets one Board view.

## The app (step 1)

- **Workspaces**: the app's workspaces, newest change first, with **New workspace** (title,
  name, what it is). Making one writes its kinds and its board view into `w-<workspace>`,
  then its index into `work` (the index is what makes it exist).
- **Board**: columns by `group_by`: the stages of the kinds shown, in order, or an enum
  field's options and "None". A card whose value no column names goes to **Other**, never
  lost. Dragging a card to another column sets that value: one new revision of the item with
  nothing else changed. The kind chips choose what the board shows and **By …** chooses the
  columns; both are kept in the view item. A column's **+** makes an item of a kind at that
  column's value and opens it.
- **Detail** (click a card; beside the view): title, stage, the kind's fields edited by type,
  the sections edited in place (click; ⌘↩ or clicking away keeps it, Esc does not), tags;
  links, material and the publication log as they are (edited from step 2); the owner's notes
  not yet sent; **Revisions**, with a word diff of each changed section from any revision to
  the current one. **Cut** takes the item out of the views; the header's "n cut" lists cut
  items to restore.

## Keeping in step with Caster

- **Read-back.** The workspace list reads `work` when it opens and every 25 s; an open
  workspace reads its namespace every 20 s (not while a section is being edited). One
  `caster_ls` per namespace (through `CallAll`, which reassembles a list the gateway cut);
  only items whose revision moved are fetched.
- **Writes go to Caster at once**, each after reading the item again (Caster's put has no
  "only if still at revision N"):
  - Moving a card, setting a field from a column, cutting, restoring and a parent's
    `children` apply the change to the item as it is now.
  - An edit carries the revision it was begun on. If the item moved on in Caster meanwhile,
    nothing is written over it: each changed section is kept as a suggestion (quote = the
    newer text, replace = the owner's) on the newer revision, any other change as a comment,
    and the pane says so.

## Routes

`GET /api/work?app=` (the workspaces and presets; `refresh=1` reads Caster now),
`POST /api/work` `{workspace, title, preset, about}`,
`GET /api/work/{ws}` (the workspace: index, kinds, items, views),
`POST /api/work/{ws}/items` `{kind, title?, stage?, parent?, fields?}`,
`GET /api/work/{ws}/items/{id}` (the detail),
`PUT /api/work/{ws}/items/{id}` `{base_rev, title?, fields?, sections?, tags?}` (409 when it
moved on), `PUT …/items/{id}/move` `{key, value}` (`stage`, or an enum or date field),
`POST …/items/{id}/cut`, `POST …/items/{id}/restore`, `DELETE /api/work/{ws}/notes/{id}`,
`POST /api/work/{ws}/views` and `PUT …/views/{id}` (the keys sent are set, null takes one
out), `GET /api/work/{ws}/log?name=`, `GET /api/work/{ws}/at?name=&rev=`.
