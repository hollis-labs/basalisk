/**
 * A terminal window of its own: one shell, in a window beside the others, so several can
 * be tiled. The first Terminal window (and the drop-down) is still the deck with its tabs;
 * a shell shown here is left out of the deck. Closing the window ends the shell, and the
 * shell ending closes the window.
 */
import { SquareTerminal } from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { terminal as api } from "../api/shell"
import { useDesk } from "../desktop/context"
import { type DeskAPI, registerCommands, type WindowProps } from "../desktop/registry"
import { useTerminalHost } from "./host"
import { type DeckKeys, XTermView } from "./xterm"

const FONT_KEY = "basalisk.terminal.fontSize"
const DEFAULT_FONT = 14
const storedFont = () => {
  const n = Number(window.localStorage.getItem(FONT_KEY))
  return n >= 9 && n <= 32 ? n : DEFAULT_FONT
}

/** Told when a shell goes to, or comes back from, a window of its own (detail: its id). */
export const SOLO_EVENT = "basalisk:terminal-solo"

/** window id -> the shell it shows. */
const solo = new Map<string, string>()
/** Windows whose shell is being made, so it is made once. */
const making = new Set<string>()
let watching = false

/** Whether a shell is shown in a window of its own (and so is not a tab in the deck). */
export const isSolo = (session: string) => [...solo.values()].includes(session)

/** The shell a terminal window of its own shows, if it is one. */
export const soloSession = (winId: string) => solo.get(winId)

function claim(desk: DeskAPI, winId: string, session: string) {
  if (solo.get(winId) === session) return
  solo.set(winId, session)
  window.dispatchEvent(new CustomEvent(SOLO_EVENT, { detail: session }))
  if (watching) return
  watching = true
  // A window that is gone was closed: its shell ends with it.
  desk.subscribe(() => {
    const open = new Set(desk.wmState().wins.map((w) => w.id))
    for (const [win, id] of solo) {
      if (open.has(win)) continue
      solo.delete(win)
      void api.close(id).catch(() => undefined)
    }
  })
}

let opened = 0
/** Open a terminal window of its own: a new shell (in a folder, if given), or an existing one. */
export function openTerminalWindow(
  desk: DeskAPI,
  opts: { session?: string; cwd?: string; title?: string } = {},
) {
  opened++
  desk.open("terminal", {
    args: { solo: true, ...(opts.session ? { session: opts.session } : {}), cwd: opts.cwd ?? "" },
    key: opts.session ?? `solo-${Date.now()}-${opened}`,
    title: opts.title ?? "Terminal",
  })
}

export function SoloTerminal({ winId, args }: WindowProps) {
  const desk = useDesk()
  const { enabled } = useTerminalHost()
  const session = typeof args.session === "string" ? args.session : ""
  const cwd = typeof args.cwd === "string" ? args.cwd : ""
  const [error, setError] = useState("")
  const [fontSize, setFontSize] = useState(storedFont)
  const [epoch, setEpoch] = useState(0)

  // No shell yet: make one, and remember it as what this window shows.
  useEffect(() => {
    if (!enabled || session || making.has(winId)) return
    making.add(winId)
    // A window brought back after a restart: its old shell if it is still there, else a new one.
    const was = typeof args.was === "string" ? args.was : ""
    const find = was
      ? api
          .state()
          .then((st) => st.sessions.find((s) => s.id === was && !s.exited))
          .catch(() => undefined)
      : Promise.resolve(undefined)
    void find
      .then((old) => old ?? api.create(cwd))
      .then((s) => {
        claim(desk, winId, s.id)
        desk.setArgs(winId, { solo: true, session: s.id, cwd: s.cwd })
        desk.setTitle(winId, s.title || "Terminal")
      })
      .catch((e) => setError(e instanceof Error ? e.message : "Could not open a terminal."))
      .finally(() => making.delete(winId))
  }, [enabled, session, cwd, winId, desk, args.was])

  useEffect(() => {
    if (session) claim(desk, winId, session)
  }, [desk, winId, session])

  // Refit and take the keyboard when the window gets focus.
  useEffect(() => {
    let had = desk.wmState().focus === winId
    return desk.subscribe(() => {
      const has = desk.wmState().focus === winId
      if (has && !had) setEpoch((e) => e + 1)
      had = has
    })
  }, [desk, winId])

  const keys = useMemo<DeckKeys>(
    () => ({
      newTab: () => openTerminalWindow(desk),
      closeTab: () => desk.close(winId),
      selectTab: () => undefined,
      stepTab: () => undefined,
      zoom: (delta) =>
        setFontSize((size) => {
          const next = delta === 0 ? DEFAULT_FONT : Math.max(9, Math.min(32, size + delta))
          window.localStorage.setItem(FONT_KEY, String(next))
          return next
        }),
    }),
    [desk, winId],
  )

  if (enabled === null) return <p className="desk-note">Opening the terminal…</p>
  if (!enabled)
    return (
      <p className="desk-note">
        This Basalisk does not hand out a shell. The terminal runs in the desktop app.
      </p>
    )
  if (error) return <p className="desk-note">{error}</p>
  if (!session) return <p className="desk-note">Opening a shell…</p>
  return (
    <div className="terminal-deck terminal-solo">
      <div className="terminal-stage">
        <XTermView
          id={session}
          active
          epoch={epoch}
          fontSize={fontSize}
          keys={keys}
          // The shell ended on its own (exit, ctrl-d): the window goes with it.
          onExit={() => desk.close(winId)}
        />
      </div>
    </div>
  )
}

registerCommands([
  {
    id: "terminal.window",
    title: "Terminal: new window",
    section: "Terminal",
    icon: SquareTerminal,
    keywords: "shell console another second split tile",
    run: (desk) => openTerminalWindow(desk),
  },
])
