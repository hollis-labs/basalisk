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

## Planned, in order

1. A watcher on the owner's inbox that triages each new message (Tether's classes: human,
   action, fyi, read) and either settles it, holds it for a digest or puts a card in chat.
2. A quiet lane: the assistant's background work out of the chat, following Tether's
   reply-audience contract (fold a turn only when its input was a mail wake; never fold
   what was triaged human; never infer from the text).
3. Context slots: one per kind of work (messaging, planning, notes, writing, engineering),
   each with the tools that are top of mind there, the last stretch of that context and
   pointers to the rest (chat history, notes, Tesseract, Torque, Planner, scratch).
   Switching slot swaps tools and recent context, not who the assistant is.
4. Status: what is in flight, read from the owner's own tools (Planner, Torque, scratch,
   notes, seat status) with a short "is this current" check, fanned out to task agents.
5. Host and command-line work: handed to @chief, or a terminal opened on the right host
   with the command ready.
6. Tuning: "that was noise" and "always tell me" on a card become rules; the assistant
   edits its own rules on request; what configuration cannot do goes to Local Dev.
