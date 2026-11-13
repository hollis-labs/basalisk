import { request } from "./account"

/** The native desktop shell (GET /api/shell). `available` is false in a browser. */
export interface ShellStatus {
  available: boolean
  active: boolean
  kiosk: boolean
  menu_bar_visible: boolean
  block_switching: boolean
  swallow: boolean
  combos: string[]
  tap_installed: boolean
  tap_level: "" | "hid" | "session"
  fix_minimized: boolean
  permissions: { accessibility: boolean; input_monitoring: boolean }
  screen: { width: number; height: number; safe_top: number; count: number }
  identity: { bundle_id: string; path: string; bundled: boolean }
  chords: { escape: string; toggle_kiosk: string; toggle_menu_bar: string }
  last_seq: number
}

export interface ShellEvent {
  seq: number
  at: string
  kind: string
  detail: string
}

export interface ShellWindow {
  pid: number
  app: string
  bundle_id: string
  index: number
  title: string
  subrole: string
  minimized: boolean
  app_hidden: boolean
  app_active: boolean
}

export const shell = {
  status: () => request<ShellStatus>("shell"),
  events: (after = 0) => request<{ events: ShellEvent[] }>(`shell/events?after=${after}`),
  windows: () => request<{ windows: ShellWindow[] }>("shell/windows"),
  kiosk: (on: boolean, blockSwitching: boolean) =>
    request<ShellStatus>("shell/kiosk", "POST", { on, block_switching: blockSwitching }),
  menuBar: (visible: boolean) => request<ShellStatus>("shell/menu-bar", "POST", { visible }),
  swallow: (on: boolean, combos: string[]) =>
    request<ShellStatus>("shell/swallow", "POST", { on, combos }),
  fixMinimized: (on: boolean) => request<ShellStatus>("shell/fix-minimized", "POST", { on }),
  unminimize: (pid: number, index: number) =>
    request<ShellStatus>("shell/windows/unminimize", "POST", { pid, index }),
  requestPermission: (kind: "accessibility" | "input-monitoring") =>
    request<ShellStatus>("shell/permissions", "POST", { kind }),
}

/** The DOM event the desktop app dispatches for every shell log entry. */
export const SHELL_EVENT = "basalisk:shell"

/** A terminal session (GET /api/terminal). */
export interface TerminalSession {
  id: string
  title: string
  shell: string
  cwd: string
  created_at: string
  started: boolean
  exited: boolean
  exit_code: number
  cols: number
  rows: number
  viewers: number
}

export const terminal = {
  state: () => request<{ enabled: boolean; sessions: TerminalSession[] }>("terminal"),
  create: (cwd = "") => request<TerminalSession>("terminal/sessions", "POST", { cwd, title: "" }),
  rename: (id: string, title: string) =>
    request<TerminalSession>(`terminal/sessions/${id}`, "PUT", { title }),
  close: (id: string) => request<void>(`terminal/sessions/${id}`, "DELETE"),
  socketURL: (id: string) =>
    `${window.location.protocol === "https:" ? "wss:" : "ws:"}//${window.location.host}/api/terminal/sessions/${id}/ws`,
}
