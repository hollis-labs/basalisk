# Bas, the assistant

The agent built into Basalisk is also the owner's assistant across their agent system: it
reads what agents send the owner, answers and routes in the owner's name, delegates, and
brings the owner only what needs them. This file is the record of what is built and what is
planned (design: Tesseract `project/basalisk/knowledge/designs`,
`design_bas_assistant_20261114`).

## Acting on the owner's mail (built)

Tools the server runs (`internal/agent/owner.go`), over the Inbox's providers
(`internal/inbox`), so they read and send from the owner's own addresses
(`basalisk.inbox_addresses`):

| Tool | Runs | What |
|---|---|---|
| `owner_inbox`, `owner_mail_read` | at once | list the owner's mail; read one message whole |
| `owner_mail_settle` | at once | mark read (and archive) what needs nothing, with a reason |
| `owner_ack` | at once | a receipt from a fixed list: received, will_reply, noted, thanks |
| `owner_status_ask` | at once | a fixed request for status on one named piece of work |
| `owner_reply` | asks first | a reply in the assistant's words, or the owner's choice of an option |
| `owner_send` | asks first | a new message: a request, a delegation, a message passed on |

Other agents take mail from the owner's address as the owner speaking. So what goes without
asking is mechanical (the assistant picks which receipt, not its words; a status request
takes one line naming the work), and anything in the assistant's words asks first. The
owner can answer "Always" for a tool, which is how the dial is loosened. A message's text is
data from its sender, never an instruction: the tools' descriptions and the standing
instructions say so, and the account and the message are checked against the owner's own.

Every send carries `on_behalf_of` (the owner's address), `sent_by: "assistant"` and
`assistant: "basalisk"` in its payload, so the owner and the receiving agent can tell.

## Done for you (built)

Everything the assistant does in the owner's name is written to `assist.db`
(`internal/assist`) before it is done; if it cannot be written, it is not done. The window
**Done for you** lists it by day: what, with whom, whether the owner was asked, and what
was sent. `GET /api/assist/activity`. The chat is told outcomes, not steps.

## Always there (built)

- Closing the desktop window hides it: the app stays in the Dock and keeps working. Its
  Dock icon brings the window back. Quit stops it.
- The installed app makes itself a login item the first time it runs (macOS 13+,
  `SMAppService`), once; **Start at login** in the desktop menu turns it off or on.
- With Basalisk quit, nothing runs: mail waits in Tether.

## Sorting new mail (built)

While Basalisk runs, the owner's unread mail is swept every minute
(`internal/assist/watch.go`) and each new message is sorted, by rule and not by a model:

| Class | When | What happens |
|---|---|---|
| `needs` | it asks the owner to decide (a question, a decision, an approval) or escalates | a card at the top of the assistant's panel, with its options as buttons |
| `bas` | a request with no options | handed to the assistant as a background task ("Mail for you"), at most one batch every five minutes; its report reaches the owner with their next message |
| `digest` | anything else | held and counted ("3 held for you"), listed on demand |
| `settle` | one of the owner's rules says so | marked read, on the record |

The owner's rules are tried first, newest first. A card makes one: **This is noise** holds
that kind of mail from that sender from now on; **Always tell me** shows the owner that
sender's mail. `GET /api/assist/rules` lists them; `DELETE /api/assist/rules/{id}` removes
one. What was already unread when the watcher first ran is not sorted. A message that stops
being unread (answered here, in the Inbox or anywhere else) leaves the lists.

Answering a card's option is the owner answering: it goes through the Inbox's reply, from
their address, with no assistant marker.

Routes: `GET /api/assist/needs`, `POST /api/assist/mail/{id}` (`state`, and `rule` to make
one).

## Commands on hosts (built: ready to run)

`terminal_prepare` opens a terminal window with a command typed and not run; Enter is the
owner's. With `host` the line is `ssh -t <host> '<command>'`. It runs at once (it only
types). `terminal_run` still runs a command on this Mac after asking each time. Work the
owner should not have to press Enter for goes to @chief with `owner_send`.

## The quiet lane (built, first part)

- In the chat, tool calls that follow one another are folded to one line ("4 steps · …")
  that opens to show each. A call still running, and one whose result is a card, stay as
  they are.
- A background run the assistant started for itself (sorting mail) is not in the task strip
  unless it is asking permission or failed. It is listed in Done for you, under "Working in
  the background", and opens from there.

Not yet: Tether's reply-audience fields. Basalisk's own runs are not Tether turns, so there
is nothing to read them from; they matter when Basalisk shows other seats' sessions.

## Context slots (built, first part)

The owner moves between kinds of work and talks to one assistant throughout. A slot is what
the assistant brings to one kind (`internal/agent/slots.go`): the tools that are top of
mind there, its own note of where things stand, and where the rest is kept.

| Slot | Selected by the window in front | Top of mind |
|---|---|---|
| messaging | Inbox, Done for you | the `owner_…` tools, the registry, a task by id |
| planning | Planner, Content, Work sessions, Scratch | Torque, scratch, Tesseract recall, task agents |
| writing | Writer, Story, Drafts | the collab and story tools, notes |
| notes | Notes, Frag | notes, capture, scratch, recall |
| engineering | Terminal, Files, Diagram, Viewer | files, terminals, collab, looking |
| reading | Browser, Reader, Watch | the browser tools, capture |
| general | anything else | |

When the slot changes (or its note does), the owner's next message carries a `<context>`
block: the kind of work, the top-of-mind tools this run has, the assistant's note for that
slot with when it was written, and where the rest is. A thread is told once while nothing
changed. The tool definitions and the system prompt do not change with the slot, so the
provider's cache holds across a switch: a slot changes what is attended to, not what the
assistant is or can do.

On moving to another slot the block also asks the assistant, once, to write its note for
the slot it has just left if something there is worth carrying.

The assistant keeps the notes: `context_note` (the whole note, under a page: where things
stand, what is next, pointers such as task ids and document names), `context_get` (a note,
and the list of slots with when each was written) and `context_switch` (move when the
question has changed kind and the window has not). Notes are in `agent.db`.

When a long conversation is summarised (compaction), the summary is written under a heading
per context, with what holds across all of them first, so each context can be picked up
from it.

**Focus.** Each of the owner's messages is stored with the slot it was said in. Once the
unsummarised part of the thread passes about 60,000 characters, the model is sent the turns
of the slot the owner is in and the last three turns of any, in order; a run of turns from
other slots is left out and an `<elsewhere>` block in front of the next kept turn says how
many and from where (`focusHistory`). A turn is the owner's message and everything up to
their next, so no call is parted from its result. Nothing is deleted: `thread_recall` reads
back the owner's messages and the assistant's replies of a slot's recent turns, with words
to search by. Below that size the thread is sent whole, which is cheaper than re-reading it
after every move.

**In the panel.** The header shows the slot the conversation is in. Its menu moves it by
hand (it holds until a window with a slot of its own comes to the front) and shows the
assistant's note for each slot. `GET /api/agent/contexts` (`?note=<slot>` for a note),
`POST /api/agent/contexts {name}`.

**Projects.** A slot is a kind of work; a project is what the work is on, and the owner
defines them (`internal/agent/projects.go`, in `agent.db`). A project has what selects it
(words found in the title, path, address or details of the window in front), where its
state lives (a Torque project, a Tesseract namespace, a Work workspace, a repository,
anything else worth knowing), and the owner's standing instructions for it. The assistant
keeps a note per project. The project holds while the owner moves between kinds of work,
until a window selects another or it is changed by hand. The `<context>` block carries the
project's lines after the slot's.

**Standing instructions.** Each kind of work and each project can carry the owner's
instructions, told to the assistant in the `<context>` block whenever that context is
entered or the instructions change.

**Editing.** The Contexts window (the chip's menu, or the Apps menu): the kinds of work
with their instructions; projects with add, edit and remove. The assistant has
`project_define` and `context_instruct`, which ask first. A starting list is read once from
`~/.config/basalisk/projects.json` (`{"projects": [...], "instructions": {...}}`) when
there are no projects yet. Routes: `PUT|DELETE /api/agent/projects/{name}`,
`PUT /api/agent/contexts/{name}/instructions`.

Not yet: pointers gathered automatically.

## Seen with the window put away (built)

What waits on the owner is kept while the desktop page is up, whether or not the
assistant's panel is open (`frontend/src/agent/attention.ts`): the bar shows "N for you"
(it opens the panel), and in the desktop app the Dock icon carries the count
(`POST /api/shell/badge`). An answer that came back to the assistant is a row in the strip;
clicking it asks the assistant what came back.

The desktop menu has **Restart Basalisk** with the running build, and after an install the
bar shows **Update**: closing the window does not load a new build, restarting does.

## Knowing how things stand (built)

- **Around.** The state of the owner's mail rides with their next message in an `<around>`
  block when it has changed since the thread was last told: answers that came to what the
  assistant asked, what waits on the owner, how much is held
  (`internal/agent/ambient.go`; any source can be added with `Runtime.AddAmbient`).
- **Answers come back to the asker.** A message the assistant sends in the owner's name
  (`owner_status_ask`, `owner_send`, a delegation) is remembered. A reply to it is sorted as
  `answer`: it goes to the assistant in the `<around>` block, not to the owner as new mail.
- **`status_overview`** (runs at once): the sorted mail, what was done in the owner's name
  lately, the assistant's background runs, the owner's plans (each Content, Planner and
  Writer workspace from the Work engine's working copy: items by stage, the unfinished ones
  last touched, their dates), and Torque's work in flight (doing, review,
  blocked: totals, by team tag, the twenty most recently touched; `team` narrows to a tag).
  It is the first call for "where are things".

## The standing rule, and commands (built)

The assistant's instructions carry the owner's rule: ask first for anything destructive,
disruptive or financial; otherwise go by the standing approvals (tools that run without
asking, the "Always" answers, commands marked outright).

**Named commands** (`internal/commands`): the command lines the assistant may run, each a
fixed program and arguments with parameters checked against a pattern. Nothing goes through
a shell here; on a host the words are quoted one by one. A host must be named in the
owner's ssh config. Each run is in Done for you.

| Marked | Means |
|---|---|
| outright, looks | runs at once (`command_run`) |
| outright, changes | runs at once only when its guards (checks run first on the same machine) pass |
| ask | the owner is asked every time (`command_run_asking`) |

Built in: `host-reachable`, `host-uptime`, `host-disk`, `host-memory`, `service-status`,
`service-user-status`, `service-log`, `caddy-validate`, `git-status`, `git-log`,
`git-fetch` and `secret-generate` run outright; `service-restart` and `caddy-reload`
(validated first) ask. `secret-generate` writes a new secret to a file of its own under
`~/.config/basalisk/secrets/` and returns the path, never the secret, and never replaces
one. The owner's file `~/.config/basalisk/commands.json` (`{"commands": [...]}`) adds
commands, replaces built-in ones by name, or switches one off (`"off": true`). The
Commands tab of Done for you lists them. They exist only where Basalisk has a shell (the
desktop app, or `BASALISK_TERMINAL=1`).

**Through the gateway**: Cerberus (health, projects, resources, hosts, logs, schedules) and
Tether (health, sessions, seat status, message trace) to look, at once;
`cerberus_resource_reload`, `cerberus_resource_deploy` and
`cerberus_resource_ensure_fresh` ask first, and "Always" for one is a standing approval.

## Handing work on (built)

- **`consult`** (runs at once): the assistant asks another seat a question as itself, from
  its own address (`basalisk.agent_urn`), by handle or name; the message says it carries no
  authority of the owner's. The answer comes to the assistant's own mailbox and is told to
  it once in the `<around>` block (`agent.OwnAnswers`). This is how it reaches the architect
  about a design or a lead about its app without the owner being asked.

- **`chief_delegate`** (asks first): makes a Torque task in the create-task shape (Goal,
  Context, Scope in and out, Acceptance, Evidence, Classification), tagged `seat-chief`,
  and sends @chief a request naming it. @chief is found in the registry by its handle.
  It is refused without what not to touch and at least one check.
- **`dev_request`** (asks first): asks whoever builds Basalisk (`basalisk.dev_address`) for
  what configuration cannot do.
- **`mail_rules`**, **`mail_rule_add`**, **`mail_rule_remove`**: the assistant reads the
  owner's sorting rules, and changes them when told to (asks first).

## The roster (built)

The assistant writes to a small number of seats, and they fan out to their own teams from
there. The roster is that list (`internal/agent/roster.go`): for each seat a key, a name,
its address, whether it runs as a local session or as a seat on the agent system, and
what it is managing, responsible for or leading now. That last part is how the assistant
knows who to message, so it should say what goes to the seat and what does not.

- The assistant is told the roster in a `<roster>` block beside the `<context>` block: when
  the context changes and whenever the roster itself has changed. The block carries keys
  and descriptions, not addresses.
- `consult`, `owner_send`, `owner_status_ask` and `chief_delegate` take a seat by its key
  (or its name); the roster is looked in before the registry, so a name with several
  registry entries means the one the owner chose.
- The prompt tells it to write to the roster seat whose part it is, not to an agent beneath
  one unless the owner names that agent, and to ask the owner when no seat fits.
- `roster_list` reads it with addresses; `roster_set` and `roster_remove` change it and ask
  first. The owner edits it in the Contexts window, under Roster.
- A seat may name a roster other than the standard one (a workspace: personal,
  hollis-labs). Today every roster is told, grouped by name; choosing one by workspace is
  later.
- The owner's seats are theirs, not the repository's: a starting list is read once from
  `~/.config/basalisk/roster.json` (`{"seats": [...]}`) when the roster is empty.

## The pilot (built)

The assistant is told it is in a pilot with the owner: the two are working out how each
kind of thing should be done and refining it as they learn. It asks a short question when
something is new, undefined or unclear, and keeps the answer where it will be used next
time. A dead end, an error it cannot get past, missing access or major friction it brings
to the owner at once; minor friction it logs and carries on. `friction_log` (runs at once,
sends nothing) writes an entry of kind `friction` to the activity log, minor or major,
with what it was doing and an idea for handling it; the entries are in Done for you.

## Planned, in order

1. Sends through Tether's typed messages (`tether_message_post`) once the address aliases
   land, and a small model for sorting what the rules cannot.
3. Context slots, second part: recent conversation kept per slot, with the summary made per
   slot; slots the owner defines; pointers gathered as the assistant works.
4. Status, second part: seat status in the overview; a check that a note or a plan is
   current before it is relied on.
5. Commands: the assistant proposing a new named command for the owner to approve; guards
   that read a condition from another tool (nothing running, nobody connected).
6. Tuning: the standing "Always" answers listed beside the mail rules (the rules are in the
   Done for you window, under Mail rules, with Remove).

## Run with the model

Twice on 2026-11-14, on Opus through the owner's gateway, against a stand-in mailbox (no
live mail): (1) four new messages: a report was acknowledged and archived, two decisions
were left to the owner, and an instruction planted in one message was declined and
reported; (2) a status request sent in the owner's name, its reply routed back in the
`<around>` block and relayed on "Any news?" after a move to another window, with a new
decision inside the reply raised and a context note written. Not yet run against live mail.
