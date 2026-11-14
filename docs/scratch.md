# Scratch

Quick notes, commands, follow-ups not to forget, and ideas. Each one is a small item of its
own. A "scratch file" is a view put together from items (a day, a project, a task, an
agent, the open follow-ups, a search); it is never the unit that is stored.

## Writing

Type as you would. A few markers say what a thing is and what it belongs to, and they are
the portfolio's directives (`libs/ui/ui-go/directives`), so anything that reads directives
elsewhere reads these too.

| You type | It is |
|---|---|
| plain text | a note (it may have paragraphs) |
| `::idea …`, `::follow-up …`, `::todo …`, `::q …` | an idea, a follow-up, a todo, a question |
| `:idea …` | the same: one colon at the start of a line is read as two |
| `$ git status` | a command |
| `[] call back` or `- [ ] call back` | a todo (`[x]` is a done one) |
| `::anything …` | an item of that kind (`::blog-draft`, or its alias `::bd`) |
| `+basalisk` | tied to a project |
| `@devtoo` | tied to an agent |
| `#clip` | a tag |
| `CW-20261114-0045` | tied to a task |
| `::config project=basalisk, task=CW-…` | ties everything after it in the same capture |

Each directive, command or checkbox line starts an item; the plain lines under a directive
belong to it, up to a blank line. So one capture can hold several items. A command's own
text is not read for `+`, `@` and `#`. Short forms: `::n` note, `::t` todo, `::fu`
follow-up, `::i` idea, `::q` question.

Where you were when you wrote it (the window, file, page or terminal folder) is kept with
the item.

## Where to write

- **The capture box** (⌥⇧S, or ⌥⇧F and ⌘⇧D to switch): the Frag box set to scratch. Tags
  in its tags field tie every item written. It remembers which of the two it was last set to.
- **The Scratch window**: the box at the top; Enter keeps it, ⇧Enter is a new line.
- **The agent**: `/s <text>` keeps it word for word with no model; the agent can also add,
  list and tick items (`scratch_add`, `scratch_list`, `scratch_done`).

## The window

Views are on the left: Today, Open follow-ups, Everything, recent days, kinds, and the
projects, tasks, agents and tags items are tied to, each with its count. Clicking a chip
on an item goes to that view. In a view: tick a todo, follow-up or question; double-click
an item to edit it in place (⌘↩ or clicking away keeps it, Esc does not); copy it; run a
command in the terminal; put it away. "page" copies the view as one markdown page, which
reads back in as the same items (`GET /api/scratch/page`).

**Sessions.** An item written while a work session is running is tied to it (and to the
session's project, when the text names none). Sessions are a group on the left, by title.

**Completion.** While typing, `::` offers the markers, `+` the projects, `@` the agents and
`#` the tags already in use, most used first. Arrows choose, Tab or Enter takes one, Esc
closes the list. It works in the window and in the capture box when it is set to scratch.

**Making an item into something more** (the arrow on an item). The item stays, with a chip
for what it became:

- **Torque task**: the first line is the title, the rest the description. It goes in the
  Torque project whose name is the item's `+project`, when exactly one matches, else in none.
  The item is tied to the task id. Needs the Tether gateway.
- **Note**: a markdown file `scratch/<day>-<first words>.md` in the notes folder. An
  existing file is never replaced; the name gets `-2`, `-3`.
- **Fragment**: queued for Fragments Engine like a capture, with the item's tags and `scratch`.
- **Task agent**: the text is the brief of a task agent that works in the background
  (docs/agent.md); its window opens. `… ::delegate` marks an item meant for this.

## Where it is kept

- `scratch.db`, beside the app's database, is the working copy. Every view is a query over
  it, and it holds what Caster has no place for: done, and put away.
- **Caster** is where the text lives for everything else. Each item is written behind to
  `caster://scratch/<id>`, a new revision per edit, with its kind, day and links as tags
  (`scratch kind:todo day:2026-11-14 project:basalisk done`). A write that fails is tried
  again; an item says "saving…" until Caster has it. Nothing is ever deleted there:
  putting an item away adds the tag `archived`.
- **Read back.** Items in Caster's `scratch` namespace that are not here (an agent put them
  there, or another machine) are brought in when the window is opened, at most every two
  minutes. Only text up to 8 KB; a larger document parked in the namespace is left alone.
  They keep Caster's name (`cst_<name>` here) and are not written back until edited.

## Routes

`GET /api/scratch` (`day`, `kind`, `rel` + `value`, `open=1`, `q`, `archived=1`),
`GET /api/scratch/facets`, `GET /api/scratch/page` (the same filters, and `title`),
`POST /api/scratch` `{text, context}`, `PUT /api/scratch/{id}` `{text?, done?, archived?, link?}`,
`POST /api/scratch/{id}/task` (make a Torque task of it). A task agent is started with
`POST /api/agent/tasks` `{brief, title?, client_id, context, tools}`.
