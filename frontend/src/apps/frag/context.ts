/**
 * What a capture remembers about its moment: the work session and the window that had
 * focus. Read when a draft begins and again at save. It stays in Basalisk; the server keeps
 * it with the queued capture and never sends it to the Fragments Engine.
 */
import { request } from "../../api/account"
import type { DeskAPI } from "../../desktop/registry"
import { filesLocations } from "../files/model"

export interface Moment {
  session?: { id: string; title: string; project: string; workstream: string; contextId: string }
  window?: {
    app: string
    title: string
    file?: { root: string; path: string }
    url?: string
    cwd?: string
  }
}

interface ActivityState {
  current: null | {
    id: string
    title: string
    status: string
    context: { id: string; project: string; workstream: string }
  }
}

async function session(): Promise<Moment["session"]> {
  try {
    const st = await request<ActivityState>("activity")
    const s = st.current
    if (!s || s.status === "ended") return undefined
    return {
      id: s.id,
      title: s.title,
      project: s.context.project,
      workstream: s.context.workstream,
      contextId: s.context.id,
    }
  } catch {
    return undefined
  }
}

async function terminalCwd(): Promise<string | undefined> {
  try {
    const st = await request<{ sessions: { id: string; cwd: string; exited: boolean }[] }>(
      "terminal",
    )
    const active = window.localStorage.getItem("basalisk.terminal.active")
    return st.sessions.find((s) => s.id === active && !s.exited)?.cwd
  } catch {
    return undefined
  }
}

/** The focused window, described as far as its app can say. */
async function focusedWindow(desk: DeskAPI): Promise<Moment["window"]> {
  const st = desk.wmState()
  const win = st.wins.find((w) => w.id === st.focus)
  if (!win) return undefined
  const out: NonNullable<Moment["window"]> = { app: win.app, title: win.title }
  const args = (win.args ?? {}) as Record<string, unknown>
  if (win.app === "files") {
    const loc = filesLocations.get(win.id)
    if (loc) out.file = loc
  } else if (args.file && typeof args.file === "object") {
    const f = args.file as { root?: unknown; path?: unknown }
    if (typeof f.root === "string" && typeof f.path === "string")
      out.file = { root: f.root, path: f.path }
  }
  if (win.app === "browser" && typeof args.url === "string") out.url = args.url
  if (win.app === "terminal") out.cwd = await terminalCwd()
  return out
}

export async function moment(desk: DeskAPI): Promise<Moment> {
  const [s, w] = await Promise.all([session(), focusedWindow(desk)])
  return { session: s, window: w }
}
