import { FitAddon } from "@xterm/addon-fit"
import { WebLinksAddon } from "@xterm/addon-web-links"
import { WebglAddon } from "@xterm/addon-webgl"
import { type ITheme, Terminal } from "@xterm/xterm"
import { useEffect, useRef } from "react"
import { terminal as api } from "../api/shell"
import { citeLinkProvider } from "../cite/terminal"
import { currentTheme, mix } from "../desktop/themes"

/** What a terminal asks of the deck that holds it. */
export interface DeckKeys {
  newTab: () => void
  closeTab: () => void
  selectTab: (index: number) => void
  stepTab: (delta: number) => void
  zoom: (delta: number) => void
}

const FONT_FAMILY =
  '"Meslo LG L DZ for Powerline", "MesloLGS NF", "Hack Nerd Font Mono", "JetBrainsMono Nerd Font", ui-monospace, SFMono-Regular, Menlo, monospace'

/** The terminal palette: the active desktop theme's. */
export function terminalTheme(): ITheme {
  const t = currentTheme()
  const names = [
    "black",
    "red",
    "green",
    "yellow",
    "blue",
    "magenta",
    "cyan",
    "white",
    "brightBlack",
    "brightRed",
    "brightGreen",
    "brightYellow",
    "brightBlue",
    "brightMagenta",
    "brightCyan",
    "brightWhite",
  ] as const
  const ansi = Object.fromEntries(names.map((name, i) => [name, t.ansi[i]]))
  return {
    background: t.bg,
    foreground: t.fg,
    cursor: t.accent,
    cursorAccent: t.bg,
    selectionBackground: mix(t.selection, t.accent, 0.25),
    ...ansi,
  }
}

interface Props {
  id: string
  active: boolean
  /** Bumped by the host each time the deck is shown again, to refit and refocus. */
  epoch: number
  fontSize: number
  keys: DeckKeys
  onExit: (id: string, code: number) => void
}

/**
 * One terminal: an xterm bound to a session's socket. Binary frames are raw bytes both ways;
 * text frames are JSON control messages (resize out; state and exit in). The socket
 * reconnects by itself, and every (re)attach starts with a replay of recent output.
 */
export function XTermView({ id, active, epoch, fontSize, keys, onExit }: Props) {
  const host = useRef<HTMLDivElement>(null)
  const term = useRef<Terminal | null>(null)
  const fit = useRef<FitAddon | null>(null)
  const keysRef = useRef(keys)
  keysRef.current = keys
  const exitRef = useRef(onExit)
  exitRef.current = onExit

  // biome-ignore lint/correctness/useExhaustiveDependencies: the terminal is built once per session; font size and callbacks are applied through refs and the effects below
  useEffect(() => {
    const el = host.current
    if (!el) return
    const t = new Terminal({
      cursorBlink: true,
      cursorStyle: "bar",
      fontFamily: FONT_FAMILY,
      fontSize,
      lineHeight: 1.15,
      scrollback: 20000,
      allowProposedApi: true,
      macOptionClickForcesSelection: true,
      theme: terminalTheme(),
    })
    const f = new FitAddon()
    t.loadAddon(f)
    t.loadAddon(new WebLinksAddon((_event, uri) => window.open(uri, "_blank", "noopener")))
    t.open(el)
    const citeLinks = t.registerLinkProvider(citeLinkProvider(t))
    try {
      const gl = new WebglAddon()
      gl.onContextLoss(() => gl.dispose())
      t.loadAddon(gl)
    } catch {
      // No WebGL: the DOM renderer is used.
    }
    term.current = t
    fit.current = f

    let ws: WebSocket | null = null
    let closed = false
    let exited = false
    let retry = 0
    let timer = 0
    const encoder = new TextEncoder()
    const send = (data: string | Uint8Array) => {
      if (ws && ws.readyState === WebSocket.OPEN) ws.send(data)
    }
    const sendSize = () => send(JSON.stringify({ type: "resize", cols: t.cols, rows: t.rows }))
    const refit = () => {
      if (el.offsetWidth > 0 && el.offsetHeight > 0) {
        try {
          f.fit()
        } catch {
          // The renderer is not ready yet; the next resize refits.
        }
      }
    }

    const connect = () => {
      if (closed || exited) return
      ws = new WebSocket(api.socketURL(id))
      ws.binaryType = "arraybuffer"
      let fresh = true
      ws.onopen = () => {
        refit()
        sendSize()
      }
      ws.onmessage = (ev) => {
        if (typeof ev.data !== "string") {
          if (fresh) {
            // What arrives first is the replay: start from a clean screen.
            t.reset()
            fresh = false
          }
          t.write(new Uint8Array(ev.data as ArrayBuffer))
          return
        }
        try {
          const msg = JSON.parse(ev.data) as { type: string; code?: number }
          if (msg.type === "state") {
            fresh = false
            retry = 0
          }
          if (msg.type === "exit") {
            exited = true
            exitRef.current(id, msg.code ?? 0)
          }
        } catch {
          // Not a control message we know.
        }
      }
      ws.onclose = (ev) => {
        // 1008: the server no longer has this session.
        if (ev.code === 1008 && !exited) {
          exited = true
          exitRef.current(id, -1)
          return
        }
        if (closed || exited) return
        retry = Math.min(retry + 1, 6)
        timer = window.setTimeout(connect, 250 * 2 ** (retry - 1))
      }
    }
    connect()

    const onData = t.onData((d) => send(encoder.encode(d)))
    const onBinary = t.onBinary((d) => send(Uint8Array.from(d, (c) => c.charCodeAt(0))))
    const onResize = t.onResize(sendSize)

    // Keys the deck owns, and the line-editing shortcuts a Mac terminal is expected to have.
    t.attachCustomKeyEventHandler((e) => {
      if (e.type !== "keydown") return true
      const k = keysRef.current
      const take = (fn: () => void) => {
        e.preventDefault()
        e.stopPropagation()
        fn()
        return false
      }
      const only = (mods: { meta?: boolean; alt?: boolean; shift?: boolean; ctrl?: boolean }) =>
        e.metaKey === !!mods.meta &&
        e.altKey === !!mods.alt &&
        e.shiftKey === !!mods.shift &&
        e.ctrlKey === !!mods.ctrl
      if (only({ meta: true })) {
        if (e.key === "t") return take(k.newTab)
        if (e.key === "w") return take(k.closeTab)
        if (e.key === "k") return take(() => t.clear())
        if (e.key === "=" || e.key === "+") return take(() => k.zoom(1))
        if (e.key === "-") return take(() => k.zoom(-1))
        if (e.key === "0") return take(() => k.zoom(0))
        if (/^[1-9]$/.test(e.key)) return take(() => k.selectTab(Number(e.key) - 1))
        if (e.key === "ArrowLeft") return take(() => send(encoder.encode("\x01")))
        if (e.key === "ArrowRight") return take(() => send(encoder.encode("\x05")))
        if (e.key === "Backspace") return take(() => send(encoder.encode("\x15")))
      }
      if (only({ meta: true, shift: true })) {
        if (e.key === "[" || e.key === "{") return take(() => k.stepTab(-1))
        if (e.key === "]" || e.key === "}") return take(() => k.stepTab(1))
      }
      if (only({ alt: true })) {
        if (e.key === "ArrowLeft") return take(() => send(encoder.encode("\x1bb")))
        if (e.key === "ArrowRight") return take(() => send(encoder.encode("\x1bf")))
      }
      if (only({ shift: true }) && e.key === "Enter")
        return take(() => send(encoder.encode("\x1b\r")))
      return true
    })

    const observer = new ResizeObserver(() => refit())
    observer.observe(el)
    const mode = new MutationObserver(() => {
      t.options.theme = terminalTheme()
    })
    mode.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-desk-theme"],
    })

    return () => {
      closed = true
      window.clearTimeout(timer)
      observer.disconnect()
      mode.disconnect()
      citeLinks.dispose()
      onData.dispose()
      onBinary.dispose()
      onResize.dispose()
      ws?.close()
      t.dispose()
      term.current = null
      fit.current = null
    }
  }, [id])

  useEffect(() => {
    const t = term.current
    if (!t) return
    t.options.fontSize = fontSize
    try {
      fit.current?.fit()
    } catch {
      // Hidden: refits when shown.
    }
  }, [fontSize])

  // biome-ignore lint/correctness/useExhaustiveDependencies: epoch is a signal, not a value read here
  useEffect(() => {
    if (!active) return
    const frame = window.requestAnimationFrame(() => {
      try {
        fit.current?.fit()
      } catch {
        // Not laid out yet.
      }
      term.current?.focus()
    })
    return () => window.cancelAnimationFrame(frame)
  }, [active, epoch])

  return <div ref={host} className="xterm-host" data-active={active} />
}
