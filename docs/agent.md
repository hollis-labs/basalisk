# The agent

One assistant, in a panel beside the windows (⌘J). You talk to it while you work; it knows
which window has focus and what you have selected, and it can use the desktop through
tools. There is one agent for the whole desktop, not one per app: apps give it tools and
describe their windows, and it does the rest.

This page describes the first slice: the runtime, the stream, the panel and the desktop's
own tools. App tools (Browser, Notes, Terminal), the Tether gateway's tools, the router,
reflexes and cards come after it and plug into what is here.

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
  `X-Station-Session`.
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
| `write` | changes data, or reaches outside Basalisk | yes |

A tool with no level, or an unknown one, is treated as `write`.

The desktop's own tools: `desktop_windows`, `window_context`, `desktop_apps` (read),
`desktop_open`, `desktop_focus` (act), `desktop_run_command` (write: any palette command).

## Permission

When a `write` tool is called, the run stops and the panel asks: **Once**, **This session**
(until Basalisk restarts), **Always** (kept in `agent.db`, listed at `GET /api/agent/grants`,
removed with `DELETE /api/agent/grants/{tool}`), or **No**. A "No" is told to the model as a
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

- No app tools yet: the agent can see which page or file a window shows and what is
  selected, but cannot read a whole note or page, or act inside an app.
- Tool calls run one after another.
- No prompt caching and no compaction: a very long thread will eventually exceed the
  model's window. Start a new conversation.
- If the page is reloaded while a tool call is waiting on it, that call times out (90 s)
  and the model is told the desktop did not answer.
- The panel has a fixed width, and a floating window can sit partly under it.
