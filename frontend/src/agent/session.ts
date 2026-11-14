/**
 * The conversation as the panel shows it, kept outside React: a run goes on, and the
 * desktop's tools keep answering it, while the panel is closed. The panel subscribes.
 */
import { useSyncExternalStore } from "react"
import { ApiError } from "../api/account"
import type { DeskAPI } from "../desktop/registry"
import {
  type AgentState,
  agentApi,
  type Decision,
  type DeskSnapshot,
  followRun,
  type StoredMessage,
  type StreamEvent,
} from "./api"
import { routeHints, runTool, snapshot, toolSpecs } from "./tools"

export type Item =
  | { kind: "user"; id: string; text: string; context?: DeskSnapshot | null }
  | { kind: "text"; id: string; text: string }
  | {
      kind: "tool"
      id: string
      name: string
      input?: unknown
      result?: string
      isError?: boolean
      running: boolean
    }
  | { kind: "notice"; id: string; text: string; tone: "error" | "quiet" }

export interface Approval {
  run: string
  id: string
  tool: string
  input: unknown
  description?: string
  /** "This session" and "Always" are on offer (not for a tool that asks every time). */
  standing: boolean
}

export interface Session {
  state: AgentState | null
  loaded: boolean
  items: Item[]
  /** The run being answered, when there is one. */
  run: string | null
  /** A question waiting for the owner. */
  approval: Approval | null
  /** What the next message will say the owner is looking at. */
  context: DeskSnapshot | null
  error: string
}

let session: Session = {
  state: null,
  loaded: false,
  items: [],
  run: null,
  approval: null,
  context: null,
  error: "",
}
const listeners = new Set<() => void>()
function set(patch: Partial<Session>) {
  session = { ...session, ...patch }
  for (const fn of listeners) fn()
}
const subscribe = (fn: () => void) => {
  listeners.add(fn)
  return () => {
    listeners.delete(fn)
  }
}
export const useAgentSession = () => useSyncExternalStore(subscribe, () => session)

/** This page, to the server: the one that runs the tools of the runs it starts. */
const CLIENT = crypto.randomUUID()
/** Calls this page has already run, so a replayed stream does not run them again. */
const ran = new Set<string>()
let desk: DeskAPI | null = null
let following: AbortController | null = null
/** Calls this page is running now, so they can be told the run stopped waiting. */
const inFlight = new Map<string, AbortController>()

/**
 * Run a call for the server, saying "still at it" every few seconds so a call that waits
 * (on the owner, on a slow page) is not given up on. When the run stops waiting, the call
 * is aborted and its result is not sent.
 */
async function execute(run: string, call: string, name: string, input: unknown, d: DeskAPI) {
  const control = new AbortController()
  inFlight.set(call, control)
  const beat = window.setInterval(() => {
    agentApi.toolAlive(run, call).catch((err) => {
      if (err instanceof ApiError && (err.status === 409 || err.status === 404)) control.abort()
    })
  }, 10_000)
  try {
    const out = await runTool(name, input, d, control.signal)
    if (!control.signal.aborted)
      await agentApi.toolResult(run, call, out.content, out.isError).catch(() => undefined)
  } finally {
    window.clearInterval(beat)
    inFlight.delete(call)
  }
}

const patchItem = (id: string, change: (item: Item) => Item) =>
  set({ items: session.items.map((it) => (it.id === id ? change(it) : it)) })

function fromStored(messages: StoredMessage[], skipRun: string): Item[] {
  const items: Item[] = []
  const tools = new Map<string, Extract<Item, { kind: "tool" }>>()
  for (const m of messages) {
    if (m.role === "user" && typeof m.meta?.text === "string") {
      items.push({ kind: "user", id: m.id, text: m.meta.text, context: m.meta.context })
      continue
    }
    // The run still going is rebuilt from its event stream, not from what is stored so far.
    if (skipRun && m.run_id === skipRun) continue
    m.blocks.forEach((b, i) => {
      if (b.type === "text" && b.text)
        items.push({ kind: "text", id: `${m.id}:${i}`, text: b.text })
      else if (b.type === "tool_use" && b.id) {
        const item = {
          kind: "tool" as const,
          id: b.id,
          name: b.name ?? "",
          input: b.input,
          running: false,
        }
        tools.set(b.id, item)
        items.push(item)
      } else if (b.type === "tool_result" && b.tool_use_id) {
        const call = tools.get(b.tool_use_id)
        if (call) {
          call.result = b.content ?? ""
          call.isError = !!b.is_error
        }
      }
    })
  }
  return items
}

/** Load the thread, and pick up a run that is still going. Safe to call more than once. */
export async function start(d: DeskAPI) {
  desk = d
  if (session.loaded) return
  try {
    const state = await agentApi.state()
    if (!state.available) {
      set({ state, loaded: true })
      return
    }
    const thread = await agentApi.thread()
    set({
      state,
      loaded: true,
      items: fromStored(thread.messages, thread.active_run),
      error: "",
    })
    if (thread.active_run) void follow(thread.active_run)
  } catch (err) {
    set({
      loaded: true,
      error: err instanceof Error ? err.message : "The agent could not be reached.",
    })
  }
}

/** Note what the owner is looking at, for the next message. Call before focus moves to the panel. */
export async function noteContext() {
  if (!desk) return
  const next = await snapshot(desk)
  set({ context: next })
}

export async function send(text: string): Promise<boolean> {
  if (!desk || session.run || !text.trim()) return false
  // The one command that is about the panel itself; the server's router has the rest.
  if (text.trim().toLowerCase() === "/new") {
    await newThread()
    return true
  }
  // Where they are now; the selection noted when they came to the panel, if the window is
  // the same one (moving the keyboard here can clear a selection on the page).
  const context = await snapshot(desk)
  const held = session.context?.focused
  if (context.focused && held && held.winId === context.focused.winId && !context.focused.selection)
    context.focused.selection = held.selection
  const temp = `pending:${Date.now()}`
  set({
    items: [...session.items, { kind: "user", id: temp, text: text.trim(), context }],
    error: "",
  })
  try {
    const { run_id } = await agentApi.turn({
      text,
      client_id: CLIENT,
      context,
      tools: toolSpecs(),
      hints: routeHints(desk),
    })
    void follow(run_id)
    return true
  } catch (err) {
    set({
      items: session.items.filter((it) => it.id !== temp),
      error: err instanceof ApiError ? err.message : "The message could not be sent.",
    })
    return false
  }
}

export function stop() {
  // First, so a call that is waiting (on a question, say) cannot go on to change anything.
  for (const c of inFlight.values()) c.abort()
  if (session.run) void agentApi.cancel(session.run).catch(() => undefined)
}

export async function answer(decision: Decision) {
  const q = session.approval
  if (!q) return
  set({ approval: null })
  await agentApi.approve(q.run, q.id, decision).catch(() => undefined)
}

/** Put the thread away and start a new one. */
export async function newThread() {
  if (session.run) return
  try {
    await agentApi.newThread()
    set({ items: [], error: "" })
  } catch (err) {
    set({ error: err instanceof Error ? err.message : "A new conversation could not be started." })
  }
}

async function follow(run: string) {
  following?.abort()
  const control = new AbortController()
  following = control
  set({ run, approval: null })
  // Part ids are the run's own; an item's id carries the run so two runs never collide.
  const key = (part: string) => `${run}:${part}`
  const kinds = new Map<string, string>()
  // Whether the router took the message, and whether the model was called after all.
  let routed = false
  let modelUsed = false
  const onEvent = (ev: StreamEvent) => {
    switch (ev.verb) {
      case "usage":
        modelUsed = true
        break
      case "part.start": {
        if (!ev.part_id) break
        kinds.set(ev.part_id, ev.kind ?? "")
        if (ev.kind === "text")
          set({ items: [...session.items, { kind: "text", id: key(ev.part_id), text: "" }] })
        else if (ev.kind === "tool_call")
          set({
            items: [
              ...session.items,
              {
                kind: "tool",
                id: key(ev.part_id),
                name: String(ev.meta?.name ?? ""),
                running: true,
              },
            ],
          })
        else if (ev.kind === "tool_result") {
          const call = key(String(ev.meta?.call_id ?? ""))
          kinds.set(ev.part_id, `result:${call}`)
          patchItem(call, (it) =>
            it.kind === "tool"
              ? { ...it, running: false, result: "", isError: ev.meta?.is_error === true }
              : it,
          )
        }
        break
      }
      case "part.delta": {
        if (!ev.part_id || !ev.text) break
        const kind = kinds.get(ev.part_id) ?? ""
        const add = ev.text
        if (kind === "text")
          patchItem(key(ev.part_id), (it) =>
            it.kind === "text" ? { ...it, text: it.text + add } : it,
          )
        else if (kind.startsWith("result:"))
          patchItem(kind.slice(7), (it) =>
            it.kind === "tool" ? { ...it, result: (it.result ?? "") + add } : it,
          )
        break
      }
      case "part.end": {
        if (!ev.part_id || kinds.get(ev.part_id) !== "tool_call") break
        patchItem(key(ev.part_id), (it) => (it.kind === "tool" ? { ...it, input: ev.final } : it))
        const call = typeof ev.ext?.["basalisk.call"] === "string" ? ev.ext["basalisk.call"] : ""
        // Only the page that sent the message runs its tools, and each call once.
        if (ev.ext?.["basalisk.exec"] !== "client" || ev.ext["basalisk.client"] !== CLIENT) break
        if (!call || ran.has(call) || !desk) break
        ran.add(call)
        const name = session.items.find((it) => it.id === key(ev.part_id ?? ""))
        void execute(run, call, name?.kind === "tool" ? name.name : "", ev.final, desk)
        break
      }
      case "approval.request":
        if (ev.approval_id && ev.descriptor)
          set({
            approval: {
              run,
              id: ev.approval_id,
              tool: ev.descriptor.tool,
              input: ev.descriptor.input,
              description: ev.descriptor.description,
              standing: ev.descriptor.standing !== false,
            },
          })
        break
      case "activity":
        if (ev.kind === "basalisk.approval" && session.approval?.id === ev.value?.approval_id)
          set({ approval: null })
        else if (ev.kind === "basalisk.route") routed = true
        break
      case "run.error":
        set({
          items: [
            ...session.items,
            {
              kind: "notice",
              id: key("error"),
              tone: "error",
              text: ev.message || "The agent could not finish.",
            },
          ],
        })
        break
      case "run.abort":
        set({
          items: [
            ...session.items,
            { kind: "notice", id: key("abort"), tone: "quiet", text: "Stopped." },
          ],
        })
        break
      case "run.finish":
        if (routed && !modelUsed)
          set({
            items: [
              ...session.items,
              {
                kind: "notice",
                id: key("direct"),
                tone: "quiet",
                text: "Done directly, without the model.",
              },
            ],
          })
        if (ev.reason === "turn_limit" || ev.reason === "length")
          set({
            items: [
              ...session.items,
              {
                kind: "notice",
                id: key("limit"),
                tone: "quiet",
                text:
                  ev.reason === "length"
                    ? "The answer was cut off at the length limit."
                    : "Stopped after many steps. Say “continue” to go on.",
              },
            ],
          })
        break
    }
  }
  try {
    await followRun(run, onEvent, control.signal)
  } catch {
    if (!control.signal.aborted)
      set({ error: "The connection to the agent was lost. Reopen the panel to pick it up." })
  }
  // The run is over: nothing it asked this page to do may still change anything.
  for (const c of inFlight.values()) c.abort()
  if (following === control) {
    following = null
    // Anything still marked running did not finish with the run.
    set({
      run: null,
      approval: null,
      items: session.items.map((it) =>
        it.kind === "tool" && it.running ? { ...it, running: false } : it,
      ),
    })
  }
}
