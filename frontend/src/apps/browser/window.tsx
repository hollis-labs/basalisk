import {
  ArrowLeft,
  ArrowRight,
  ExternalLink,
  Globe,
  Lock,
  RotateCw,
  Scissors,
  X,
} from "lucide-react"
import { type KeyboardEvent, useCallback, useEffect, useRef, useState } from "react"
import {
  BROWSER_EVENT,
  type BrowserEvent,
  type BrowserState,
  browserData,
  type Frame,
  hostOf,
  nativeBrowser,
  toURL,
  type Visit,
} from "../../api/browser"
import { SHELL_EVENT } from "../../api/shell"
import { useDesk } from "../../desktop/context"
import { bindings, chordOf } from "../../desktop/keys"
import type { FileRef } from "../../desktop/registry"
import { useDeskSettings } from "../../desktop/settings"
import { loadRoots } from "../files/browser"
import { CLIP_EVENT, clipPage, setPendingClip } from "../frag/clip"
import { FindBar } from "./find"

const HOME = "https://duckduckgo.com/"
/** The window's own shortcuts, which must work while the page has the keyboard. */
const LOCAL_CHORDS = [
  "cmd+l",
  "cmd+[",
  "cmd+]",
  "cmd+r",
  "cmd+f",
  "cmd+g",
  "shift+cmd+g",
  "cmd+=",
  "shift+cmd+=",
  "cmd+-",
  "cmd+0",
]
/** Zoom steps, as Safari has them. */
const ZOOMS = [0.5, 0.67, 0.75, 0.85, 1, 1.1, 1.25, 1.5, 1.75, 2, 2.5, 3]
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
  // Suggestions from history while the address is being typed.
  const [typed, setTyped] = useState(false)
  const [suggestions, setSuggestions] = useState<Visit[]>([])
  const [pick, setPick] = useState(-1)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [finding, setFinding] = useState(false)
  // Bumped to ask for the find field to take the keyboard, once it is on screen.
  const [findFocus, setFindFocus] = useState(0)
  const findRef = useRef<HTMLInputElement>(null)
  const [shot, setShot] = useState("")
  const [covered, setCovered] = useState(false)
  const [zoom, setZoom] = useState(1)
  // The level the next step starts from, current even between renders (two quick ⌘+).
  const zoomRef = useRef(1)
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

  // Each site keeps the zoom chosen for it.
  const host = hostOf(state.url)
  useEffect(() => {
    if (!available || !host) return
    let live = true
    void browserData.zooms().then((z) => {
      if (!live) return
      const want = z[host] ?? 1
      zoomRef.current = want
      setZoom(want)
      void send({ op: "zoom", zoom: want }).catch(() => undefined)
    })
    return () => {
      live = false
    }
  }, [available, host, send])

  const zoomTo = (next: number) => {
    zoomRef.current = next
    setZoom(next)
    void send({ op: "zoom", zoom: next }).catch(() => undefined)
    if (host) void browserData.setZoom(host, next)
  }
  const zoomStep = (dir: 1 | -1) => {
    const next =
      dir > 0
        ? (ZOOMS.find((z) => z > zoomRef.current + 0.001) ?? ZOOMS[ZOOMS.length - 1])
        : ([...ZOOMS].reverse().find((z) => z < zoomRef.current - 0.001) ?? ZOOMS[0])
    zoomTo(next)
  }

  // Every page the window settles on goes in the history (the server drops what is not http).
  const lastVisit = useRef("")
  useEffect(() => {
    if (!available || state.loading || !state.url) return
    const key = `${state.url}\u0000${state.title}`
    if (key === lastVisit.current) return
    lastVisit.current = key
    void browserData.visit(state.url, state.title)
  }, [available, state.url, state.title, state.loading])

  useEffect(() => {
    if (!editing || !typed || !address.trim()) {
      setSuggestions([])
      setPick(-1)
      return
    }
    let live = true
    const timer = window.setTimeout(() => {
      void browserData
        .history(address.trim(), 8)
        .then((list) => {
          if (!live) return
          setSuggestions(list)
          setPick(-1)
        })
        .catch(() => undefined)
    }, 80)
    return () => {
      live = false
      window.clearTimeout(timer)
    }
  }, [editing, typed, address])

  const forgetSuggestion = (url: string) => {
    void browserData.forget(url).catch(() => undefined)
    setSuggestions((list) => {
      const next = list.filter((v) => v.url !== url)
      setPick((p) => Math.min(p, next.length - 1))
      return next
    })
  }

  // Clip: extract this page and hand it to the Frag box (the button, or the frag.clip command).
  const [clipping, setClipping] = useState(false)
  const clip = useCallback(async () => {
    if (clipping) return
    setClipping(true)
    try {
      setPendingClip(await clipPage(winId))
      desk.toggleOverlay("frag", true)
    } catch (e) {
      flash(e instanceof Error ? e.message : "Could not clip this page.")
    } finally {
      setClipping(false)
    }
  }, [clipping, desk, flash, winId])
  useEffect(() => {
    const on = (e: Event) => {
      if ((e as CustomEvent<string>).detail === winId) void clip()
    }
    window.addEventListener(CLIP_EVENT, on)
    return () => window.removeEventListener(CLIP_EVENT, on)
  }, [clip, winId])

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
    // Not at once: when the native view is told to let go (the agent panel, ⌘J), WebKit
    // focuses this window's element again on its own, and passing that on would take the
    // keyboard straight back from whatever is being focused next. So only if the window
    // still has the focus a moment later.
    // (A timer, not animation frames, which do not run while the app is in the background.)
    let timer = 0
    const onFocus = (e: FocusEvent) => {
      if (e.target !== win) return
      window.clearTimeout(timer)
      timer = window.setTimeout(() => {
        if (document.activeElement === win) void send({ op: "focus" }).catch(() => undefined)
      }, 60)
    }
    win.addEventListener("focus", onFocus)
    return () => {
      window.clearTimeout(timer)
      win.removeEventListener("focus", onFocus)
    }
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
      case "cmd+=":
      case "shift+cmd+=":
        zoomStep(1)
        return true
      case "cmd+-":
        zoomStep(-1)
        return true
      case "cmd+0":
        zoomTo(1)
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
            aria-autocomplete="list"
            aria-controls={suggestions.length ? `${winId}-suggest` : undefined}
            onFocus={(e) => {
              setEditing(true)
              setTyped(false)
              e.currentTarget.select()
            }}
            onBlur={() => setEditing(false)}
            onChange={(e) => {
              setAddress(e.target.value)
              setTyped(true)
            }}
            onKeyDown={(e) => {
              if (e.key === "ArrowDown" && suggestions.length) {
                e.preventDefault()
                setPick((p) => Math.min(suggestions.length - 1, p + 1))
              } else if (e.key === "ArrowUp" && suggestions.length) {
                e.preventDefault()
                setPick((p) => Math.max(-1, p - 1))
              } else if (e.key === "Backspace" && e.shiftKey && pick >= 0 && suggestions[pick]) {
                // ⇧⌫ forgets the chosen page.
                e.preventDefault()
                forgetSuggestion(suggestions[pick].url)
              } else if (e.key === "Enter" && pick >= 0 && suggestions[pick]) {
                e.preventDefault()
                navigate(suggestions[pick].url)
              } else if (e.key === "Escape") {
                if (suggestions.length) {
                  setSuggestions([])
                  setTyped(false)
                  return
                }
                setAddress(state.url)
                e.currentTarget.blur()
                void send({ op: "focus" })
              }
            }}
          />
          {editing && suggestions.length > 0 && (
            <div className="browser-suggest" id={`${winId}-suggest`} role="listbox">
              {suggestions.map((v, i) => (
                <button
                  key={v.url}
                  type="button"
                  role="option"
                  aria-selected={i === pick}
                  data-selected={i === pick}
                  tabIndex={-1}
                  // Before the field blurs, so the click is not lost.
                  onMouseDown={(e) => {
                    e.preventDefault()
                    navigate(v.url)
                  }}
                >
                  <span className="browser-suggest-title">{v.title || v.url}</span>
                  <span className="browser-suggest-url">{v.url}</span>
                  <span
                    className="browser-suggest-forget"
                    title="Forget this page (⇧⌫)"
                    aria-hidden="true"
                    onMouseDown={(e) => {
                      e.preventDefault()
                      e.stopPropagation()
                      forgetSuggestion(v.url)
                    }}
                  >
                    <X size={12} strokeWidth={1.6} />
                  </span>
                </button>
              ))}
            </div>
          )}
        </form>
        {zoom !== 1 && (
          <button
            type="button"
            className="browser-zoom"
            title="Actual size (⌘0)"
            onClick={() => zoomTo(1)}
          >
            {Math.round(zoom * 100)}%
          </button>
        )}
        {notice && <span className="browser-notice">{notice}</span>}
        {/^https?:/.test(state.url) && (
          <button
            type="button"
            className="files-icon"
            title="Clip this page to Frag (⌥⇧C)"
            disabled={clipping || state.loading}
            onClick={() => void clip()}
          >
            <Scissors size={15} strokeWidth={1.6} aria-label="Clip this page" />
          </button>
        )}
        {/^https?:/.test(state.url) && (
          <button
            type="button"
            className="files-icon"
            title="Open in the default browser (for sites that need extensions or DRM)"
            onClick={() => void send({ op: "external" }).catch((e: Error) => flash(e.message))}
          >
            <ExternalLink size={15} strokeWidth={1.6} aria-label="Open in the default browser" />
          </button>
        )}
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
