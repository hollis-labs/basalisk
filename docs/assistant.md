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

Not yet: the last stretch of conversation kept per slot (the thread and its summary are
still one), slots the owner can define, and pointers gathered automatically (the thread's
history, what the assistant looked up).

## Knowing how things stand (built)

- **Around.** The state of the owner's mail rides with their next message in an `<around>`
  block when it has changed since the thread was last told: answers that came to what the
  assistant asked, what waits on the owner, how much is held
  (`internal/agent/ambient.go`; any source can be added with `Runtime.AddAmbient`).
- **Answers come back to the asker.** A message the assistant sends in the owner's name
  (`owner_status_ask`, `owner_send`, a delegation) is remembered. A reply to it is sorted as
  `answer`: it goes to the assistant in the `<around>` block, not to the owner as new mail.
- **`status_overview`** (runs at once): the sorted mail, what was done in the owner's name
  lately, the assistant's background runs, and Torque's work in flight (doing, review,
  blocked: totals, by team tag, the twenty most recently touched; `team` narrows to a tag).
  It is the first call for "where are things".

## Handing work on (built)

- **`chief_delegate`** (asks first): makes a Torque task in the create-task shape (Goal,
  Context, Scope in and out, Acceptance, Evidence, Classification), tagged `seat-chief`,
  and sends @chief a request naming it. @chief is found in the registry by its handle.
  It is refused without what not to touch and at least one check.
- **`dev_request`** (asks first): asks whoever builds Basalisk (`basalisk.dev_address`) for
  what configuration cannot do.
- **`mail_rules`**, **`mail_rule_add`**, **`mail_rule_remove`**: the assistant reads the
  owner's sorting rules, and changes them when told to (asks first).

## Planned, in order

1. Sends through Tether's typed messages (`tether_message_post`) once the address aliases
   land, and a small model for sorting what the rules cannot.
3. Context slots, second part: recent conversation kept per slot, with the summary made per
   slot; slots the owner defines; pointers gathered as the assistant works.
4. Status, second part: the Planner and Content workspaces and seat status in the overview;
   a check that a note or a plan is current before it is relied on.
5. Host and command-line work: a list of command kinds the assistant may run outright, and
   a delegation to @chief that follows its create-task shape.
6. Tuning: the standing "Always" answers listed beside the mail rules (the rules are in the
   Done for you window, under Mail rules, with Remove).
