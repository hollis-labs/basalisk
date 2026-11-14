# Work

One engine of typed items, links and saved views, kept in Caster, with three apps as
presets over it: **Content** (ideas to published), **Planner** (the strategy level above
Torque) and **Writer** (books, TTRPG settings and campaigns). The design is in Tesseract
(`project/basalisk/knowledge/designs`, `design_work_engine_content_planner_writer_20261114`);
the task is CW-20261114-0076. It generalizes Story (`docs/story.md`), which stays on
`story/v1` for now.

Built so far: step 1 (the engine, Board, Detail, the Content app), step 2 (Calendar, the
publication log, material by reference) and step 3 (Outline, the Writer app, links both
ways) and step 4 (Canvas). Flow, Planner, and rounds and agents follow.

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
  `channel` (ref), `format`, `length`. A new Content workspace gets a Board and a Calendar
  view; the header's **+** adds either to any workspace.

- **Writer**: the manuscript, `part` (holds chapters), `chapter` (holds scenes) and `scene`,
  with stages `idea → outline → draft → revise → final`; the codex, `character`,
  `location`, `item`, `faction` and `lore`, with no stages; and for play, `session` (stages
  `planned → prepped → played`, holds encounters and scenes) and `encounter`. Scene fields:
  `pov` (a character), `location`, `when`; a scene's words are counted from its text, not
  kept in a field. Character sections: Role, Description, Voice, Arc, Notes. Other fields:
  a location's `region`, an item's `owner`, a faction's `members` (refs) and `base`, an
  encounter's `location`, a session's `played_on`. A new Writer workspace gets an Outline
  (the manuscript and sessions; it opens on it), a Board (parts, chapters, scenes by stage),
  a Codex (a board of the codex kinds, a column each) and a Canvas.
- A `ref` or `refs` field's `options` name the kinds it may point at (`pov: [character]`);
  empty means any kind. This is an addition to the contract's use of `options`, which
  otherwise lists an enum's values.

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
  links as they are; material and the publication log (step 2); the owner's notes
  not yet sent; **Revisions**, with a word diff of each changed section from any revision to
  the current one. **Cut** takes the item out of the views; the header's "n cut" lists cut
  items to restore.

## Step 2: Calendar, the publication log, material

- **Calendar**: items placed by the view's `date_field` (the first date field of the kinds
  shown when it names none; `publish_on` in Content), by month or by week (the window's own
  choice, not saved). Dragging an item to another day sets that date: one new revision of
  the item, nothing else changed. Items with no date wait in the **No date** list beside the
  grid; dropping one there takes the date out. A day's **+** makes an item on that day. The
  kind chips and the date field are kept in the view item, as on the board. Published items
  are struck through.
- **The publication log** (`published`): when, where, an address and a note. Publishing is
  not done here; this records it. When an item reaches the stage `published` (a board drag,
  the stage menu) and its log is empty, the log gets its first entry: when is `publish_on`
  if that is set and not in the future, else today; where is the title of the item's
  `channel`. A log that has entries is never touched by a move. The detail edits entries
  (click one), adds them (**+ Log a publication**) and takes them out. `at` is `YYYY-MM-DD`
  or an RFC 3339 time; `url` is http(s).
- **Material**: references, never copies, shown as Cite chips where Cite knows the kind
  (`tach:fragment/<id>`, Tesseract and Torque refs, links; other text as it is). Paste any
  reference, or **From Fragments…** to search the Fragments Engine library and attach a
  fragment as `tach:fragment/<id>` with its title as the caption. The picker uses the
  Reader's route (`/api/reader/items`, FE's reader with the owner's integration), which is
  the read path the app has; `internal/feread` (the `dataread` provider) is not wired into
  the app yet. Taking material off does not touch the thing itself.
- Setting one field keeps the item's layout: a `fields` map written on one line stays on one
  line, and a quoted value stays quoted, so a calendar drag shows as one changed value.

## Step 3: Outline, Writer, links both ways

- **Outline**: the `children` tree from the view's `root` (an item) or from the top. A
  parent's `children` list is the order; an item that names a parent missing it from the list
  goes at the end, never lost. The top level is ordered by the view's `layout.order`, then by
  kind and title. Drag a row onto another row's top or bottom edge to put it before or after
  that row (under the same parent), or onto its middle to put it inside, when that row's kind
  holds the dragged kind. Moving to another parent changes the old parent's `children`, the
  new parent's `children` and the item's `parent` (`PUT …/items/{id}/place`
  `{parent, before, view}`), each applied to the item as it is now; an item never goes inside
  itself. Words roll up from scenes to chapters to parts. A container row's **+** makes an
  item inside it; **New** makes one at the top. Cut items stay in the header's cut list.
- **Board by kind** (`group_by: kind`): a column a kind, as the Codex. Moving a card between
  columns does nothing (an item does not change kind); a column's **+** makes that kind.
- **Links** in the detail, both ways: where the item sits (in its parent, what it holds),
  its own typed links (`appears-in`, `leads-to`, `depends-on`, `uses`, `relates`; added and
  taken out here, kept in `links`; `contains` is the outline and is refused), and
  **Pointing here**: other items' links to it and their `ref`/`refs` fields naming it
  (Mara: "Arrival · point of view", "Salt guild · members"). Each opens that item.
- A workspace opens on its preset's first view (`default_view` from `GET /api/work/{ws}`).
- The Published section shows for kinds with a `published` stage, or when the log has
  entries.

## Step 4: Canvas

- Cards placed freely, **stacks** (titled piles of cards) and free **notes**, on a plane
  that pans (drag the background, or scroll) and zooms (⌘ or Ctrl with scroll, or the
  − / % / + buttons; the % goes back to 100%). Pan and zoom are the window's own, not saved.
- The tray beside the canvas lists the shown kinds' items not on it. Drag a card from the
  tray onto the canvas or into a stack, between stacks, from a stack onto the canvas, or back
  to the tray to take it off. A click on a card opens it. A card is on the canvas or in one
  stack, never both.
- A stack: **Stack** makes one (type its title; double-click to rename); drag it by its
  header; × takes it off (its cards go back to the tray). Its menu binds it: **Just a pile**
  (arrangement only), **Sets stage: …** or **Puts in: …** (an item that holds others).
  Dropping a card into a bound stack also moves the item to that stage (Move) or under that
  parent (Place); when the kind has no such stage or cannot go there, the card stays in the
  stack and the window says the item was not changed.
- A note: **Note** makes one; double-click to write; ⌘↩ or clicking away keeps it, Esc does
  not; drag it by its top bar; × takes it off.
- The arrangement is the view item's `layout`: `cards: {<id>: {x, y}}`,
  `stacks: [{id, title, x, y, items, bind: {key: stage|parent, value}}]`,
  `notes: [{id, x, y, text}]`. Every change is a patch by id
  (`PUT /api/work/{ws}/views/{id}/layout` `{cards?, stacks?, notes?}`, null takes one out)
  applied to the view as it is now, so a change made in another window is not written over;
  other layout keys (an outline's `order`) are kept. Coordinates are whole plane units.
- Any workspace can have a canvas (the header's **+**); a new Writer workspace has one.

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
`PUT /api/work/{ws}/items/{id}` `{base_rev, title?, stage?, fields?, sections?, tags?,
material?, published?, links?}` (409 when it moved on; `material`, `published` and `links`
replace the lists), `PUT …/items/{id}/place` `{parent, before, view}`, `PUT …/items/{id}/move` `{key, value}` (`stage`, or an enum or date field; an empty
value takes a date out),
`POST …/items/{id}/cut`, `POST …/items/{id}/restore`, `DELETE /api/work/{ws}/notes/{id}`,
`POST /api/work/{ws}/views` and `PUT …/views/{id}` (the keys sent are set, null takes one
out), `PUT …/views/{id}/layout` (a canvas patch), `GET /api/work/{ws}/log?name=`, `GET /api/work/{ws}/at?name=&rev=`.
