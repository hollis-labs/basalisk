# The agent

One assistant, in a panel beside the windows (⌘J). You talk to it while you work; it knows
which window has focus and what you have selected, and it can use the desktop through
tools. There is one agent for the whole desktop, not one per app: apps give it tools and
describe their windows, and it does the rest.

This page describes the first slice: the runtime, the stream, the panel and the desktop's
own tools, the apps' tools, and Torque and Tesseract through the Tether gateway. The
router, reflexes, cards and per-task agents come after it and plug into what is here.

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
read from the gateway in the background after the first message (starting the gateway can
take a few seconds), so the first message after a start may go out without these tools.
Without `tether_mcp` configured there are none.

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

- The gateway's tool descriptions are long (about 58 KB for the 18 tools). They are cached
  by the provider between steps, but they are part of every request.
- No Tesseract writes and no mail yet.
- Tool calls run one after another.
- No compaction: a very long thread will eventually exceed the model's window. Start a
  new conversation.
- If the page is reloaded while it is running a tool call, the call is given up on after
  45 seconds and the model is told the desktop did not answer.
- The panel has a fixed width, and a floating window can sit partly under it.
