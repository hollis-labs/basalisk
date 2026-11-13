import { ArrowLeft, ArrowRight, Globe, Lock, RotateCw, X } from "lucide-react"
import { type KeyboardEvent, useCallback, useEffect, useRef, useState } from "react"
import {
  BROWSER_EVENT,
  type BrowserEvent,
  type BrowserState,
  type Frame,
  nativeBrowser,
  toURL,
} from "../../api/browser"
import { SHELL_EVENT } from "../../api/shell"
import { useDesk } from "../../desktop/context"
import { bindings, chordOf } from "../../desktop/keys"
import type { FileRef } from "../../desktop/registry"
import { useDeskSettings } from "../../desktop/settings"
import { loadRoots } from "../files/browser"
import { FindBar } from "./find"

const HOME = "https://duckduckgo.com/"
/** The window's own shortcuts, which must work while the page has the keyboard. */
const LOCAL_CHORDS = ["cmd+l", "cmd+[", "cmd+]", "cmd+r", "cmd+f", "cmd+g", "shift+cmd+g"]
/** How often a visible view refreshes the picture shown while something covers it. */
const SNAPSHOT_MS = 3000

/**
 * Is the slot the topmost thing on the page over its whole area? The native view draws
 * above the DOM, so when a menu, the palette, a card or another window covers any of the
 * slot, the view must step aside. A grid of hit tests catches all of them without the
 * desktop having to announce each overlay.
 */
function uncovered(slot: HTMLElement, r: DOMRect): boolean {
  const xs = [0.02, 0.35, 0.65, 0.98]
  const ys = [0.02, 0.35, 0.65, 0.98]
  for (const fx of xs)
    for (const fy of ys) {
      const el = document.elementFromPoint(r.left + r.width * fx, r.top + r.height * fy)
      if (el !== slot && !slot.contains(el)) return false
    }
  return true
}

/** A file in a local root as a file:// URL; remote roots cannot be opened this way. */
export async function localFileURL(ref: FileRef): Promise<string> {
  const root = (await loadRoots()).find((r) => r.id === ref.root)
  if (!root) throw new Error("That folder is not available.")
  if (root.kind !== "local") throw new Error("Only files on this Mac open in the browser.")
  const full = `${root.path.replace(/\/$/, "")}/${ref.path}`
  return `file://${full.split("/").map(encodeURIComponent).join("/")}`
}

export function BrowserWindow({ winId, args }: { winId: string; args: Record<string, unknown> }) {
  const desk = useDesk()
  const available = nativeBrowser.available()
  const fileRef = args.file as FileRef | undefined
  // Where the view starts: the url it was opened with, a file Files handed over (resolved
  // to a file:// URL once the roots are known), or the home page.
  const [start, setStart] = useState<string | null>(() =>
    typeof args.url === "string" && args.url ? args.url : fileRef ? null : HOME,
  )
  const slotRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const [state, setState] = useState<BrowserState>({
    id: winId,
    url: start ?? "",
    title: "",
    loading: true,
    progress: 0,
    back: false,
    forward: false,
    visible: false,
  })
  const [address, setAddress] = useState(start ?? "")
  const [editing, setEditing] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [finding, setFinding] = useState(false)
  // Bumped to ask for the find field to take the keyboard, once it is on screen.
  const [findFocus, setFindFocus] = useState(0)
  const findRef = useRef<HTMLInputElement>(null)
  const [shot, setShot] = useState("")
  const [covered, setCovered] = useState(false)
  const lastFrame = useRef("")
  const keys = useDeskSettings().keys

  const send = useCallback(
    (msg: Record<string, unknown>) => nativeBrowser.send({ id: winId, ...msg }),
    [winId],
  )
  const flash = useCallback((msg: string) => {
    setNotice(msg)
    window.setTimeout(() => setNotice((n) => (n === msg ? "" : n)), 3500)
  }, [])

  useEffect(() => {
    if (start !== null || !fileRef) return
    void localFileURL(fileRef)
      .then((u) => setStart(u))
      .catch((e: Error) => {
        setError(e.message)
        setStart("about:blank")
      })
  }, [start, fileRef])

  // ---- The view lives as long as the window. ----
  useEffect(() => {
    if (!available || start === null) return
    void send({ op: "open", url: start }).catch((e: Error) => setError(e.message))
    return () => {
      void send({ op: "close" }).catch(() => undefined)
    }
    // The view is created once; later navigation goes through "navigate".
  }, [available, send, start])

  // ---- Follow the slot: position, size and whether anything covers it, every frame. ----
  useEffect(() => {
    if (!available) return
    let raf = 0
    const tick = () => {
      raf = requestAnimationFrame(tick)
      const slot = slotRef.current
      if (!slot) return
      const r = slot.getBoundingClientRect()
      // The desktop says when a window is moving or something it draws is over this one;
      // the hit tests catch anything else in the DOM.
      const g = desk.geometry(winId)
      const shown = r.width > 1 && r.height > 1 && !document.hidden && g?.visible !== false
      const clear = shown && !g?.covered && uncovered(slot, r)
      const f: Frame = { x: r.left, y: r.top, w: r.width, h: r.height, visible: clear }
      const key = `${Math.round(f.x)},${Math.round(f.y)},${Math.round(f.w)},${Math.round(f.h)},${clear}`
      if (key === lastFrame.current) return
      lastFrame.current = key
      setCovered(shown && !clear)
      void send({ op: "frame", ...f }).catch(() => undefined)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [available, desk, send, winId])

  // ---- A picture to show while the view steps aside. ----
  const refreshShot = useCallback(() => {
    void send({ op: "snapshot" })
      .then((s) => typeof s === "string" && setShot(s))
      .catch(() => undefined)
  }, [send])
  useEffect(() => {
    if (!available || covered) return
    const timer = window.setInterval(refreshShot, SNAPSHOT_MS)
    return () => window.clearInterval(timer)
  }, [available, covered, refreshShot])
  useEffect(() => {
    if (available && !state.loading) refreshShot()
  }, [available, state.loading, refreshShot])

  const runLocalRef = useRef<(chord: string) => boolean>(() => false)
  const runLocal = (chord: string) => runLocalRef.current(chord)

  // ---- Shortcuts while the page has the keyboard: the native layer hands these back. ----
  useEffect(() => {
    if (!available) return
    const chords = [...bindings(keys).keys(), ...LOCAL_CHORDS]
    void nativeBrowser.send({ op: "chords", chords }).catch(() => undefined)
  }, [available, keys])

  // ---- What the view tells us. ----
  useEffect(() => {
    const on = (e: Event) => {
      const ev = (e as CustomEvent<BrowserEvent>).detail
      if (!ev || ev.id !== winId) return
      switch (ev.kind) {
        case "state":
          setState(ev)
          if (ev.loading) setError("")
          break
        case "error":
          setError(`${ev.message}`)
          break
        case "crashed":
          setError("This page stopped working.")
          break
        case "open":
          if (ev.url) desk.open("browser", { args: { url: ev.url } })
          break
        case "close":
          desk.close(winId)
          break
        case "focus":
          desk.wm({ type: "focus", id: winId })
          break
        case "chord":
          if (!runLocalRef.current(ev.chord))
            // The desktop runs a chord it is told the native layer swallowed.
            window.dispatchEvent(
              new CustomEvent(SHELL_EVENT, { detail: { kind: "swallowed", detail: ev.chord } }),
            )
          break
        case "download":
          flash(
            ev.state === "started"
              ? `Downloading ${ev.file.split("/").pop()}…`
              : ev.state === "done"
                ? `Saved to ${ev.file}`
                : `Download failed: ${ev.message ?? ""}`,
          )
          break
      }
    }
    window.addEventListener(BROWSER_EVENT, on)
    return () => window.removeEventListener(BROWSER_EVENT, on)
  }, [desk, flash, winId])

  // The window's args follow the page, so a restored desktop reopens it where it was.
  useEffect(() => {
    if (!state.url || state.loading || state.url === args.url) return
    desk.setArgs(winId, { url: state.url })
  }, [desk, winId, state.url, state.loading, args.url])

  // The address field shows where the page is, unless the user is typing.
  useEffect(() => {
    if (!editing && state.url) setAddress(state.url)
  }, [editing, state.url])

  useEffect(() => {
    let host = ""
    try {
      host = new URL(state.url).host
    } catch {
      host = ""
    }
    desk.setTitle(winId, state.title || host || "Browser")
  }, [desk, winId, state.title, state.url])

  // Focusing the window gives the keyboard to the page in it.
  useEffect(() => {
    if (!available) return
    const win = slotRef.current?.closest<HTMLElement>("[data-win]")
    if (!win) return
    const onFocus = (e: FocusEvent) => {
      if (e.target === win) void send({ op: "focus" }).catch(() => undefined)
    }
    win.addEventListener("focus", onFocus)
    return () => win.removeEventListener("focus", onFocus)
  }, [available, send])

  const navigate = (input: string) => {
    const url = toURL(input)
    if (!url) return
    setEditing(false)
    setError("")
    setAddress(url)
    void send({ op: "navigate", url })
      .then(() => send({ op: "focus" }))
      .catch((e: Error) => setError(e.message))
  }

  // A DOM field takes the keyboard only once the native view gives it back.
  const focusField = (ref: { current: HTMLInputElement | null }) => {
    void send({ op: "blur" })
      .catch(() => undefined)
      .finally(() => {
        ref.current?.focus()
        ref.current?.select()
      })
  }
  const find = useCallback(
    (query: string, backwards: boolean) =>
      send({ op: "find", query, backwards })
        .then((r) => !!(r as { found?: boolean })?.found)
        .catch(() => false),
    [send],
  )
  const closeFind = () => {
    setFinding(false)
    void send({ op: "find-clear" })
      .then(() => send({ op: "focus" }))
      .catch(() => undefined)
  }
  const findStep = (backwards: boolean) => {
    const q = findRef.current?.value
    if (!finding || !q) {
      setFinding(true)
      setFindFocus((n) => n + 1)
      return
    }
    void find(q, backwards)
  }

  // biome-ignore lint/correctness/useExhaustiveDependencies: runs when focus is asked for
  useEffect(() => {
    if (finding && findFocus) focusField(findRef)
  }, [findFocus])

  runLocalRef.current = (chord: string) => {
    switch (chord) {
      case "cmd+l":
        focusField(inputRef)
        return true
      case "cmd+f":
        setFinding(true)
        setFindFocus((n) => n + 1)
        return true
      case "cmd+g":
        findStep(false)
        return true
      case "shift+cmd+g":
        findStep(true)
        return true
      case "cmd+[":
        void send({ op: "back" })
        return true
      case "cmd+]":
        void send({ op: "forward" })
        return true
      case "cmd+r":
        void send({ op: "reload" })
        return true
    }
    return false
  }

  const onChromeKey = (e: KeyboardEvent<HTMLDivElement>) => {
    const chord = chordOf(e.nativeEvent)
    if (chord && runLocal(chord)) {
      e.preventDefault()
      e.stopPropagation()
    }
  }

  if (!available)
    return (
      <div className="browser-off">
        <Globe aria-hidden="true" size={22} strokeWidth={1.6} />
        <p>The browser runs in the desktop app.</p>
        {typeof args.url === "string" && args.url && (
          <a className="desk-button" href={args.url} target="_blank" rel="noreferrer">
            Open {args.url} in a tab
          </a>
        )}
      </div>
    )

  const secure = state.url.startsWith("https:")
  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: browser shortcuts for the chrome
    <div className="browser" onKeyDown={onChromeKey}>
      <div className="browser-bar">
        <button
          type="button"
          className="files-icon"
          title="Back (⌘[)"
          disabled={!state.back}
          onClick={() => void send({ op: "back" })}
        >
          <ArrowLeft size={15} strokeWidth={1.6} aria-label="Back" />
        </button>
        <button
          type="button"
          className="files-icon"
          title="Forward (⌘])"
          disabled={!state.forward}
          onClick={() => void send({ op: "forward" })}
        >
          <ArrowRight size={15} strokeWidth={1.6} aria-label="Forward" />
        </button>
        <button
          type="button"
          className="files-icon"
          title={state.loading ? "Stop" : "Reload (⌘R)"}
          onClick={() => void send({ op: state.loading ? "stop" : "reload" })}
        >
          {state.loading ? (
            <X size={15} strokeWidth={1.6} aria-label="Stop" />
          ) : (
            <RotateCw size={15} strokeWidth={1.6} aria-label="Reload" />
          )}
        </button>
        <form
          className="browser-address"
          onSubmit={(e) => {
            e.preventDefault()
            navigate(address)
          }}
        >
          {secure ? (
            <Lock aria-hidden="true" size={12} strokeWidth={1.6} />
          ) : (
            <Globe aria-hidden="true" size={12} strokeWidth={1.6} />
          )}
          <input
            ref={inputRef}
            value={address}
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            aria-label="Address"
            placeholder="Search or enter an address"
            onFocus={(e) => {
              setEditing(true)
              e.currentTarget.select()
            }}
            onBlur={() => setEditing(false)}
            onChange={(e) => setAddress(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") {
                setAddress(state.url)
                e.currentTarget.blur()
                void send({ op: "focus" })
              }
            }}
          />
        </form>
        {notice && <span className="browser-notice">{notice}</span>}
      </div>
      {finding && <FindBar inputRef={findRef} find={find} onClose={closeFind} />}
      <div
        className="browser-progress"
        data-on={state.loading}
        style={{ transform: `scaleX(${state.loading ? Math.max(0.08, state.progress) : 1})` }}
      />
      <div className="browser-stage">
        <div ref={slotRef} className="browser-slot">
          {covered && shot && <img className="browser-shot" src={shot} alt="" />}
        </div>
        {error && (
          <div className="browser-error">
            <p>{error}</p>
            <button
              type="button"
              className="desk-button"
              onClick={() => void send({ op: "reload" })}
            >
              Try again
            </button>
          </div>
        )}
      </div>
    </div>
  )
}
