# The agent

One assistant, in a panel beside the windows (⌘J). You talk to it while you work; it knows
which window has focus and what you have selected, and it can use the desktop through
tools. There is one agent for the whole desktop, not one per app: apps give it tools and
describe their windows, and it does the rest.

This page describes the runtime, the stream, the panel and the desktop's own tools, the
apps' tools, Torque and Tesseract through the Tether gateway, the router in front of the
model, reflexes, task agents, and triggers.

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
- Tesseract, asks first: `memory_write`, `knowledge_write`, `workspace_write`, with
  `tesseract_skills` (read) for the shape of a write. The agent writes as itself
  (`author_agent_id` is always `basalisk`) and never in your own scope: a write to a
  `user/…` or `system` namespace is refused before it reaches the gateway.
- Caster (named, revisioned text): `caster_head`, `caster_get`, `caster_ls`, `caster_log`
  and `caster_search` (read), and `caster_put` to store text or a new revision (asks first).
- Mail, when the agent has an address of its own (`agent_urn`): `tether_registry_search`,
  `tether_message_list` and `tether_message_get` (read), `tether_message_mark_read`, and
  `tether_message_notify` to send (asks first). Whatever the model passes, mail is sent
  from the agent's own address and read from its own box: it cannot write as you or as
  anyone else, and cannot read another's mail. It reads mail only when asked; nothing
  wakes it.

A tool that is not in that list cannot be called, whatever the gateway offers. The list is
read from the gateway in the background when Basalisk starts (starting the gateway can
take a few seconds), so only a message sent in those first seconds goes out without them.
Without `tether_mcp` configured there are none.

**Described on demand.** The gateway's definitions are long (written as guidance for
coding agents: about 57 KB for these 18, measured at about 22,500 tokens of every
request). So the model is not sent them each time. It gets each tool by name with the
first line of its description, and one more tool, `tether_describe`, which returns the
full description and arguments of the tools it names, from the list Basalisk already
holds (no gateway call). The model asks once per tool, the first time it needs it in a
conversation; after that the definition is in the thread. A definition under 600 bytes
(the three single-record `get` tools) is sent whole. What goes with every request is now
about 4.7 KB. A call made with guessed arguments fails at the gateway, and the failure
says to describe the tool first. The allowlist and the levels are unchanged, and
`tether_describe` describes only tools on the allowlist.

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
| `/open <app>`, an app's name as the command (`/terminal`, `/notes`), or "open notes", "launch a terminal", "show the browser" | `desktop_open` |
| `/do <command title or id>`, or a palette command's title on its own ("Tile windows") | `desktop_run_command` (asks, like any use of it) |
| `/run <command line>` | `terminal_run` (asks every time) |
| `/task <what to do>` | `task_start`: a task agent takes it (see Task agents) |
| `/s <text>` | `scratch_add`: kept in your scratch, word for word (docs/scratch.md) |
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
| `slash_app` | "yes" when the slash command itself is an app's name (`/terminal`) |
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
model was over about 150,000 tokens, which means compaction (below) did not bring the
thread down, the model is asked to finish what was asked and then mention `/new` once. It
keeps quiet for an hour after firing.

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
The engine's schedule and send-a-message actions are not wired.

## Compaction

A thread that grows long is shortened for the model, not for you
(`internal/agent/compact.go`). When a thread's last request was over about 110,000
tokens, then before the next one the older part of the thread is put to the model once,
as plain text, to be summarised: what you asked and decided, what was done and where,
what was found, what is unfinished. From then on the model is sent that summary in place
of those messages, then the recent ones in full (about the last 90,000 characters, cut at
one of your messages so no call is parted from its result).

Nothing is deleted. The thread keeps every message and the panel shows them all; the
summary and the point it reaches are kept on the thread. A later compaction summarises
the earlier summary together with what came since. The panel says when it happens
("Summarising the older part of this conversation…"). If the summary cannot be made, the
thread is sent whole as before and the message is still answered. Task threads are
compacted the same way. The 110,000 figure assumes a model window of 200,000 tokens or
more.

## Task agents

There is one running thread, and beside it any number of task agents: each is handed one
piece of work, does it by itself in a thread of its own, and reports
(`internal/agent/tasks.go`, `frontend/src/agent/tasks.ts`). Up to four run at once, at the
same time as the running thread.

- **Started** by the main agent when the work is many steps you need not watch (its tool
  `task_start`), or by you: `/task <what to do>`.
- **What it knows:** only its brief. It does not see the running thread. It has the same
  tools and asks permission the same way; a question nobody answers in ten minutes counts
  as no. A task cannot start tasks.
- **Where you see it:** a line in the strip at the top of the panel (title and state), a
  card under the call that started it, and a window of its own (click either), which is
  its conversation. You can say more to it there, which sets it going again.
- **When it ends:** its state (done, failed, stopped) and its report, which is its last
  message, are kept on the task. The running thread is told once, in a `<tasks>` block
  with your next message, and the main agent can ask with `task_status`. The ✕ on its line
  puts a finished task away; nothing is deleted.
- **Its tools run in the page** like any run's, whether or not its window is open: the
  page follows every running task from the moment the desktop is up. With Basalisk closed
  a task cannot use the desktop; one that was running when Basalisk stopped is marked
  stopped.

Routes: `GET /api/agent/tasks`, `GET /api/agent/thread?id=<task>`, `thread` on
`POST /api/agent/turns` (a follow-up), `DELETE /api/agent/tasks/{id}`.

## Triggers

A trigger is a reflex that fires without you saying anything and starts a task agent
(`internal/agent/triggers.go`). It is written in the same file as your other reflexes,
with the action `dispatch_to_agent`:

```json
{
  "reflexes": [
    {
      "name": "morning-tasks",
      "about": "Once the desktop is up, say what is on my plate.",
      "trigger_kind": "event",
      "trigger": { "name": "desktop_ready" },
      "action_kind": "dispatch_to_agent",
      "action": { "title": "Today's tasks", "prompt": "List my Torque tasks that are in doing or review, most urgent first, in five lines." },
      "cooldown_seconds": 43200
    },
    {
      "name": "inbox-check",
      "trigger_kind": "interval",
      "trigger": { "every_n_ticks": 30 },
      "action_kind": "dispatch_to_agent",
      "action": { "prompt": "Look at the latest Frag captures and say if any failed to send." }
    }
  ]
}
```

| `trigger_kind` | `trigger` | Fires |
|---|---|---|
| `event` | `{"name": "<event>"}` | when the named thing happens |
| `interval` | `{"every_n_ticks": N}` | every N minutes while Basalisk is running and its desktop page has been seen |
| `predicate` (the default) | a predicate tree, as above | on a message of yours that matches; the message is still answered |

The events: `desktop_ready` (the desktop page came up), `window_opened:<app>` (a window
of that app was opened: `window_opened:browser`), and `task_ended` (a task agent ended).
The page reports its events to `POST /api/agent/events`.

What keeps this in hand:

- None ship. Nothing starts by itself unless you write a trigger.
- A trigger fires at most once a minute, whatever its `cooldown_seconds` says.
- The task it starts is a task like any other: it is in the strip and has its window, it
  asks permission for anything that changes data (its line in the strip says "asks you"),
  and it counts toward the four that may run at once. A standing "Always" for a tool
  covers a trigger's task too.
- A task that a trigger started does not raise `task_ended`, so a trigger on that event
  cannot set itself off.

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
| `agent_urn` | | the agent's own Tether address (`msg://agent/…`), for mail; empty: no mail tools |

The key is read from its file at each request, so a rotated key needs no restart; it is
never logged. Without a gateway and a model the panel opens and says what to set.

## Limits of this slice

- The first use of a Torque or Tesseract tool in a conversation takes one extra step (the
  model reads its definition first).
- The agent does not check its mail by itself. A trigger can ask it to (see Triggers).
- Tool calls run one after another.
- If the page is reloaded while it is running a tool call, the call is given up on after
  45 seconds and the model is told the desktop did not answer.
- The panel has a fixed width, and a floating window can sit partly under it.
