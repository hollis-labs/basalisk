/**
 * The agent's HTTP surface (internal/api/agent.go): the thread, a turn, and a run's events
 * as chatstream/v1 server-sent events.
 */
import { request } from "../api/account"

export interface AgentState {
  /** The server has the agent at all. */
  available: boolean
  /** A model is configured, so a message can be answered. */
  ready: boolean
  model: string
  /** Why the owner's router rules file is not in use, when it is not. */
  rules_problem?: string
  /** The same for the owner's reflexes file. */
  reflexes_problem?: string
}

/** What the desktop has by name, for the server's router: "open notes", "tile windows". */
export interface RouteHints {
  apps: { id: string; title: string }[]
  commands: { id: string; title: string }[]
}

/** A content block as the model sees it (llmtypes.ContentBlock). */
export interface Block {
  type: "text" | "tool_use" | "tool_result" | "thinking"
  text?: string
  id?: string
  name?: string
  input?: Record<string, unknown>
  tool_use_id?: string
  content?: string
  is_error?: boolean
}

export interface StoredMessage {
  id: string
  seq: number
  role: "user" | "assistant"
  blocks: Block[]
  /** On a message the owner wrote: their words, and what they were looking at. */
  meta: { text?: string; context?: DeskSnapshot | null }
  run_id?: string
  created_at: string
}

export interface ThreadState {
  thread_id: string
  messages: StoredMessage[]
  active_run: string
  /** Present when the thread is a task agent's. */
  task?: TaskInfo
}

/** A task agent: one piece of work, in a thread of its own (internal/agent/tasks.go). */
export interface TaskInfo {
  id: string
  title: string
  state: "running" | "done" | "failed" | "stopped" | ""
  result: string
  /** Who started it: agent, owner, or trigger:<name>. */
  origin: string
  created_at: string
  updated_at: string
  reported: boolean
  /** Its run, while one is going. */
  active_run?: string
}

/** What the page tells the agent about the desktop with each message. */
export interface DeskSnapshot {
  focused: SnapshotWindow | null
  windows: SnapshotWindow[]
}
export interface SnapshotWindow {
  winId: string
  app: string
  title: string
  file?: { root: string; path: string }
  url?: string
  cwd?: string
  selection?: string
  extra?: Record<string, unknown>
}

/** A tool the page offers the model and runs itself (tools.ts). */
export interface ToolSpec {
  name: string
  description: string
  input_schema: Record<string, unknown>
  /**
   * read: looks. act: changes what is on screen. write: changes data; asks first, and the
   * answer can stand (this session, always). confirm: asks every single time.
   */
  level: "read" | "act" | "write" | "confirm"
}

/** One chatstream/v1 event; only the fields the panel reads. */
export interface StreamEvent {
  v: string
  seq: number
  run_id: string
  verb: string
  reason?: string
  code?: string
  message?: string
  message_id?: string
  role?: string
  part_id?: string
  kind?: string
  meta?: Record<string, unknown>
  text?: string
  final?: unknown
  approval_id?: string
  call_id?: string
  descriptor?: { tool: string; input: unknown; description?: string; standing?: boolean }
  usage?: { uncached_input?: number; output?: number; total?: number }
  value?: Record<string, unknown>
  ext?: Record<string, unknown>
}

export const agentApi = {
  state: () => request<AgentState>("agent"),
  thread: (task?: string) =>
    request<ThreadState>(task ? `agent/thread?id=${encodeURIComponent(task)}` : "agent/thread"),
  tasks: () => request<{ tasks: TaskInfo[] }>("agent/tasks").then((r) => r.tasks),
  closeTask: (id: string) => request<void>(`agent/tasks/${encodeURIComponent(id)}`, "DELETE"),
  newThread: () => request<void>("agent/thread", "DELETE"),
  turn: (body: {
    text: string
    client_id: string
    context: DeskSnapshot
    tools: ToolSpec[]
    hints: RouteHints
    /** A task's thread, for a follow-up to its agent. */
    thread?: string
  }) => request<{ run_id: string }>("agent/turns", "POST", body),
  cancel: (run: string) => request<void>(`agent/runs/${run}/cancel`, "POST", {}),
  toolResult: (run: string, call_id: string, content: string, is_error: boolean) =>
    request<void>(`agent/runs/${run}/tool-results`, "POST", { call_id, content, is_error }),
  /** "Still running this call." Rejects (409) once the run has stopped waiting for it. */
  toolAlive: (run: string, call_id: string) =>
    request<void>(`agent/runs/${run}/tool-alive`, "POST", { call_id }),
  approve: (run: string, approval_id: string, decision: Decision) =>
    request<void>(`agent/runs/${run}/approvals`, "POST", { approval_id, decision }),
}

export type Decision = "once" | "session" | "always" | "deny"

const TERMINAL = new Set(["run.finish", "run.error", "run.abort"])

/**
 * Follow a run to its end, calling onEvent for each event in order. A dropped connection
 * is resumed from the last event seen (Last-Event-ID), so nothing is shown twice or missed.
 * Resolves when the run's terminal event has arrived; rejects when it cannot be followed.
 */
export async function followRun(
  run: string,
  onEvent: (ev: StreamEvent) => void,
  signal: AbortSignal,
): Promise<void> {
  let last = 0
  let failures = 0
  for (;;) {
    if (signal.aborted) return
    try {
      const res = await fetch(`/api/agent/runs/${run}/events`, {
        credentials: "same-origin",
        headers: last ? { "Last-Event-ID": String(last) } : {},
        signal,
      })
      if (res.status === 410 || res.status === 404) return // long over: the thread has it
      if (!res.ok || !res.body) throw new Error(`events: ${res.status}`)
      const reader = res.body.pipeThrough(new TextDecoderStream()).getReader()
      let buffer = ""
      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        buffer += value
        let cut = buffer.indexOf("\n\n")
        while (cut >= 0) {
          const frame = buffer.slice(0, cut)
          buffer = buffer.slice(cut + 2)
          cut = buffer.indexOf("\n\n")
          const data = frame
            .split("\n")
            .filter((l) => l.startsWith("data:"))
            .map((l) => l.slice(l[5] === " " ? 6 : 5))
            .join("\n")
          if (!data) continue // a keepalive comment
          const ev = JSON.parse(data) as StreamEvent
          if (ev.seq <= last) continue
          last = ev.seq
          failures = 0
          onEvent(ev)
          if (TERMINAL.has(ev.verb)) return
        }
      }
      // The stream closed without a terminal event: reconnect and resume.
    } catch (err) {
      if (signal.aborted) return
      if (++failures > 5) throw err
    }
    await new Promise((r) => setTimeout(r, Math.min(4000, 300 * 2 ** failures)))
  }
}
