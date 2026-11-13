/**
 * What a capture remembers about its moment: the work session and the window that had
 * focus. Read when a draft begins and again at save. It stays in Basalisk; the server keeps
 * it with the queued capture and never sends it to the Fragments Engine.
 */
import { request } from "../../api/account"
import type { DeskAPI } from "../../desktop/registry"

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

/** The focused window, as the desktop describes it (registry.ts, DeskAPI.context). */
async function focusedWindow(desk: DeskAPI): Promise<Moment["window"]> {
  const ctx = await desk.context()
  if (!ctx) return undefined
  return { app: ctx.app, title: ctx.title, file: ctx.file, url: ctx.url, cwd: ctx.cwd }
}

export async function moment(desk: DeskAPI): Promise<Moment> {
  const [s, w] = await Promise.all([session(), focusedWindow(desk)])
  return { session: s, window: w }
}
