/**
 * What waits on the owner, kept while the desktop is up whether or not the assistant's
 * panel is open: the count is on the bar and, in the desktop app, on the Dock icon, so it
 * is seen with the window put away.
 */
import { useSyncExternalStore } from "react"
import { request } from "../api/account"
import { INBOX_CHANGED } from "../api/inbox"
import { shell } from "../api/shell"
import type { DeskAPI } from "../desktop/registry"
import { mainSession } from "./session"

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
  /** The assistant said something unasked that has not been looked at (the panel was shut). */
  news: boolean
}

let current: Attention = {
  on: false,
  needs: [],
  held: [],
  answers: [],
  withBas: [],
  news: false,
}
/** Where the running thread stood when last looked at: its last message, and its run. */
let mark: { last: number; run: string } | null = null
let panelShown: () => boolean = () => true

/** The assistant's panel was opened: what it said unasked has been seen. */
export function seenNews() {
  if (!current.news) return
  current = { ...current, news: false }
  for (const fn of listeners) fn()
}
let badge = ""
const listeners = new Set<() => void>()

export async function refreshAttention() {
  try {
    const r = await request<{
      needs: Sorted[]
      held: Sorted[]
      with_assistant: Sorted[]
      answers?: Sorted[]
      active_run?: string
      thread_last?: number
    }>("assist/needs")
    current = {
      on: true,
      needs: r.needs ?? [],
      held: r.held ?? [],
      answers: r.answers ?? [],
      withBas: r.with_assistant ?? [],
      news: current.news,
    }
    // The running thread moved without this page sending anything: the assistant was
    // started on something that came (an answer). Show it, and say so if the panel is shut.
    const now = { last: r.thread_last ?? 0, run: r.active_run ?? "" }
    const session = mainSession.get()
    if (mark && !session.run && (now.last !== mark.last || now.run !== mark.run)) {
      void mainSession.reload()
      if (!panelShown()) current = { ...current, news: true }
    }
    mark = now
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
export function watchAttention(desk?: DeskAPI) {
  if (started) return
  started = true
  if (desk) panelShown = () => desk.panelOpen("agent")
  void refreshAttention()
  window.setInterval(() => void refreshAttention(), 10_000)
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
