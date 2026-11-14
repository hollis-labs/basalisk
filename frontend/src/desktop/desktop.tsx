import {
  AppWindow,
  ChevronRight,
  Columns2,
  LayoutGrid,
  LogOut,
  Maximize,
  Minimize,
  Minus,
  PictureInPicture2,
  Search,
  Settings,
  Square,
  X,
} from "lucide-react"
import {
  type Dispatch,
  memo,
  type PointerEvent as ReactPointerEvent,
  useCallback,
  useEffect,
  useMemo,
  useReducer,
  useRef,
  useState,
} from "react"
import { ApiError, request } from "../api/account"
import "../apps"
import "./commands"
import "../agent/contexts"
import { SHELL_EVENT, type ShellEvent, type ShellStatus, shell } from "../api/shell"
import { initCite, setCiteOpener, useCard } from "../cite"
import { CiteCardLayer } from "../cite/ui"
import { useSession } from "../session"
import { useTerminalHost } from "../terminal/host"
import { DeskContext } from "./context"
import { bindings, chordFor, chordOf, keyCapture, prettyChord } from "./keys"
import { PaletteDialog } from "./palette"
import {
  type AppDef,
  allApps,
  allCommands,
  allStartups,
  appById,
  type ConfirmOptions,
  contextDescriber,
  type DeskAPI,
  handlerFor,
  overlayById,
  panelById,
  type WindowContext,
} from "./registry"
import { loadSettings, updateSettings, useDeskSettings } from "./settings"
import { applyDeskTheme, THEMES } from "./themes"
import {
  dividerX,
  GAP,
  initialState,
  type Layout,
  MIN_H,
  MIN_W,
  type Placed,
  place,
  type Rect,
  reducer,
  type Win,
  type WMAction,
} from "./wm"

const seqOf = (id: string) => Number(id.slice(id.lastIndexOf("-") + 1))

/**
 * The desktop: a bar, a canvas, and windows on it. It starts empty. Apps open from the menu
 * or the command palette, each in a window the window manager tiles or floats.
 *
 * Everything it can do is a command (commands.ts and the apps' own), and every shortcut is
 * a command's: the defaults are in the commands, the user's changes in the desk settings.
 */
export function Desktop() {
  const [state, dispatch] = useReducer(reducer, undefined, initialState)
  const canvas = useRef<HTMLDivElement>(null)
  const [area, setArea] = useState<Rect>({ x: 0, y: 0, w: 1280, h: 720 })
  const areaRef = useRef(area)
  areaRef.current = area
  const stateRef = useRef(state)
  stateRef.current = state
  const [dropTarget, setDropTarget] = useState<string | null>(null)
  const [status, setStatus] = useState<ShellStatus | null>(null)

  useEffect(() => {
    const el = canvas.current
    if (!el) return
    const measure = () => setArea({ x: 0, y: 0, w: el.clientWidth, h: el.clientHeight })
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    return () => observer.disconnect()
  }, [])

  // The native shell, when this is the desktop app: kiosk state and the camera strip.
  useEffect(() => {
    let live = true
    const refresh = () =>
      shell
        .status()
        .then((s) => live && setStatus(s))
        .catch(() => undefined)
    void refresh()
    window.addEventListener(SHELL_EVENT, refresh)
    return () => {
      live = false
      window.removeEventListener(SHELL_EVENT, refresh)
    }
  }, [])

  // For geometry(): where the windows are, whether a DOM overlay is up, what is being moved.
  const placedRef = useRef(new Map<string, Placed>())
  const overlayRef = useRef(false)
  const movingRef = useRef(new Set<string>())
  const watchers = useRef(new Set<() => void>())
  const [menuOpen, setMenuOpen] = useState(false)
  const [movingTick, setMovingTick] = useState(0)
  const card = useCard()
  const onMoving = useCallback((id: string, moving: boolean) => {
    if (moving) movingRef.current.add(id)
    else movingRef.current.delete(id)
    setMovingTick((t) => t + 1)
  }, [])

  const { signedOut } = useSession()
  const terminal = useTerminalHost()
  const terminalRef = useRef(terminal)
  terminalRef.current = terminal
  const statusRef = useRef(status)
  statusRef.current = status
  const [palette, setPalette] = useState(false)
  // Registered overlays that are showing (registry.ts, registerOverlay).
  const [overlays, setOverlays] = useState<string[]>([])
  const overlaysRef = useRef(overlays)
  overlaysRef.current = overlays
  const [question, setQuestion] = useState<{
    options: ConfirmOptions
    answer: (yes: boolean) => void
  } | null>(null)
  const settings = useDeskSettings()
  // The side panel that is open (registry.ts, registerPanel). Kept in the settings, so it
  // is still there after a restart.
  const panel = settings.panel && panelById(settings.panel) ? settings.panel : ""
  const panelRef = useRef(panel)
  panelRef.current = panel

  // What commands, apps and the palette may ask of the desktop. Stable for its lifetime.
  const desk = useMemo<DeskAPI>(() => {
    const api: DeskAPI = {
      open(id, opts = {}) {
        const app = appById(id)
        if (!app) return
        dispatch({
          type: "open",
          app: id,
          title: opts.title ?? app.title,
          size: app.size,
          area: areaRef.current,
          multi: !!app.multi,
          key: opts.key ?? app.defaultKey,
          args: opts.args,
        })
      },
      openFile(ref) {
        const name = ref.path.split("/").pop() || ref.path
        const handler = handlerFor(name)
        if (!handler) return false
        api.open(handler.app, { args: { file: ref }, key: `${ref.root}:${ref.path}`, title: name })
        return true
      },
      close(id) {
        const win = stateRef.current.wins.find((w) => w.id === id)
        if (!win?.dirty) {
          dispatch({ type: "close", id })
          return
        }
        void api
          .confirm({
            title: `Close ${win.title.replace(/^● /, "")}?`,
            message: "It has unsaved changes.",
            confirmLabel: "Close anyway",
            danger: true,
          })
          .then((yes) => yes && dispatch({ type: "close", id }))
      },
      setDirty: (id, dirty) => dispatch({ type: "dirty", id, dirty }),
      setTitle: (id, title) => dispatch({ type: "title", id, title }),
      setArgs: (id, args) => dispatch({ type: "args", id, args }),
      wm: dispatch,
      wmState: () => stateRef.current,
      area: () => areaRef.current,
      togglePalette: (open) => setPalette((p) => open ?? !p),
      toggleOverlay: (id, open) =>
        setOverlays((current) => {
          const show = open ?? !current.includes(id)
          if (show === current.includes(id) || (show && !overlayById(id))) return current
          return show ? [...current, id] : current.filter((o) => o !== id)
        }),
      overlayOpen: (id) => overlaysRef.current.includes(id),
      togglePanel(id, open) {
        const show = open ?? panelRef.current !== id
        if (show === (panelRef.current === id) || (show && !panelById(id))) return
        updateSettings({ panel: show ? id : "" })
        if (show) return
        // The keyboard goes back to the window it came from.
        const focus = stateRef.current.focus
        requestAnimationFrame(() =>
          canvas.current?.querySelector<HTMLElement>(`[data-win="${focus}"]`)?.focus(),
        )
      },
      panelOpen: (id) => panelRef.current === id,
      toggleTerminal() {
        // With a terminal window open the shortcut goes to it; otherwise it drops one down.
        const wins = stateRef.current.wins.filter((w) => w.app === "terminal")
        const win = wins.find((w) => w.key === "deck") ?? wins[0]
        if (win) dispatch({ type: "focus", id: win.id })
        else terminalRef.current.setOverlayOpen(!terminalRef.current.overlayOpen)
      },
      shellStatus: () => statusRef.current,
      setKiosk: (on) =>
        void shell
          .kiosk(on, false)
          .then(setStatus)
          .catch(() => undefined),
      setTheme(id) {
        const theme = THEMES.find((t) => t.id === id)
        if (!theme) return
        applyDeskTheme(theme)
        updateSettings({ theme: id })
      },
      geometry(id) {
        const win = stateRef.current.wins.find((w) => w.id === id)
        if (!win) return null
        const box = canvas.current?.getBoundingClientRect()
        const p = placedRef.current.get(id)
        const focused = stateRef.current.focus === id
        if (!p || !box) {
          return { rect: { x: 0, y: 0, w: 0, h: 0 }, visible: false, focused, covered: true }
        }
        // Stacking as the windows are drawn: a focused floating window is one above its z.
        const zOf = (wid: string, q: { z: number; tiled: boolean }) =>
          q.z + (stateRef.current.focus === wid && !q.tiled ? 1 : 0)
        const mine = zOf(id, p)
        let covered = overlayRef.current || movingRef.current.size > 0
        for (const [other, q] of placedRef.current) {
          if (covered) break
          if (other === id || zOf(other, q) <= mine) continue
          covered = q.x < p.x + p.w && q.x + q.w > p.x && q.y < p.y + p.h && q.y + q.h > p.y
        }
        return {
          rect: { x: box.left + p.x, y: box.top + p.y, w: p.w, h: p.h },
          visible: true,
          focused,
          covered,
        }
      },
      confirm: (options) =>
        new Promise<boolean>((resolve) => {
          setQuestion((current) => {
            current?.answer(false) // a second question replaces the first, answered "no"
            return { options, answer: resolve }
          })
        }),
      async context(id) {
        const st = stateRef.current
        const win = st.wins.find((w) => w.id === (id ?? st.focus))
        if (!win) return null
        const out: WindowContext = {
          winId: win.id,
          app: win.app,
          title: win.title.replace(/^● /, ""),
        }
        // What the window was opened with is the first thing known about it.
        const args = win.args as { file?: { root?: unknown; path?: unknown }; url?: unknown }
        if (typeof args.file?.root === "string" && typeof args.file.path === "string") {
          out.file = { root: args.file.root, path: args.file.path }
        }
        if (typeof args.url === "string") out.url = args.url
        // A selection made in the window's own DOM (a native view answers through its app).
        const el = canvas.current?.querySelector(`[data-win="${win.id}"]`)
        const selection = window.getSelection()
        if (el && selection && !selection.isCollapsed && el.contains(selection.anchorNode)) {
          out.selection = selection.toString().slice(0, 8000)
        }
        try {
          Object.assign(out, await contextDescriber(win.app)?.(win))
        } catch {
          // The app could not say more; what the desktop knows stands.
        }
        return out
      },
      subscribe(fn) {
        watchers.current.add(fn)
        return () => {
          watchers.current.delete(fn)
        }
      },
      signOut() {
        void request("logout", "POST", {})
          .then(signedOut)
          .catch((e) => e instanceof ApiError && e.status === 401 && signedOut())
      },
    }
    return api
  }, [signedOut])

  // For an agent driving the app (the shell's eval endpoint, a browser console): the desktop
  // API, so a window can be opened or a command run without synthesizing clicks.
  useEffect(() => {
    const host = window as unknown as { basalisk?: { desk: DeskAPI } }
    host.basalisk = { desk }
    return () => {
      delete host.basalisk
    }
  }, [desk])

  // What asked to be run once the desktop is up (registerStartup).
  const started = useRef(false)
  useEffect(() => {
    if (started.current) return
    started.current = true
    for (const fn of allStartups()) fn(desk)
  }, [desk])

  // References: learn what the server resolves, and open a record in a Notes window.
  useEffect(() => {
    void initCite()
    setCiteOpener((ref) =>
      desk.open("notes", {
        args: { cite: { kind: ref.kind, canonical: ref.canonical } },
        key: `${ref.kind}:${ref.canonical}`,
        title: ref.canonical,
      }),
    )
  }, [desk])

  // The server's copy of the desk settings wins over the cached one.
  useEffect(() => {
    void loadSettings().then((s) => {
      const theme = THEMES.find((t) => t.id === s.theme)
      if (theme) applyDeskTheme(theme)
      if (s.layout !== stateRef.current.layout) dispatch({ type: "layout", layout: s.layout })
    })
  }, [])
  useEffect(() => {
    if (settings.layout !== state.layout) updateSettings({ layout: state.layout })
  }, [state.layout, settings.layout])

  // Shortcuts: a chord runs the command it is bound to. Chords arrive from the page, and,
  // in the desktop app with key swallowing on, from the native event tap, which takes the
  // ones the system would otherwise keep (Cmd-Space, Cmd-Tab) and reports them here.
  const bound = useMemo(() => bindings(settings.keys), [settings.keys])
  const boundRef = useRef(bound)
  boundRef.current = bound
  const runChord = useCallback(
    (chord: string) => {
      const id = boundRef.current.get(chord)
      const command = id ? allCommands().find((c) => c.id === id) : undefined
      if (!command || (command.when && !command.when(desk))) return false
      void command.run(desk)
      return true
    },
    [desk],
  )
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (keyCapture.active) return
      const chord = chordOf(e)
      // An element may keep chords for itself: data-keys-own="cmd+k cmd+b" (Notes' editor).
      if (
        chord &&
        (e.target as Element | null)
          ?.closest?.("[data-keys-own]")
          ?.getAttribute("data-keys-own")
          ?.split(" ")
          .includes(chord)
      )
        return
      if (!chord || !runChord(chord)) return
      e.preventDefault()
      e.stopPropagation()
    }
    const onShell = (e: Event) => {
      const detail = (e as CustomEvent<ShellEvent>).detail
      if (detail?.kind === "swallowed") runChord(detail.detail)
    }
    window.addEventListener("keydown", onKey, true)
    window.addEventListener(SHELL_EVENT, onShell)
    return () => {
      window.removeEventListener("keydown", onKey, true)
      window.removeEventListener(SHELL_EVENT, onShell)
    }
  }, [runChord])

  // Keyboard focus follows window focus, and returns to the window when the palette closes.
  useEffect(() => {
    if (!state.focus || palette || overlays.length) return
    const el = canvas.current?.querySelector<HTMLElement>(`[data-win="${state.focus}"]`)
    if (!el || el.contains(document.activeElement)) return
    const target =
      el.querySelector<HTMLElement>('.xterm-host[data-active="true"] .xterm-helper-textarea') ?? el
    target.focus({ preventScroll: true })
  }, [state.focus, palette, overlays.length])

  // The room an open side panel takes, so floating windows keep clear of it (wm.ts, place).
  const reserved = panel ? Math.min(panelById(panel)?.width ?? 0, window.innerWidth / 2) : 0
  const placed = useMemo(() => place(state, area, reserved), [state, area, reserved])
  placedRef.current = placed
  overlayRef.current =
    palette || menuOpen || terminal.overlayOpen || !!card || !!question || overlays.length > 0
  // Tell whoever follows window geometry (a native view) that it may have changed.
  // biome-ignore lint/correctness/useExhaustiveDependencies: these are the things geometry() reads
  useEffect(() => {
    for (const fn of watchers.current) fn()
  }, [
    placed,
    state.focus,
    palette,
    menuOpen,
    terminal.overlayOpen,
    card,
    movingTick,
    question,
    overlays,
  ])
  const divider = dividerX(state, area)

  const hitTest = useCallback(
    (clientX: number, clientY: number, self: string) => {
      const box = canvas.current?.getBoundingClientRect()
      if (!box) return null
      const x = clientX - box.left
      const y = clientY - box.top
      for (const [id, r] of placed) {
        if (id !== self && r.tiled && x >= r.x && x <= r.x + r.w && y >= r.y && y <= r.y + r.h)
          return id
      }
      return null
    },
    [placed],
  )

  const dragDivider = (e: ReactPointerEvent<HTMLDivElement>) => {
    const el = e.currentTarget
    el.setPointerCapture(e.pointerId)
    const box = canvas.current?.getBoundingClientRect()
    const move = (ev: PointerEvent) => {
      if (!box) return
      dispatch({
        type: "ratio",
        value: (ev.clientX - box.left - GAP * 1.5) / (areaRef.current.w - 3 * GAP),
      })
    }
    const up = () => {
      el.removeEventListener("pointermove", move)
      el.removeEventListener("pointerup", up)
    }
    el.addEventListener("pointermove", move)
    el.addEventListener("pointerup", up)
  }

  const notch =
    !!status?.available && status.kiosk && !status.menu_bar_visible && status.screen.safe_top > 0
  const ordered = useMemo(
    () => [...state.wins].sort((a, b) => seqOf(a.id) - seqOf(b.id)),
    [state.wins],
  )

  return (
    <DeskContext.Provider value={desk}>
      <div className="desk" data-layout={state.layout}>
        <header className="desk-bar" data-notch={notch}>
          <div className="desk-bar-side">
            <DeskMenu
              layout={state.layout}
              status={status}
              onOpen={(id) => desk.open(id)}
              onLayout={() => dispatch({ type: "layout" })}
              onKiosk={() => status && void shell.kiosk(!status.kiosk, false).then(setStatus)}
              onOpenChange={setMenuOpen}
            />
            <div className="desk-tasks">
              {state.wins.map((w) => {
                const app = appById(w.app)
                const Icon = app?.icon ?? AppWindow
                const active = state.focus === w.id && !w.minimized
                return (
                  <button
                    key={w.id}
                    type="button"
                    className="desk-task"
                    data-active={active}
                    data-minimized={w.minimized}
                    title={w.title}
                    onClick={() =>
                      dispatch(
                        active ? { type: "minimize", id: w.id } : { type: "focus", id: w.id },
                      )
                    }
                  >
                    <Icon aria-hidden="true" size={14} strokeWidth={1.6} />
                    <span>{w.title}</span>
                  </button>
                )
              })}
            </div>
          </div>
          <div className="desk-bar-side desk-bar-end">
            <button
              type="button"
              className="desk-bar-button"
              title={`Command palette (${prettyChord(chordFor("palette.toggle", settings.keys))})`}
              aria-label="Command palette"
              onClick={() => setPalette(true)}
            >
              <Search aria-hidden="true" size={14} strokeWidth={1.6} />
            </button>
            <button
              type="button"
              className="desk-bar-button"
              title={`${state.layout === "tile" ? "Tiling" : "Floating"} layout (⌃⌥T)`}
              onClick={() => dispatch({ type: "layout" })}
            >
              {state.layout === "tile" ? (
                <Columns2 aria-hidden="true" size={14} strokeWidth={1.6} />
              ) : (
                <AppWindow aria-hidden="true" size={14} strokeWidth={1.6} />
              )}
              <span>{state.layout}</span>
            </button>
            {status?.available && (
              <button
                type="button"
                className="desk-bar-button"
                title={`${status.kiosk ? "Leave" : "Enter"} full screen (⌃⌥⌘F)`}
                aria-label={status.kiosk ? "Leave full screen" : "Enter full screen"}
                onClick={() => void shell.kiosk(!status.kiosk, false).then(setStatus)}
              >
                {status.kiosk ? (
                  <Minimize aria-hidden="true" size={14} strokeWidth={1.6} />
                ) : (
                  <Maximize aria-hidden="true" size={14} strokeWidth={1.6} />
                )}
              </button>
            )}
            <Clock />
          </div>
        </header>

        <div className="desk-main">
          <div ref={canvas} className="desk-canvas">
            {!state.wins.length && (
              <div className="desk-empty" aria-hidden="true">
                <span className="desk-empty-mark">basalisk</span>
                <span>
                  {prettyChord(chordFor("palette.toggle", settings.keys))} commands ·{" "}
                  {prettyChord(chordFor("terminal.toggle", settings.keys))} terminal
                </span>
              </div>
            )}
            {ordered.map((w) => {
              const rect = placed.get(w.id)
              const app = appById(w.app)
              if (!app) return null
              return (
                <WindowFrame
                  key={w.id}
                  win={w}
                  app={app}
                  rect={rect}
                  focused={state.focus === w.id}
                  layout={state.layout}
                  dropTarget={dropTarget === w.id}
                  dispatch={dispatch}
                  hitTest={hitTest}
                  onDropTarget={setDropTarget}
                  onMoving={onMoving}
                  onClose={desk.close}
                />
              )
            })}
            {divider !== null && (
              <div
                className="desk-divider"
                style={{ left: divider, width: GAP, top: GAP, bottom: GAP }}
                onPointerDown={dragDivider}
                role="separator"
                aria-orientation="vertical"
              />
            )}
          </div>
          {panel && (
            <aside
              className="desk-panel"
              data-panel={panel}
              style={{ width: panelById(panel)?.width }}
              aria-label={panelById(panel)?.title}
            >
              {panelById(panel)?.render({ close: () => desk.togglePanel(panel, false) })}
            </aside>
          )}
        </div>
        {palette && <PaletteDialog desk={desk} onClose={() => setPalette(false)} />}
        {overlays.map((id) => (
          <div key={id} className="desk-overlay" data-overlay={id}>
            {overlayById(id)?.render({ close: () => desk.toggleOverlay(id, false) })}
          </div>
        ))}
        {question && (
          <ConfirmDialog
            options={question.options}
            onAnswer={(yes) => {
              question.answer(yes)
              setQuestion(null)
            }}
          />
        )}
        <CiteCardLayer />
      </div>
    </DeskContext.Provider>
  )
}

/** A yes/no question, centred over the desktop. Enter confirms, Escape declines. */
function ConfirmDialog({
  options,
  onAnswer,
}: {
  options: ConfirmOptions
  onAnswer: (yes: boolean) => void
}) {
  return (
    <div className="desk-palette-scrim desk-confirm-scrim" onPointerDown={() => onAnswer(false)}>
      <div
        className="desk-confirm"
        role="alertdialog"
        aria-modal="true"
        aria-label={options.title}
        onPointerDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          if (e.key === "Escape") onAnswer(false)
        }}
      >
        <h3>{options.title}</h3>
        {options.message && <p>{options.message}</p>}
        <div className="desk-confirm-actions">
          <button
            type="button"
            className="desk-button"
            // biome-ignore lint/a11y/noAutofocus: a destructive question starts on the safe answer
            autoFocus={!!options.danger}
            onClick={() => onAnswer(false)}
          >
            Cancel
          </button>
          <button
            type="button"
            className="desk-button"
            data-primary="true"
            data-danger={!!options.danger}
            // biome-ignore lint/a11y/noAutofocus: the dialog exists to be answered
            autoFocus={!options.danger}
            onClick={() => onAnswer(true)}
          >
            {options.confirmLabel ?? "OK"}
          </button>
        </div>
      </div>
    </div>
  )
}

/** The app's content. Memoized so moving a window does not re-render what is inside it. */
const AppBody = memo(function AppBody({
  app,
  winId,
  args,
}: {
  app: AppDef
  winId: string
  args: Record<string, unknown>
}) {
  return <>{app.render({ winId, args })}</>
})

const EDGES = ["n", "s", "e", "w", "ne", "nw", "se", "sw"] as const

function WindowFrame({
  win,
  app,
  rect,
  focused,
  layout,
  dropTarget,
  dispatch,
  hitTest,
  onDropTarget,
  onMoving,
  onClose,
}: {
  win: Win
  app: AppDef
  rect: Placed | undefined
  focused: boolean
  layout: Layout
  dropTarget: boolean
  dispatch: Dispatch<WMAction>
  hitTest: (x: number, y: number, self: string) => string | null
  onDropTarget: (id: string | null) => void
  onMoving: (id: string, moving: boolean) => void
  onClose: (id: string) => void
}) {
  const drag = useRef<{ px: number; py: number; x: number; y: number; moved: boolean } | null>(null)
  const resize = useRef<{ dir: string; px: number; py: number; start: Rect } | null>(null)
  const [offset, setOffset] = useState<{ dx: number; dy: number } | null>(null)
  const Icon = app.icon
  const tiled = !!rect?.tiled
  const sizable = !!rect && !tiled && !win.maximized

  const onTitleDown = (e: ReactPointerEvent<HTMLElement>) => {
    if (e.button !== 0 || !rect || (e.target as HTMLElement).closest("button")) return
    e.currentTarget.setPointerCapture(e.pointerId)
    drag.current = { px: e.clientX, py: e.clientY, x: rect.x, y: rect.y, moved: false }
  }
  const onTitleMove = (e: ReactPointerEvent<HTMLElement>) => {
    const d = drag.current
    if (!d || !rect) return
    const dx = e.clientX - d.px
    const dy = e.clientY - d.py
    if (!d.moved && Math.hypot(dx, dy) < 4) return
    if (!d.moved) onMoving(win.id, true)
    d.moved = true
    if (tiled) {
      // A tiled window is dragged onto another to trade places.
      setOffset({ dx, dy })
      onDropTarget(hitTest(e.clientX, e.clientY, win.id))
    } else if (!win.maximized) {
      dispatch({
        type: "rect",
        id: win.id,
        rect: { x: d.x + dx, y: d.y + dy, w: rect.w, h: rect.h },
      })
    }
  }
  const onTitleUp = (e: ReactPointerEvent<HTMLElement>) => {
    const d = drag.current
    drag.current = null
    if (d?.moved) onMoving(win.id, false)
    if (!d?.moved || !tiled) return
    const target = hitTest(e.clientX, e.clientY, win.id)
    if (target) dispatch({ type: "swap", a: win.id, b: target })
    setOffset(null)
    onDropTarget(null)
  }

  const onEdgeDown = (dir: string) => (e: ReactPointerEvent<HTMLElement>) => {
    if (!rect) return
    e.stopPropagation()
    e.currentTarget.setPointerCapture(e.pointerId)
    onMoving(win.id, true)
    resize.current = {
      dir,
      px: e.clientX,
      py: e.clientY,
      start: { x: rect.x, y: rect.y, w: rect.w, h: rect.h },
    }
  }
  const onEdgeMove = (e: ReactPointerEvent<HTMLElement>) => {
    const r = resize.current
    if (!r) return
    const dx = e.clientX - r.px
    const dy = e.clientY - r.py
    let { x, y, w, h } = r.start
    if (r.dir.includes("e")) w = Math.max(MIN_W, w + dx)
    if (r.dir.includes("s")) h = Math.max(MIN_H, h + dy)
    if (r.dir.includes("w")) {
      const next = Math.max(MIN_W, w - dx)
      x += w - next
      w = next
    }
    if (r.dir.includes("n")) {
      const next = Math.max(MIN_H, h - dy)
      y += h - next
      h = next
    }
    dispatch({ type: "rect", id: win.id, rect: { x, y, w, h } })
  }
  const onEdgeUp = () => {
    if (resize.current) onMoving(win.id, false)
    resize.current = null
  }

  return (
    <section
      className="desk-window"
      data-win={win.id}
      data-app={win.app}
      data-focused={focused}
      data-tiled={tiled}
      data-dragging={!!offset}
      data-drop={dropTarget}
      tabIndex={-1}
      aria-label={win.title}
      hidden={!rect}
      style={
        rect
          ? {
              left: rect.x,
              top: rect.y,
              width: rect.w,
              height: rect.h,
              zIndex: offset ? 9000 : rect.z + (focused && !tiled ? 1 : 0),
              transform: offset ? `translate(${offset.dx}px, ${offset.dy}px)` : undefined,
            }
          : undefined
      }
      onPointerDownCapture={() => !focused && dispatch({ type: "focus", id: win.id })}
    >
      <header
        className="desk-titlebar"
        onPointerDown={onTitleDown}
        onPointerMove={onTitleMove}
        onPointerUp={onTitleUp}
        onDoubleClick={(e) =>
          !(e.target as HTMLElement).closest("button") && dispatch({ type: "maximize", id: win.id })
        }
      >
        <Icon aria-hidden="true" size={14} strokeWidth={1.6} />
        <span className="desk-title">{win.title}</span>
        <div className="desk-window-actions">
          {layout === "tile" && (
            <button
              type="button"
              title={win.floating ? "Tile (⌃⌥F)" : "Float (⌃⌥F)"}
              aria-label={win.floating ? "Tile window" : "Float window"}
              onClick={() => dispatch({ type: "float", id: win.id })}
            >
              {win.floating ? (
                <Columns2 aria-hidden="true" size={13} strokeWidth={1.6} />
              ) : (
                <PictureInPicture2 aria-hidden="true" size={13} strokeWidth={1.6} />
              )}
            </button>
          )}
          <button
            type="button"
            title="Hide (⌃⌥H)"
            aria-label="Hide window"
            onClick={() => dispatch({ type: "minimize", id: win.id })}
          >
            <Minus aria-hidden="true" size={13} strokeWidth={1.6} />
          </button>
          <button
            type="button"
            title="Maximize (⌃⌥Enter)"
            aria-label="Maximize window"
            onClick={() => dispatch({ type: "maximize", id: win.id })}
          >
            <Square aria-hidden="true" size={11} strokeWidth={1.8} />
          </button>
          <button
            type="button"
            className="desk-close"
            title="Close (⌃⌥W)"
            aria-label="Close window"
            onClick={() => onClose(win.id)}
          >
            <X aria-hidden="true" size={13} strokeWidth={1.6} />
          </button>
        </div>
      </header>
      <div className="desk-window-body" data-bare={!!app.bare}>
        <AppBody app={app} winId={win.id} args={win.args} />
      </div>
      {sizable &&
        EDGES.map((dir) => (
          <div
            key={dir}
            className="desk-edge"
            data-dir={dir}
            onPointerDown={onEdgeDown(dir)}
            onPointerMove={onEdgeMove}
            onPointerUp={onEdgeUp}
          />
        ))}
    </section>
  )
}

function DeskMenu({
  layout,
  status,
  onOpen,
  onLayout,
  onKiosk,
  onOpenChange,
}: {
  layout: Layout
  status: ShellStatus | null
  onOpen: (app: string) => void
  onLayout: () => void
  onKiosk: () => void
  onOpenChange: (open: boolean) => void
}) {
  const { signedOut } = useSession()
  const [open, setOpen] = useState(false)
  useEffect(() => onOpenChange(open), [open, onOpenChange])
  const [apps, setApps] = useState(false)
  const root = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) {
      setApps(false)
      return
    }
    const onDown = (e: PointerEvent) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false)
    }
    document.addEventListener("pointerdown", onDown, true)
    document.addEventListener("keydown", onKey)
    return () => {
      document.removeEventListener("pointerdown", onDown, true)
      document.removeEventListener("keydown", onKey)
    }
  }, [open])

  const pick = (fn: () => void) => () => {
    setOpen(false)
    fn()
  }
  const signOut = async () => {
    try {
      await request("logout", "POST", {})
    } catch (e) {
      if (!(e instanceof ApiError && e.status === 401)) return
    }
    signedOut()
  }

  return (
    <div className="desk-menu-root" ref={root}>
      <button
        type="button"
        className="desk-menu-button"
        aria-label="Menu"
        aria-haspopup="menu"
        aria-expanded={open}
        data-open={open}
        onClick={() => setOpen((o) => !o)}
      >
        <LayoutGrid aria-hidden="true" size={15} strokeWidth={1.6} />
      </button>
      {open && (
        <div className="desk-menu" role="menu">
          <div
            className="desk-menu-sub"
            onPointerEnter={() => setApps(true)}
            onPointerLeave={() => setApps(false)}
          >
            <button
              type="button"
              role="menuitem"
              aria-haspopup="menu"
              aria-expanded={apps}
              onClick={() => setApps((a) => !a)}
            >
              <AppWindow aria-hidden="true" size={15} strokeWidth={1.6} />
              <span>Apps</span>
              <ChevronRight
                aria-hidden="true"
                size={14}
                strokeWidth={1.6}
                className="desk-menu-hint"
              />
            </button>
            {apps && (
              <div className="desk-menu desk-menu-fly" role="menu">
                {allApps()
                  .filter((a) => a.menu)
                  .map((a) => (
                    <button
                      key={a.id}
                      type="button"
                      role="menuitem"
                      onClick={pick(() => onOpen(a.id))}
                    >
                      <a.icon aria-hidden="true" size={15} strokeWidth={1.6} />
                      <span>{a.title}</span>
                    </button>
                  ))}
              </div>
            )}
          </div>
          <button type="button" role="menuitem" onClick={pick(() => onOpen("settings"))}>
            <Settings aria-hidden="true" size={15} strokeWidth={1.6} />
            <span>Settings</span>
          </button>
          <hr />
          <button type="button" role="menuitem" onClick={pick(onLayout)}>
            {layout === "tile" ? (
              <AppWindow aria-hidden="true" size={15} strokeWidth={1.6} />
            ) : (
              <Columns2 aria-hidden="true" size={15} strokeWidth={1.6} />
            )}
            <span>{layout === "tile" ? "Float windows" : "Tile windows"}</span>
            <kbd className="desk-menu-hint">⌃⌥T</kbd>
          </button>
          {status?.available && (
            <button type="button" role="menuitem" onClick={pick(onKiosk)}>
              {status.kiosk ? (
                <Minimize aria-hidden="true" size={15} strokeWidth={1.6} />
              ) : (
                <Maximize aria-hidden="true" size={15} strokeWidth={1.6} />
              )}
              <span>{status.kiosk ? "Leave full screen" : "Full screen"}</span>
              <kbd className="desk-menu-hint">⌃⌥⌘F</kbd>
            </button>
          )}
          <hr />
          <button type="button" role="menuitem" onClick={pick(() => void signOut())}>
            <LogOut aria-hidden="true" size={15} strokeWidth={1.6} />
            <span>Sign out</span>
          </button>
        </div>
      )}
    </div>
  )
}

function Clock() {
  const [now, setNow] = useState(() => new Date())
  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), 15000)
    return () => window.clearInterval(timer)
  }, [])
  const date = now.toLocaleDateString([], { weekday: "short", day: "numeric", month: "short" })
  const time = now.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
  return (
    <time className="desk-clock" dateTime={now.toISOString()}>
      <span>{date}</span>
      {time}
    </time>
  )
}
