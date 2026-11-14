import { request } from "./account"

/** One scratch item (internal/scratch). */
export interface ScratchItem {
  id: string
  kind: string
  text: string
  done: boolean
  archived: boolean
  day: string
  links: { rel: string; value: string }[]
  origin: string
  created_at: string
  updated_at: string
  caster_ref?: string
  synced: boolean
  sync_error?: string
}

export interface Count {
  value: string
  n: number
  /** What to call it when the value is an id (a session's title). */
  label?: string
}

export interface ScratchFacets {
  days: Count[]
  kinds: Count[]
  projects: Count[]
  tasks: Count[]
  agents: Count[]
  tags: Count[]
  sessions: Count[]
  open: number
  total: number
}

/** A view over the items: what narrows it. Empty fields do not. */
export interface ScratchView {
  day?: string
  kind?: string
  rel?: string
  value?: string
  open?: boolean
  q?: string
  archived?: boolean
}

function query(v: ScratchView): string {
  const p = new URLSearchParams()
  if (v.day) p.set("day", v.day)
  if (v.kind) p.set("kind", v.kind)
  if (v.rel && v.value) {
    p.set("rel", v.rel)
    p.set("value", v.value)
  }
  if (v.open) p.set("open", "1")
  if (v.q) p.set("q", v.q)
  if (v.archived) p.set("archived", "1")
  const s = p.toString()
  return s ? `?${s}` : ""
}

export const scratch = {
  list: (v: ScratchView) =>
    request<{ items: ScratchItem[]; today: string; labels?: Record<string, string> }>(
      `scratch${query(v)}`,
    ),
  facets: () => request<{ facets: ScratchFacets; today: string }>("scratch/facets"),
  add: (text: string, context?: unknown) =>
    request<{ items: ScratchItem[] }>("scratch", "POST", { text, context }),
  update: (
    id: string,
    change: {
      text?: string
      done?: boolean
      archived?: boolean
      /** Tie the item to something more: what it became, a task. */
      link?: { rel: string; value: string }
    },
  ) => request<{ item: ScratchItem }>(`scratch/${encodeURIComponent(id)}`, "PUT", change),
  /** Make a Torque task of the item (in the Torque project named like the item's, if any). */
  toTask: (id: string) =>
    request<{ task_id: string; item: ScratchItem; in_project: boolean }>(
      `scratch/${encodeURIComponent(id)}/task`,
      "POST",
    ),
  /** A view as one markdown page. */
  page: async (v: ScratchView, title: string): Promise<string> => {
    const q = query(v)
    const r = await fetch(
      `/api/scratch/page${q}${q ? "&" : "?"}title=${encodeURIComponent(title)}`,
      { credentials: "same-origin" },
    )
    if (!r.ok) throw new Error(`The page could not be made (${r.status}).`)
    return r.text()
  },
}

/** Told when scratch changed somewhere (the capture box, the agent), so an open window re-reads. */
export const SCRATCH_CHANGED = "basalisk:scratch-changed"
