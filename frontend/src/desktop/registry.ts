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
  wm(action: WMAction): void
  wmState(): WMState
  area(): Rect
  togglePalette(open?: boolean): void
  toggleTerminal(): void
  shellStatus(): ShellStatus | null
  setKiosk(on: boolean): void
  setTheme(id: string): void
  signOut(): void
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

export function registerFileHandler(h: FileHandler) {
  handlers.push(h)
}
export function handlerFor(name: string): FileHandler | undefined {
  return handlers.find((h) => h.match(name.toLowerCase()))
}
