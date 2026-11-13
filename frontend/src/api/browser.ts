import { request } from "./account"
/**
 * The native browser views (desktop app only). The page drives them over a WebKit message
 * channel the native layer installs; it hears back through the "basalisk:browser" DOM
 * event. In a plain browser neither exists and `available()` is false.
 */

export interface BrowserState {
  id: string
  url: string
  title: string
  loading: boolean
  progress: number
  back: boolean
  forward: boolean
  visible: boolean
}

export type BrowserEvent =
  | ({ kind: "state" } & BrowserState)
  | { kind: "error"; id: string; message: string; url: string }
  | { kind: "crashed"; id: string }
  | { kind: "open"; id: string; url: string }
  | { kind: "close"; id: string }
  | { kind: "focus"; id: string }
  | { kind: "chord"; id: string; chord: string }
  | {
      kind: "download"
      id: string
      file: string
      state: "started" | "done" | "failed"
      message?: string
    }

export const BROWSER_EVENT = "basalisk:browser"

type Channel = { postMessage: (msg: unknown) => Promise<unknown> }

function channel(): Channel | undefined {
  const w = window as unknown as { webkit?: { messageHandlers?: Record<string, Channel> } }
  return w.webkit?.messageHandlers?.basaliskBrowser
}

export interface Frame {
  x: number
  y: number
  w: number
  h: number
  visible: boolean
}

export const nativeBrowser = {
  available: () => !!channel(),
  send<T = unknown>(msg: Record<string, unknown>): Promise<T> {
    const c = channel()
    if (!c) return Promise.reject(new Error("The browser runs in the desktop app."))
    return c.postMessage(msg) as Promise<T>
  },
}

/**
 * Opens a web link: in a Browser window in the desktop app, so links stay in the desktop;
 * in a new tab anywhere else.
 */
export function openLink(url: string) {
  const host = window as unknown as {
    basalisk?: { desk?: { open: (app: string, opts?: { args?: Record<string, unknown> }) => void } }
  }
  const desk = host.basalisk?.desk
  if (desk && nativeBrowser.available() && /^https?:/i.test(url)) {
    desk.open("browser", { args: { url } })
    return
  }
  window.open(url, "_blank", "noopener")
}

// ---- The browser's data on the server: visited pages and zoom per site. ----

export interface Visit {
  url: string
  title: string
  last: string
  count: number
}

let zoomCache: Promise<Record<string, number>> | null = null

export const browserData = {
  /** Zoom per host, fetched once per page. */
  zooms(): Promise<Record<string, number>> {
    zoomCache ??= request<{ zoom: Record<string, number> }>("browser/prefs")
      .then((p) => p.zoom ?? {})
      .catch(() => ({}))
    return zoomCache
  },
  async setZoom(host: string, zoom: number) {
    const z = await browserData.zooms()
    if (zoom === 1) delete z[host]
    else z[host] = zoom
    await request("browser/prefs/zoom", "PUT", { host, zoom }).catch(() => undefined)
  },
  history: (q: string, limit = 8) =>
    request<{ entries: Visit[] }>(`browser/history?q=${encodeURIComponent(q)}&limit=${limit}`).then(
      (r) => r.entries ?? [],
    ),
  visit: (url: string, title: string) =>
    request("browser/history", "POST", { url, title }).catch(() => undefined),
}

/** The host a zoom is remembered under ("" for pages without one). */
export function hostOf(url: string): string {
  try {
    const u = new URL(url)
    return u.protocol === "http:" || u.protocol === "https:" ? u.hostname.toLowerCase() : ""
  } catch {
    return ""
  }
}

const SEARCH = "https://duckduckgo.com/?q="

/** What the address field means: a URL as typed, a host, or a search. */
export function toURL(input: string): string {
  const s = input.trim()
  if (!s) return ""
  if (/^[a-z][a-z0-9+.-]*:/i.test(s) && !/^[^/]+:\d+(\/|$)/.test(s)) return s
  const local = /^(localhost|127\.\d+\.\d+\.\d+|\[::1\])(:\d+)?(\/|$)/i.test(s)
  if (local) return `http://${s}`
  if (!/\s/.test(s) && (/^[^/]+\.[a-z]{2,}(:\d+)?(\/|$)/i.test(s) || /^[^/]+:\d+(\/|$)/.test(s)))
    return `https://${s}`
  return SEARCH + encodeURIComponent(s)
}
