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
