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
import { SHELL_EVENT, type ShellStatus, shell, TERMINAL_FOCUS_EVENT } from "../api/shell"
import { useSession } from "../session"
import { APPS, type AppDef, appById } from "./apps"
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
 * (and later from the command palette), each in a window the window manager tiles or floats.
 *
 * Keys, all with ⌃⌥: T tile/float layout · J K next/previous window · Enter maximize ·
 * F float or re-tile the window · M make it the master · H hide · W close ·
 * ← → resize the master (tile) or snap to a half (float) · ↑ fill · ↓ centre.
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

  const open = useCallback((id: string) => {
    const app = appById(id)
    if (app)
      dispatch({ type: "open", app: id, title: app.title, size: app.size, area: areaRef.current })
  }, [])

  // Ctrl+` with a terminal window open brings that window forward.
  useEffect(() => {
    const onTerminal = () => open("terminal")
    window.addEventListener(TERMINAL_FOCUS_EVENT, onTerminal)
    return () => window.removeEventListener(TERMINAL_FOCUS_EVENT, onTerminal)
  }, [open])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!e.ctrlKey || !e.altKey || e.metaKey) return
      const s = stateRef.current
      const id = s.focus
      const tiling = s.layout === "tile"
      let action: WMAction | null = null
      if (e.code === "KeyT") action = { type: "layout" }
      else if (e.code === "KeyJ") action = { type: "cycle", delta: 1 }
      else if (e.code === "KeyK") action = { type: "cycle", delta: -1 }
      else if (tiling && e.code === "ArrowLeft") action = { type: "ratio", value: s.ratio - 0.04 }
      else if (tiling && e.code === "ArrowRight") action = { type: "ratio", value: s.ratio + 0.04 }
      else if (id) {
        if (e.code === "Enter") action = { type: "maximize", id }
        else if (e.code === "KeyW") action = { type: "close", id }
        else if (e.code === "KeyF") action = { type: "float", id }
        else if (e.code === "KeyM") action = { type: "promote", id }
        else if (e.code === "KeyH") action = { type: "minimize", id }
        else if (e.code === "ArrowLeft")
          action = { type: "snap", id, to: "left", area: areaRef.current }
        else if (e.code === "ArrowRight")
          action = { type: "snap", id, to: "right", area: areaRef.current }
        else if (e.code === "ArrowUp")
          action = tiling
            ? { type: "maximize", id }
            : { type: "snap", id, to: "full", area: areaRef.current }
        else if (e.code === "ArrowDown")
          action = { type: "snap", id, to: "center", area: areaRef.current }
      }
      if (!action) return
      e.preventDefault()
      e.stopPropagation()
      dispatch(action)
    }
    window.addEventListener("keydown", onKey, true)
    return () => window.removeEventListener("keydown", onKey, true)
  }, [])

  // Keyboard focus follows window focus.
  useEffect(() => {
    if (!state.focus) return
    const el = canvas.current?.querySelector<HTMLElement>(`[data-win="${state.focus}"]`)
    if (!el || el.contains(document.activeElement)) return
    const target =
      el.querySelector<HTMLElement>('.xterm-host[data-active="true"] .xterm-helper-textarea') ?? el
    target.focus({ preventScroll: true })
  }, [state.focus])

  const placed = useMemo(() => place(state, area), [state, area])
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
    <div className="desk" data-layout={state.layout}>
      <header className="desk-bar" data-notch={notch}>
        <div className="desk-bar-side">
          <DeskMenu
            layout={state.layout}
            status={status}
            onOpen={open}
            onLayout={() => dispatch({ type: "layout" })}
            onKiosk={() => status && void shell.kiosk(!status.kiosk, false).then(setStatus)}
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
                    dispatch(active ? { type: "minimize", id: w.id } : { type: "focus", id: w.id })
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

      <div ref={canvas} className="desk-canvas">
        {!state.wins.length && (
          <div className="desk-empty" aria-hidden="true">
            <span className="desk-empty-mark">basalisk</span>
            <span>⌃` terminal</span>
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
    </div>
  )
}

/** The app's content. Memoized so moving a window does not re-render what is inside it. */
const AppBody = memo(function AppBody({ app }: { app: AppDef }) {
  return <>{app.render()}</>
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
            onClick={() => dispatch({ type: "close", id: win.id })}
          >
            <X aria-hidden="true" size={13} strokeWidth={1.6} />
          </button>
        </div>
      </header>
      <div className="desk-window-body" data-bare={!!app.bare}>
        <AppBody app={app} />
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
}: {
  layout: Layout
  status: ShellStatus | null
  onOpen: (app: string) => void
  onLayout: () => void
  onKiosk: () => void
}) {
  const { signedOut } = useSession()
  const [open, setOpen] = useState(false)
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
                {APPS.filter((a) => a.menu).map((a) => (
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
