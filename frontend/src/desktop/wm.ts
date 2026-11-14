/**
 * The window manager's state and geometry. Two layouts, as on a tiling Linux desktop:
 *
 *   tile   windows are arranged for you: the first is the master on the left, the rest are
 *          stacked on the right. A window can be floated out of the arrangement.
 *   float  every window keeps the position and size you give it.
 *
 * This file has no React in it: a reducer and a pure function from state to rectangles.
 */
export type Layout = "tile" | "float"

export interface Win {
  id: string
  app: string
  title: string
  /** Floating geometry, used when the window floats. */
  x: number
  y: number
  w: number
  h: number
  /** Stacking order among floating windows; higher is in front. */
  z: number
  floating: boolean
  maximized: boolean
  minimized: boolean
  /** What the window was opened with; handed to the app. */
  args: Record<string, unknown>
  /** For an app with several windows: what this one shows, so it is not opened twice. */
  key?: string
  /** The app says it holds unsaved work; closing asks first. */
  dirty?: boolean
}

export interface WMState {
  /** In tile order: the first tiled window is the master. */
  wins: Win[]
  focus: string | null
  layout: Layout
  /** Share of the width the master takes. */
  ratio: number
  seq: number
  z: number
}

export interface Rect {
  x: number
  y: number
  w: number
  h: number
}

export interface Placed extends Rect {
  tiled: boolean
  z: number
}

export type Snap = "left" | "right" | "full" | "center"

export type WMAction =
  | {
      type: "open"
      app: string
      title: string
      size: [number, number]
      area: Rect
      multi?: boolean
      key?: string
      args?: Record<string, unknown>
    }
  | { type: "title"; id: string; title: string }
  | { type: "args"; id: string; args: Record<string, unknown> }
  | { type: "dirty"; id: string; dirty: boolean }
  | { type: "close"; id: string }
  | { type: "focus"; id: string }
  | { type: "rect"; id: string; rect: Rect }
  | { type: "float"; id: string }
  | { type: "maximize"; id: string }
  | { type: "minimize"; id: string }
  | { type: "layout"; layout?: Layout }
  | { type: "swap"; a: string; b: string }
  | { type: "promote"; id: string }
  | { type: "ratio"; value: number }
  | { type: "cycle"; delta: number }
  | { type: "snap"; id: string; to: Snap; area: Rect }

export const GAP = 10
export const MIN_W = 320
export const MIN_H = 180
const LAYOUT_KEY = "basalisk.desk.layout"

const SESSION_KEY = "basalisk.desk.session"

/**
 * The windows as they were when the desktop last ran, so a restart, a shutdown or a crash
 * brings back what was open. `known` says which apps still exist. A terminal window's shell
 * may have gone with the old process: it is offered as `was` and a new one is made if so.
 */
function restored(layout: Layout, known: (app: string) => boolean): WMState | null {
  try {
    const raw = window.localStorage.getItem(SESSION_KEY)
    if (!raw) return null
    const saved = JSON.parse(raw) as Partial<WMState>
    if (!Array.isArray(saved.wins)) return null
    const wins = saved.wins
      .filter((w) => w && typeof w.id === "string" && typeof w.app === "string" && known(w.app))
      .map((w) => {
        const args = { ...(w.args ?? {}) }
        if (w.app === "terminal" && typeof args.session === "string") {
          args.was = args.session
          delete args.session
        }
        return { ...w, args, dirty: false, title: String(w.title ?? "").replace(/^● /, "") }
      })
    const num = (v: unknown, d: number) => (typeof v === "number" && Number.isFinite(v) ? v : d)
    const seq = Math.max(num(saved.seq, 0), ...wins.map((w) => Number(w.id.split("-").pop()) || 0))
    return {
      wins,
      focus: wins.some((w) => w.id === saved.focus) ? (saved.focus as string) : null,
      layout,
      ratio: Math.min(0.78, Math.max(0.22, num(saved.ratio, 0.56))),
      seq,
      z: Math.max(num(saved.z, 1), ...wins.map((w) => num(w.z, 1))),
    }
  } catch {
    return null
  }
}

/** Keep the windows for the next start (called on every change; cheap and small). */
export function saveSession(s: WMState) {
  try {
    window.localStorage.setItem(SESSION_KEY, JSON.stringify(s))
  } catch {
    // Not kept: the next start is an empty desktop.
  }
}

export function initialState(known: (app: string) => boolean = () => false): WMState {
  let layout: Layout = "tile"
  try {
    if (window.localStorage.getItem(LAYOUT_KEY) === "float") layout = "float"
  } catch {
    // Default layout.
  }
  return restored(layout, known) ?? { wins: [], focus: null, layout, ratio: 0.56, seq: 0, z: 1 }
}

const patch = (s: WMState, id: string, change: Partial<Win>): WMState => ({
  ...s,
  wins: s.wins.map((w) => (w.id === id ? { ...w, ...change } : w)),
})

/** The window that should have focus once `gone` is no longer available. */
function nextFocus(s: WMState, gone: string): string | null {
  const rest = s.wins.filter((w) => w.id !== gone && !w.minimized)
  if (!rest.length) return null
  return rest.reduce((a, b) => (b.z > a.z ? b : a)).id
}

export function reducer(s: WMState, a: WMAction): WMState {
  switch (a.type) {
    case "open": {
      // One window per app, or per key for an app that has several. Opening what is
      // already open brings it forward, with the new arguments if any were given.
      const existing = a.multi
        ? s.wins.find((w) => w.app === a.app && a.key !== undefined && w.key === a.key)
        : s.wins.find((w) => w.app === a.app)
      if (existing) {
        const next = a.args ? patch(s, existing.id, { args: a.args, title: a.title }) : s
        return reducer(next, { type: "focus", id: existing.id })
      }
      const [w, h] = [
        Math.min(a.size[0], a.area.w - 2 * GAP),
        Math.min(a.size[1], a.area.h - 2 * GAP),
      ]
      const step = (s.wins.length % 6) * 28
      const win: Win = {
        id: `${a.app}-${s.seq + 1}`,
        app: a.app,
        title: a.title,
        x: Math.max(GAP, Math.round((a.area.w - w) / 2) + step - 56),
        y: Math.max(GAP, Math.round((a.area.h - h) / 2.6) + step),
        w,
        h,
        z: s.z + 1,
        floating: false,
        maximized: false,
        minimized: false,
        args: a.args ?? {},
        key: a.key,
      }
      return { ...s, wins: [...s.wins, win], focus: win.id, seq: s.seq + 1, z: s.z + 1 }
    }
    case "title":
      return patch(s, a.id, { title: a.title })
    case "args":
      return patch(s, a.id, { args: a.args })
    case "dirty":
      return patch(s, a.id, { dirty: a.dirty })
    case "close":
      return {
        ...s,
        wins: s.wins.filter((w) => w.id !== a.id),
        focus: s.focus === a.id ? nextFocus(s, a.id) : s.focus,
      }
    case "focus":
      return { ...patch(s, a.id, { z: s.z + 1, minimized: false }), focus: a.id, z: s.z + 1 }
    case "rect":
      return patch(s, a.id, a.rect)
    case "float": {
      const w = s.wins.find((x) => x.id === a.id)
      return w ? patch(s, a.id, { floating: !w.floating, maximized: false }) : s
    }
    case "maximize": {
      const w = s.wins.find((x) => x.id === a.id)
      return w ? patch(s, a.id, { maximized: !w.maximized }) : s
    }
    case "minimize":
      return {
        ...patch(s, a.id, { minimized: true }),
        focus: s.focus === a.id ? nextFocus(s, a.id) : s.focus,
      }
    case "layout": {
      const layout = a.layout ?? (s.layout === "tile" ? "float" : "tile")
      try {
        window.localStorage.setItem(LAYOUT_KEY, layout)
      } catch {
        // Not remembered.
      }
      return { ...s, layout, wins: s.wins.map((w) => ({ ...w, maximized: false })) }
    }
    case "swap": {
      const i = s.wins.findIndex((w) => w.id === a.a)
      const j = s.wins.findIndex((w) => w.id === a.b)
      if (i < 0 || j < 0 || i === j) return s
      const wins = [...s.wins]
      ;[wins[i], wins[j]] = [wins[j], wins[i]]
      return { ...s, wins }
    }
    case "promote": {
      const w = s.wins.find((x) => x.id === a.id)
      return w ? { ...s, wins: [w, ...s.wins.filter((x) => x.id !== a.id)] } : s
    }
    case "ratio":
      return { ...s, ratio: Math.min(0.78, Math.max(0.22, a.value)) }
    case "cycle": {
      const open = s.wins.filter((w) => !w.minimized)
      if (!open.length) return s
      const at = open.findIndex((w) => w.id === s.focus)
      const next = open[(at + a.delta + open.length) % open.length]
      return reducer(s, { type: "focus", id: next.id })
    }
    case "snap": {
      const { area } = a
      const half = Math.round((area.w - 3 * GAP) / 2)
      const full = { x: GAP, y: GAP, w: area.w - 2 * GAP, h: area.h - 2 * GAP }
      const rect: Rect =
        a.to === "left"
          ? { ...full, w: half }
          : a.to === "right"
            ? { ...full, x: 2 * GAP + half, w: half }
            : a.to === "full"
              ? full
              : {
                  w: Math.round(area.w * 0.6),
                  h: Math.round(area.h * 0.7),
                  x: Math.round(area.w * 0.2),
                  y: Math.round(area.h * 0.12),
                }
      return patch(s, a.id, { ...rect, floating: true, maximized: false })
    }
  }
}

/** Whether the window is part of the tiled arrangement. */
export const isTiled = (s: WMState, w: Win) =>
  s.layout === "tile" && !w.floating && !w.maximized && !w.minimized

/** Where every visible window goes, for a canvas of the given size. */
export function place(s: WMState, area: Rect, reserved = 0): Map<string, Placed> {
  const out = new Map<string, Placed>()
  const full: Rect = {
    x: GAP,
    y: GAP,
    w: Math.max(MIN_W, area.w - 2 * GAP),
    h: Math.max(MIN_H, area.h - 2 * GAP),
  }
  const tiled = s.wins.filter((w) => isTiled(s, w))
  const masterW = tiled.length > 1 ? Math.round((area.w - 3 * GAP) * s.ratio) : full.w
  const stackW = area.w - 3 * GAP - masterW
  const stackH = tiled.length > 1 ? (area.h - GAP * tiled.length) / (tiled.length - 1) : 0
  tiled.forEach((w, i) => {
    out.set(
      w.id,
      i === 0
        ? { x: GAP, y: GAP, w: masterW, h: full.h, tiled: true, z: 1 }
        : {
            x: 2 * GAP + masterW,
            y: Math.round(GAP + (i - 1) * (stackH + GAP)),
            w: stackW,
            h: Math.round(stackH),
            tiled: true,
            z: 1,
          },
    )
  })
  for (const w of s.wins) {
    if (w.minimized || out.has(w.id)) continue
    if (w.maximized) {
      out.set(w.id, { ...full, tiled: false, z: 100 + w.z })
      continue
    }
    const width = Math.min(Math.max(w.w, MIN_W), full.w)
    const height = Math.min(Math.max(w.h, MIN_H), full.h)
    // `reserved` is room a side panel has taken from the right of the screen. A window that
    // sat wholly on the screen before the panel opened is kept clear of the panel, where it
    // was otherwise (its stored place is untouched, so it goes back when the panel closes).
    // One already hanging off the screen's right edge is left to hang.
    const onScreen = reserved > 0 && w.x + width <= area.w + reserved
    const x = onScreen ? Math.max(GAP, Math.min(w.x, area.w - GAP - width)) : w.x
    out.set(w.id, {
      w: width,
      h: height,
      // Keep the title bar reachable.
      x: Math.min(Math.max(x, 60 - width), area.w - 60),
      y: Math.min(Math.max(w.y, 0), area.h - 32),
      tiled: false,
      z: 100 + w.z,
    })
  }
  return out
}

/** The x position of the divider between master and stack, or null when there is none. */
export function dividerX(s: WMState, area: Rect): number | null {
  const tiled = s.wins.filter((w) => isTiled(s, w))
  if (tiled.length < 2) return null
  return GAP + Math.round((area.w - 3 * GAP) * s.ratio)
}
