/**
 * The desktop's own settings: theme, layout and shortcuts. They are kept by the server
 * (GET/PUT /api/desk/settings, one JSON document per person), so the desktop app and a
 * browser agree, with a copy in local storage so the first paint does not wait.
 */
import { useSyncExternalStore } from "react"
import { request } from "../api/account"

export interface DeskSettings {
  version: 1
  theme: string
  layout: "tile" | "float"
  /** Command id to chord. "" means the command has no shortcut. Absent means the default. */
  keys: Record<string, string>
}

const CACHE = "basalisk.desk.settings"
const defaults: DeskSettings = { version: 1, theme: "aura", layout: "tile", keys: {} }

function cached(): DeskSettings {
  try {
    const raw = window.localStorage.getItem(CACHE)
    if (raw) return { ...defaults, ...(JSON.parse(raw) as Partial<DeskSettings>) }
  } catch {
    // Unreadable cache: start from the defaults.
  }
  return defaults
}

let current = cached()
let saveTimer = 0
const listeners = new Set<() => void>()

function store(next: DeskSettings) {
  current = next
  try {
    window.localStorage.setItem(CACHE, JSON.stringify(next))
  } catch {
    // Not cached.
  }
  for (const fn of listeners) fn()
}

export const getSettings = () => current

export function subscribe(fn: () => void) {
  listeners.add(fn)
  return () => {
    listeners.delete(fn)
  }
}

/** Change settings: applied at once, saved to the server shortly after. */
export function updateSettings(patch: Partial<DeskSettings>) {
  store({ ...current, ...patch })
  window.clearTimeout(saveTimer)
  saveTimer = window.setTimeout(() => {
    void request("desk/settings", "PUT", { settings: current }).catch(() => undefined)
  }, 400)
}

/** Fetch the server's copy. It wins over the cache when there is one. */
export async function loadSettings(): Promise<DeskSettings> {
  try {
    const remote = await request<{ settings: Partial<DeskSettings> | null }>("desk/settings")
    if (remote.settings) store({ ...defaults, ...remote.settings })
  } catch {
    // Offline or an older server: the cache stands.
  }
  return current
}

export const useDeskSettings = () => useSyncExternalStore(subscribe, getSettings)
