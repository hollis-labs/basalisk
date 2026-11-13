/**
 * What plugs into the desktop: apps, commands, palette providers and file handlers. Each is
 * registered once, at import time, by the module that owns it (see frontend/src/apps), so
 * adding an app touches its own files and one import line.
 *
 * Nothing here imports the desktop itself; the desktop and the apps both import this.
 */
import type { LucideIcon } from "lucide-react"
import type { ReactNode } from "react"
import type { ShellStatus } from "../api/shell"
import type { Rect, WMAction, WMState } from "./wm"

/** A file the file API can serve: a root id and a path inside it. */
export interface FileRef {
  root: string
  path: string
}

/** What an app's window body receives. */
export interface WindowProps {
  winId: string
  /** Whatever the window was opened with, for instance `{ file: FileRef }`. */
  args: Record<string, unknown>
}

/** Something that opens in a window. */
export interface AppDef {
  id: string
  title: string
  icon: LucideIcon
  /** Listed under Apps in the menu. */
  menu: boolean
  size: [number, number]
  /** The body fills the window edge to edge and manages its own scrolling. */
  bare?: boolean
  /**
   * Several windows of this app may be open at once. Opening it again makes a new window,
   * unless one was opened with the same `key`, which is brought forward instead.
   */
  multi?: boolean
  render: (props: WindowProps) => ReactNode
}

export interface OpenOptions {
  args?: Record<string, unknown>
  /** For a `multi` app: identifies the window (a file path, say), so it is not opened twice. */
  key?: string
  title?: string
}

/** What commands, apps and providers may ask of the desktop. */
export interface DeskAPI {
  open(app: string, opts?: OpenOptions): void
  /** Opens a file in the app registered for it. False when no app handles it. */
  openFile(ref: FileRef): boolean
  close(winId: string): void
  setTitle(winId: string, title: string): void
  /**
   * Replace what the window was opened with (the page a browser window is on, say), so the
   * window manager's record of it stays true. The app's body is re-rendered with the new value.
   */
  setArgs(winId: string, args: Record<string, unknown>): void
  wm(action: WMAction): void
  wmState(): WMState
  area(): Rect
  togglePalette(open?: boolean): void
  /** Show or hide a registered overlay (toggles when `open` is not given). */
  toggleOverlay(id: string, open?: boolean): void
  overlayOpen(id: string): boolean
  toggleTerminal(): void
  shellStatus(): ShellStatus | null
  setKiosk(on: boolean): void
  setTheme(id: string): void
  signOut(): void
  /**
   * Ask a yes/no question in a dialog on the desktop. Resolves true when confirmed. Use this
   * instead of window.confirm, which the desktop app's web view answers "no" without showing
   * anything.
   */
  confirm(question: ConfirmOptions): Promise<boolean>
  /**
   * Where a window is on screen and whether anything is drawn over it. For content that is
   * not DOM (a native web view placed over the window): show it only while `covered` is false.
   * Null for an unknown window.
   */
  geometry(winId: string): WindowGeometry | null
  /** Calls back after anything that can change a geometry (windows, layout, overlays). Returns the unsubscribe. */
  subscribe(fn: () => void): () => void
}

export interface ConfirmOptions {
  title: string
  message?: string
  /** The confirming button's label. Default "OK". */
  confirmLabel?: string
  /** The action destroys something: the confirming button is drawn as a warning. */
  danger?: boolean
}

export interface WindowGeometry {
  /** The window frame, title bar included, in viewport CSS pixels. */
  rect: { x: number; y: number; w: number; h: number }
  /** False while the window is hidden (minimized). */
  visible: boolean
  focused: boolean
  /**
   * Something is, or may be, drawn over the window: another window overlaps it from above,
   * the palette, the menu, the drop-down terminal or a preview card is up, or the window is
   * being moved or resized.
   */
  covered: boolean
}

/** An action with a name: it shows in the palette and can be given a shortcut. */
export interface Command {
  id: string
  title: string
  section: string
  icon?: LucideIcon
  /** Extra words the palette matches on. */
  keywords?: string
  /** Default shortcut, written like "ctrl+alt+t". The user can change or clear it. */
  keys?: string
  /** Not offered when this returns false (no focused window, not the desktop app...). */
  when?: (desk: DeskAPI) => boolean
  /** Kept out of the palette list (it still has a shortcut). */
  hidden?: boolean
  run: (desk: DeskAPI) => void | Promise<void>
}

/** A row in the palette that is not a command: a file, a note, a cited record. */
export interface PaletteItem {
  id: string
  title: string
  subtitle?: string
  section: string
  icon?: LucideIcon
  run: (desk: DeskAPI) => void | Promise<void>
}

/** A source of palette rows for what the user typed. */
export interface PaletteProvider {
  id: string
  search: (query: string, desk: DeskAPI) => PaletteItem[] | Promise<PaletteItem[]>
}

/** Says which app opens a file. The first handler that matches wins. */
export interface FileHandler {
  app: string
  match: (name: string) => boolean
}

const apps = new Map<string, AppDef>()
const commands = new Map<string, Command>()
const providers = new Map<string, PaletteProvider>()
const handlers: FileHandler[] = []
const overlays = new Map<string, OverlayDef>()

export function registerApp(def: AppDef) {
  apps.set(def.id, def)
}
export const allApps = () => [...apps.values()]
export const appById = (id: string) => apps.get(id)

export function registerCommands(list: Command[]) {
  for (const c of list) commands.set(c.id, c)
}

/** Every command: the registered ones, plus "Open <app>" for each app. */
export function allCommands(): Command[] {
  const open: Command[] = allApps().map((a) => ({
    id: `app.open.${a.id}`,
    title: `Open ${a.title}`,
    section: "Apps",
    icon: a.icon,
    run: (desk) => desk.open(a.id),
  }))
  return [...open, ...commands.values()]
}

export function registerProvider(p: PaletteProvider) {
  providers.set(p.id, p)
}
export const allProviders = () => [...providers.values()]

/**
 * Something drawn over the whole desktop rather than in a window: a capture box, a switcher.
 * The desktop mounts it while it is open, counts it as covering every window (so native
 * views step aside), and returns the keyboard to the focused window when it closes. The
 * overlay draws its own backdrop and handles its own keys; `close` hides it.
 */
export interface OverlayDef {
  id: string
  render: (props: { close: () => void }) => ReactNode
}

export function registerOverlay(def: OverlayDef) {
  overlays.set(def.id, def)
}
export const overlayById = (id: string) => overlays.get(id)

export function registerFileHandler(h: FileHandler) {
  handlers.push(h)
}
export function handlerFor(name: string): FileHandler | undefined {
  return handlers.find((h) => h.match(name.toLowerCase()))
}
