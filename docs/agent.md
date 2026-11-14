# The agent

One assistant, in a panel beside the windows (⌘J). You talk to it while you work; it knows
which window has focus and what you have selected, and it can use the desktop through
tools. There is one agent for the whole desktop, not one per app: apps give it tools and
describe their windows, and it does the rest.

This page describes the runtime, the stream, the panel and the desktop's own tools, the
apps' tools, Torque and Tesseract through the Tether gateway, the router in front of the
model, and reflexes. Triggers that start a turn on their own and per-task agents come
after and plug into what is here.

## Shape

```
panel (frontend/src/agent)            server (internal/agent, internal/api/agent.go)
  message + window context + tools ──▶ POST /api/agent/turns ──▶ run loop ──▶ model gateway
  follows the run                  ◀── GET  /api/agent/runs/{id}/events (chatstream/v1, SSE)
  runs a tool the model called     ──▶ POST /api/agent/runs/{id}/tool-results
  answers a permission question    ──▶ POST /api/agent/runs/{id}/approvals
```

- **The runtime is in the server**, on the portfolio's shared libraries: the run loop and
  turn reader from `libs/agent/core`, the model contracts from `libs/llm/core`, the
  `chatstream/v1` event stream, its hub and the SSE writer from `libs/ui/ui-go`. A run
  keeps going when the page that started it goes away, and a page that reloads picks it up
  again (`Last-Event-ID`).
- **The model client is Basalisk's own** (`internal/agent/gateway.go`): the shared libs
  hold contracts and no HTTP client. It speaks the Anthropic Messages API, streamed, to
  `<agent_gateway_url>/v1/messages` with a bearer key, and sends the thread id as
  `X-Station-Session`. It marks the tools, the system prompt and the last message as
  cache points, so each step reads the part of the thread it has already sent from the
  provider's cache instead of paying for it again.
- **Conversations live in their own SQLite file**, `agent.db` beside the app's database.
  Its schema moves forward by numbered steps (`PRAGMA user_version`), so it can change
  without touching the app's single migration.
- **One running thread** per person. "New conversation" puts the thread away (nothing is
  deleted) and starts another. The store already has the columns for task threads, which
  is how parallel per-task agents will hang off the main one.

## What the agent is told

Each message carries a snapshot of the desktop taken by the page: the focused window as
its app describes it (`desk.context()`, `registerContext`), the selection in it, and the
list of open windows. The server renders that as a `<desktop>` block in front of your
words, in the same message, so the thread remembers where each thing was said and the
system prompt stays the same from turn to turn. The panel shows what will be sent under
the box, and on each sent message where it was said.

The selection is noted when you summon the panel (and on a click into it), before the
keyboard moves, because moving the keyboard can clear a selection on a page.

Window *contents* are not sent. The agent gets them by calling a tool, and you see the call.

## Tools

A tool is declared and run by the page (`frontend/src/agent/tools.ts`,
`registerAgentTools`), next to the app it belongs to. The page sends the tool list with
each message; when the model calls one, the server relays the call in the event stream
and waits for the page's answer. Only the page that sent the message runs its tools, and
each call once.

| Level | Meaning | Asks first |
|---|---|---|
| `read` | looks, changes nothing | no |
| `act` | changes what is on screen, undone as easily (open or focus a window) | no |
| `write` | changes data, or reaches outside Basalisk | yes; the answer can stand |
| `confirm` | each call is its own decision (running a command) | yes, every time |

A tool with no level, or an unknown one, is treated as `write`.

While the page runs a call it tells the server every ten seconds that it is still at it,
so a call that waits (on a question to the owner, on a slow page) is not given up on; a
page that goes quiet for 45 seconds is. When the run stops waiting for a call (the owner
pressed Stop, the run gave up), the call's `ctx.signal` is aborted and its result is not
sent. A tool that changes something after a wait checks `ctx.signal.aborted` first.

The desktop's own tools: `desktop_windows`, `window_context`, `desktop_apps` (read),
`desktop_open`, `desktop_focus` (act), `desktop_run_command` (write: any palette command).
The apps add theirs from their own folders (`apps/*/agent.ts`, `terminal/agent.ts`).

### Tools the server runs

Some tools need no page: the server runs them itself (`agent.ServerTool`, `Runtime.AddTools`).
Levels and permission work the same way, and a page cannot register a tool under one of
their names. `GET /api/agent/tools` lists them.

The first source is the Tether gateway (`internal/agent/tether.go`), the same connection
that resolves cited records. The gateway offers hundreds of tools; the agent gets only the
ones named in that file, each with the level the file gives it:

- Tesseract, read: `tesseract_recall`, `tesseract_get`.
- Torque, read: projects, epics, sprints, tasks, comments and subtodos (list and get).
- Torque, asks first: `torque_task_create`, `torque_task_update`, `torque_task_transition`,
  `torque_comment_add`, `torque_task_subtodo_add`, `torque_task_subtodo_done`.

A tool that is not in that list cannot be called, whatever the gateway offers. The list is
read from the gateway in the background when Basalisk starts (starting the gateway can
take a few seconds), so only a message sent in those first seconds goes out without them.
Without `tether_mcp` configured there are none.

## The router

A message is put to a set of rules before any model sees it (`internal/agent/router.go`,
on the shared matcher `libs/core/util/router`). A rule that matches names a tool, and that
tool is called directly: no model request, no tokens. Everything else goes to the model.

A routed call is an ordinary tool call. It is shown in the panel, it asks permission as
its level requires, and it is kept in the thread, so the model knows later what was done.
If the call fails, the run is handed to the model with the failed call in front of it. If
you decline it, the reply is "Not done." and no model is called. The panel marks a message
answered this way: "Done directly, without the model." The rule that took a message is
recorded on its run (`runs.route` in `agent.db`).

The rules that ship (`internal/agent/rules.json`; `/help` in the panel lists them):

| You type | What runs |
|---|---|
| `/open <app>`, or "open notes", "launch a terminal", "show the browser" | `desktop_open` |
| `/do <command title or id>`, or a palette command's title on its own ("Tile windows") | `desktop_run_command` (asks, like any use of it) |
| `/run <command line>` | `terminal_run` (asks every time) |
| `/help` | the list |
| `/new` | a new conversation (handled by the panel itself) |

A rule tests plain fields of the message, all of them strings:

| Field | Is |
|---|---|
| `slash`, `arg` | `/open notes` gives `slash` "open" and `arg` "notes" (as typed) |
| `said` | "yes" when the message is not a slash command |
| `text` | the message in lower case, on one line, without closing punctuation (messages up to 160 characters) |
| `verb`, `object`, `words` | its first word, the rest without a leading article, and the word count |
| `app`, `app_title` | the app that `object` (or `arg`) names, by id or title |
| `command`, `command_title` | the palette command the message (or `arg`) names, by title or id |
| `focused.app`, `selection` | the app of the window in front; "yes" when text is selected in it |

The page sends the apps and the available commands with each message, which is how a name
is matched without the server knowing the desktop. A name that fits two things matches
neither.

**Your own rules** go in `~/.config/basalisk/agent-rules.json` (or the file
`BASALISK_AGENT_RULES_FILE` names), in the same format as `rules.json`. They are tried
before the ones that ship, and the file is read again when it changes; no restart. A file
that does not parse is left out, the shipped rules go on working, and `GET /api/agent`
says why in `rules_problem`. A rule's `target` is `tool:<name>`, `say`, `help` or
`model`; its `params` are the tool's input, with `{field}` filled in from the table above.
Three params are the router's own: `_say` (the reply when the call worked; without it the
tool's result is the reply), `_then: "model"` (hand the result to the model instead of
replying), and `_help` (the rule's line in `/help`). For example, a rule that opens a page:

```json
{
  "version": "mine-1",
  "strategy": "first_match",
  "rules": [
    {
      "id": "torque-board",
      "when": [{ "field": "slash", "op": "eq", "value": "board" }],
      "target": "tool:desktop_open",
      "params": { "app": "browser", "url": "https://example.com/board", "_say": "Opened the board.", "_help": "/board: open the task board." },
      "auto": true
    }
  ]
}
```

## Reflexes

A reflex is a standing rule that looks at the conversation each time a message arrives,
before the model is called (`internal/agent/reflex.go`, on the shared engine
`libs/agent/core/reflexes`). It has a trigger and an action. Two actions are carried out:

| `action_kind` | `action` | What happens |
|---|---|---|
| `inject_reminder` | `{"body": "..."}` | The model is told the body, in a `<reminder>` block after your message. Your message is stored and shown as you typed it. |
| `halt_session` | `{"message": "..."}` | The message is answered with this text and no model is called. |

Reflexes are not evaluated for a message the router took. The panel shows a quiet line
for each one that fired ("Reflex: long-thread"), and a message keeps the names of the
reflexes that fired on it (`meta.reflexes`). When each last fired is kept in `agent.db`,
which is what its cooldown is measured from.

One ships (`internal/agent/reflexes.json`): **long-thread**. When the last request to the
model was over about 120,000 tokens, the model is asked to finish what was asked and then
mention `/new` once. It keeps quiet for an hour after firing. There is no compaction yet,
so this is the guard against a thread that outgrows the model.

**Your own reflexes** go in `~/.config/basalisk/agent-reflexes.json` (or the file
`BASALISK_AGENT_REFLEXES_FILE` names), read again when it changes. One with the name of a
shipped reflex replaces it, so `{"name": "long-thread", "disabled": true, ...}` turns that
one off. A file with a mistake in it (a pattern that does not compile, an unknown trigger
node, an action without its text) is left out whole, and `GET /api/agent` says why in
`reflexes_problem`.

```json
{
  "reflexes": [
    {
      "name": "deploys",
      "trigger": { "kind": "user_regex_window", "window": 1, "pattern": "(?i)\\bdeploy\\b" },
      "action_kind": "inject_reminder",
      "action": { "body": "Deploys go through Cerberus. Do not run a build command by hand." },
      "cooldown_seconds": 600
    }
  ]
}
```

A trigger is a predicate tree, as the shared engine defines it. The nodes that have
something to look at here:

| Node | Fires when |
|---|---|
| `user_regex_window` (`window`, `pattern`) | one of your last `window` messages matches; `window: 1` is the message just sent |
| `regex_match_window`, `text_regex_window` (`scope`: user, assistant, all) | the same over the agent's replies, or both |
| `entity_mention_window` (`entity`: task_id, path, url) | a recent message names one |
| `tool_name_window`, `tool_calls_window` | recent replies did (or did not) call the named tools; every one of the last `window` messages made that many calls |
| `prefix_pressure` (`ratio`, `context_window`) | the last request was at least `ratio` × `context_window` tokens |
| `attr` (`key`, `value`, `op` = or !=) | `focused.app` is the app in front; `selection` is "yes" or "no" |
| `AND`, `OR` (`clauses`) | all, or any, of the clauses |

Message windows are over the last 60 messages, newest first, and count your words as you
typed them (not the `<desktop>` block). Token counts per message are not kept, so the
`input_tokens_window`, `cache_read_window` and `output_growth_window` nodes see zeros.
The engine's other action kinds (dispatch to an agent, schedule, send a message) and its
event and interval triggers are not wired: nothing here starts a turn by itself yet.

`GET /api/agent/automation` lists both: the rules in the order they are tried, the
reflexes with how often and when each last fired, the two files your own go in, and why
either file is not in use.

## Permission

When a `write` tool is called, the run stops and the panel asks: **Once**, **This session**
(until Basalisk restarts), **Always** (kept in `agent.db`, listed at `GET /api/agent/grants`,
removed with `DELETE /api/agent/grants/{tool}`), or **No**. A `confirm` tool offers only
**Allow** and **No**, every time. A "No" is told to the model as a
refusal it should not retry. A question nobody answers in ten minutes counts as no. The
grant is per tool name.

This is a guard for the owner, on the owner's own page; it is not a boundary against the
page, which declares the tools and their levels.

## Configuration

In the `basalisk` block of the config (or the environment, for a development run):

| Key | Environment | |
|---|---|---|
| `agent_gateway_url` | `BASALISK_AGENT_GATEWAY` | Anthropic-compatible base URL; requests go to `<url>/v1/messages` |
| `agent_model` | `BASALISK_AGENT_MODEL` | the model, or the gateway's alias for one |
| `agent_key_file` | `BASALISK_AGENT_KEY_FILE` | file with the bearer key; default `~/.config/basalisk/agent-key` |

The key is read from its file at each request, so a rotated key needs no restart; it is
never logged. Without a gateway and a model the panel opens and says what to set.

## Limits of this slice

- The gateway's tool definitions are long: about 58 KB for the 18 tools, measured at about
  22,500 tokens, against about 6,500 for the system prompt and every tool of Basalisk's own.
  They are cached by the provider between steps (a step then costs little), but a
  conversation picked up after the cache has lapsed pays to write them again.
- No Tesseract writes and no mail yet.
- Tool calls run one after another.
- No compaction: a very long thread will eventually exceed the model's window. Start a
  new conversation.
- If the page is reloaded while it is running a tool call, the call is given up on after
  45 seconds and the model is told the desktop did not answer.
- The panel has a fixed width, and a floating window can sit partly under it.
