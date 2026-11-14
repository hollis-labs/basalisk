# Story

Video scripts and storyboards, reviewed with the writing team. A board of scripts by stage;
a script opened as a storyboard of scene cards; a scene's text edited in place. The design
is in Tesseract (`project/basalisk/knowledge/designs`,
`design_story_boards_scripts_storyboards_20261114`); the task is CW-20261114-0060.

## Where it is kept

The text lives in **Caster**, namespace `story`. Every write is a new revision there, so the
history of a scene is its revisions (`caster_log`). Basalisk keeps a working copy in
`story.db`, beside the app's database, for quick boards and for reading when Caster is away;
nothing lives only there once it is in Caster. Caster is reached through the Tether gateway
(`basalisk.tether_mcp`), like scratch and Cite. Without a gateway the app shows what the
copy holds and cannot write.

## The contract, `story/v1`

Names in the namespace use `.` between parts. Project and script slugs are lowercase
letters, digits and `-`, so the separators stay unambiguous.

- **Manifest** `caster://story/<project>.<script>`: markdown with YAML front matter.

  ```
  ---
  story: v1
  project: qspace
  script: launch-teaser
  title: QSpace launch teaser
  stage: draft            # idea | outline | draft | review | revising | approved | production
  round: 2                # the last feedback round sent
  length_s: 45            # target length, seconds
  platform: youtube-short
  owners: ["msg://agent/agent-mux/agt_ndfltf559l"]   # Tether addresses (rounds are mailed to them)
  task: CW-20261114-0101  # optional Torque task
  scenes: [s01, s02, s03] # the order; this list IS the storyboard order
  approved: {}            # at sign-off: scene id -> pinned revision
  ---
  Logline, audience, the one thing to remember, voice. Free prose.
  ```

- **Scene** `caster://story/<project>.<script>.<scene-id>`. Scene ids (`s01`, `s02`…; any
  `[a-z0-9-]`) are stable and never renumbered; order comes only from the manifest.

  ```
  ---
  story: v1
  id: s02
  title: The claim
  status: draft           # draft | ready | changes | approved
  duration_s: 6
  frames:                 # storyboard images, by reference; may be empty
    - {ref: "design-asset:<id>", caption: "wide on the hangar"}
  ---
  ## Visual
  ## On-screen text
  ## Narration
  ## Notes
  ```

- **Round** `caster://story/<project>.<script>.round-<n>`: what the owner sent in round n
  (step 2).

Keys the app does not know are kept as they are when it writes an item again: rewriting a
manifest changes only `scenes`, `stage`, `round` and `approved`, and a scene edit changes
only the title, the duration and the sections edited. Sections with other headings, and
text before the first heading, stay as they are.

## The app

- **Scripts board** (the first view): columns are the stages, a card per script with its
  project, scene count, stated length against the target, who has the next move ("waiting on
  you" in review, "with the team" while the team writes), draft notes and the last change.
  Filter by project. Dragging a card to another column sets its stage; a script reaches
  Approved only by sign-off. **New script** makes a manifest at `idea`.
- **Storyboard** (open a script): scene cards in the manifest's order, each with its number,
  status dot, title, duration, the first line of the Visual (or the first frame's caption),
  the first line of the narration and its notes. The header shows the stated total against
  the target and the narration at 150 words a minute. Drag a card to reorder (rewrites
  `scenes`); add, duplicate and cut a scene. A cut scene leaves the list but stays in Caster,
  listed under Cut scenes, from where it can be put back.
- **Scene detail** (click a card; beside the board): title and duration, and the four
  sections, each edited in place (click; ⌘↩ or clicking away keeps it, Esc does not).

## Keeping in step with Caster

- **Read-back.** The board reads the namespace when it opens and every 25 s; an open script
  every 20 s (not while a section is being edited). One `caster_ls` lists every item with its
  revision; only items whose revision moved are fetched.
- **Writes go to Caster at once**, each after reading the item again (Caster's put has no
  "only if still at revision N"):
  - The manifest is the contended item (order, stage): the change is applied to the
    manifest as it is now. A scene the team added while the owner was reordering keeps its
    place at the end.
  - A scene edit carries the revision it was begun on. If the scene moved on in Caster
    meanwhile, nothing is written over it: each changed section is kept as a suggestion (a
    draft note, quote = the newer text, replace = the owner's) on the newer revision, and
    the pane says so.

## Routes

`GET /api/story` (the board; `refresh=1` to read Caster now),
`GET /api/story/scripts/{key}` (a script, its scenes in order, cut scenes, draft notes),
`POST /api/story/scripts` `{project, script, title}`,
`PUT /api/story/scripts/{key}/stage` `{stage}`, `PUT /api/story/scripts/{key}/order` `{scenes}`,
`POST /api/story/scripts/{key}/scenes` `{after?, from?, title?}`,
`PUT /api/story/scripts/{key}/scenes/{id}` `{base_rev, title?, duration_s?, sections?}`
(409 when the scene moved on), `POST …/scenes/{id}/cut`, `POST …/scenes/{id}/restore`,
`GET /api/story/log?name=`, `GET /api/story/item?name=&rev=`.
