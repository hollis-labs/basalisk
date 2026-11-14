/**
 * What waits on the owner, kept while the desktop is up whether or not the assistant's
 * panel is open: the count is on the bar and, in the desktop app, on the Dock icon, so it
 * is seen with the window put away.
 */
import { useSyncExternalStore } from "react"
import { request } from "../api/account"
import { INBOX_CHANGED } from "../api/inbox"
import { shell } from "../api/shell"

export interface Sorted {
  id: string
  account: string
  provider: string
  from: string
  from_name?: string
  kind: string
  subject: string
  start: string
  at: string
  class: string
  ask?: {
    kind: string
    options?: { id: string; label: string; why?: string; recommended?: boolean }[]
  }
  why?: string
}

export interface Attention {
  /** False when this Basalisk does not sort mail (no gateway). */
  on: boolean
  needs: Sorted[]
  held: Sorted[]
  answers: Sorted[]
  withBas: Sorted[]
}

let current: Attention = { on: false, needs: [], held: [], answers: [], withBas: [] }
let badge = ""
const listeners = new Set<() => void>()

export async function refreshAttention() {
  try {
    const r = await request<{
      needs: Sorted[]
      held: Sorted[]
      with_assistant: Sorted[]
      answers?: Sorted[]
    }>("assist/needs")
    current = {
      on: true,
      needs: r.needs ?? [],
      held: r.held ?? [],
      answers: r.answers ?? [],
      withBas: r.with_assistant ?? [],
    }
  } catch {
    current = { ...current, on: current.on }
    return
  }
  for (const fn of listeners) fn()
  // The Dock icon says how many things wait on the owner.
  const next = current.needs.length ? String(current.needs.length) : ""
  if (next !== badge) {
    badge = next
    void shell.badge(next).catch(() => undefined)
  }
}

let started = false
/** Begin keeping it (once, when the desktop is up). */
export function watchAttention() {
  if (started) return
  started = true
  void refreshAttention()
  window.setInterval(() => void refreshAttention(), 30_000)
  window.addEventListener(INBOX_CHANGED, () => void refreshAttention())
}

export const useAttention = () =>
  useSyncExternalStore(
    (fn) => {
      listeners.add(fn)
      return () => listeners.delete(fn)
    },
    () => current,
  )
