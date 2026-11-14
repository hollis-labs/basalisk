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

## Where it is kept

- `scratch.db`, beside the app's database, is the working copy. Every view is a query over
  it, and it holds what Caster has no place for: done, and put away.
- **Caster** is where the text lives for everything else. Each item is written behind to
  `caster://scratch/<id>`, a new revision per edit, with its kind, day and links as tags
  (`scratch kind:todo day:2026-11-14 project:basalisk done`). A write that fails is tried
  again; an item says "saving…" until Caster has it. Nothing is ever deleted there:
  putting an item away adds the tag `archived`.
- Items made in Caster by something else are not read back in yet.

## Routes

`GET /api/scratch` (`day`, `kind`, `rel` + `value`, `open=1`, `q`, `archived=1`),
`GET /api/scratch/facets`, `GET /api/scratch/page` (the same filters, and `title`),
`POST /api/scratch` `{text, context}`, `PUT /api/scratch/{id}` `{text?, done?, archived?}`.
