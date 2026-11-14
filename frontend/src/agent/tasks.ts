/**
 * Task agents, as the page knows them: the list (read from the server now and then, more
 * often while one is running), and following each running task's run, so the tools it asks
 * this page to run are run whether or not its window is open.
 */
import { useSyncExternalStore } from "react"
import { registerResultCards } from "../cite/cards"
import type { DeskAPI } from "../desktop/registry"
import { agentApi, type TaskInfo } from "./api"
import { CLIENT, setOnTaskStarted, taskSession } from "./session"
import { snapshot, toolSpecs } from "./tools"

let tasks: TaskInfo[] = []
const listeners = new Set<() => void>()
const subscribe = (fn: () => void) => {
  listeners.add(fn)
  return () => {
    listeners.delete(fn)
  }
}
export const useTasks = () => useSyncExternalStore(subscribe, () => tasks)

let desk: DeskAPI | null = null
let timer = 0
let reading = false
/** Tasks seen running, so the session is read again once when one ends. */
const wasRunning = new Set<string>()

/** Read the list now, and pick up any run this page is not following yet. */
export async function refreshTasks() {
  if (reading) return
  reading = true
  try {
    const next = await agentApi.tasks()
    if (JSON.stringify(next) !== JSON.stringify(tasks)) {
      tasks = next
      for (const fn of listeners) fn()
    }
    for (const t of next) {
      const session = taskSession(t.id)
      if (t.active_run && desk) {
        wasRunning.add(t.id)
        if (!session.get().loaded) void session.start(desk)
        else session.pickUp(t.active_run)
      } else if (wasRunning.delete(t.id)) {
        void session.reload()
      }
    }
  } catch {
    // The next reading will do.
  } finally {
    reading = false
    window.clearTimeout(timer)
    const busy = tasks.some((t) => t.state === "running")
    timer = window.setTimeout(() => void refreshTasks(), busy ? 2500 : 20_000)
  }
}

/**
 * Tell the server that something happened on the desktop ("window_opened:browser"), with
 * what this page can do, so a reflex that fires on it can start a task agent (triggers in
 * docs/agent.md). Quiet when the agent is not there.
 */
export async function reportEvent(type: string) {
  if (!desk) return
  try {
    const { started } = await agentApi.event({
      type,
      client_id: CLIENT,
      context: await snapshot(desk),
      tools: toolSpecs(),
    })
    if (started.length) void refreshTasks()
  } catch {
    // No agent, or not signed in: nothing listens.
  }
}

/** Start keeping the list, and reporting what happens. Called once the desktop is up. */
export function startTasks(d: DeskAPI) {
  desk = d
  setOnTaskStarted(() => void refreshTasks())
  void refreshTasks()
  void reportEvent("desktop_ready")
  // A window that was not there before is an event, named for its app.
  let known = new Set(d.wmState().wins.map((w) => w.id))
  d.subscribe(() => {
    const wins = d.wmState().wins
    for (const w of wins)
      if (!known.has(w.id) && w.app !== "agent-task") void reportEvent(`window_opened:${w.app}`)
    known = new Set(wins.map((w) => w.id))
  })
}

/** Open a task's window (or bring it forward). */
export function openTask(d: DeskAPI, id: string, title?: string) {
  d.open("agent-task", { args: { task: id }, key: id, title: title || "Task" })
}

/** Put a finished task away. */
export async function closeTask(id: string) {
  await agentApi.closeTask(id).catch(() => undefined)
  void refreshTasks()
}

// The call that starts a task shows the task, with a way into its window.
registerResultCards({
  task_start: (_input, r) => {
    const o = (r ?? {}) as { task_id?: unknown; title?: unknown }
    if (typeof o.task_id !== "string") return null
    const id = o.task_id
    const now = tasks.find((t) => t.id === id)
    const title = typeof o.title === "string" ? o.title : "Task"
    return {
      kind: "agent.task",
      title,
      status: now
        ? { label: now.state || "running", tone: toneOf(now.state) }
        : { label: "running", tone: "info" },
      facts: [],
      open: () => {
        if (desk) openTask(desk, id, title)
      },
    }
  },
})

export function toneOf(state: string): "info" | "success" | "danger" | "neutral" {
  switch (state) {
    case "running":
      return "info"
    case "done":
      return "success"
    case "failed":
      return "danger"
  }
  return "neutral"
}
