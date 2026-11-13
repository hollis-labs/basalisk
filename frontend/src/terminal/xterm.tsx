import { FitAddon } from "@xterm/addon-fit"
import { WebLinksAddon } from "@xterm/addon-web-links"
import { WebglAddon } from "@xterm/addon-webgl"
import { type ITheme, Terminal } from "@xterm/xterm"
import { useEffect, useRef } from "react"
import { terminal as api } from "../api/shell"

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

/** Any CSS colour (the tokens are oklch) as #rrggbb, which is what xterm parses reliably. */
function toHex(css: string, fallback: string): string {
  try {
    const canvas = document.createElement("canvas")
    canvas.width = canvas.height = 1
    const ctx = canvas.getContext("2d", { willReadFrequently: true })
    if (!ctx) return fallback
    ctx.fillStyle = fallback
    ctx.fillStyle = css
    ctx.fillRect(0, 0, 1, 1)
    const [r, g, b] = ctx.getImageData(0, 0, 1, 1).data
    return `#${[r, g, b].map((v) => v.toString(16).padStart(2, "0")).join("")}`
  } catch {
    return fallback
  }
}

function token(name: string, fallback: string): string {
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  return value ? toHex(value, fallback) : fallback
}

/** The terminal palette: the app's own background and text, with a fixed ANSI set per mode. */
export function terminalTheme(): ITheme {
  const background = token("--color-bg", "#0e1014")
  const foreground = token("--color-fg", "#e6e8ec")
  const dark =
    Number.parseInt(background.slice(1, 3), 16) +
      Number.parseInt(background.slice(3, 5), 16) +
      Number.parseInt(background.slice(5, 7), 16) <
    384
  const accent = token("--color-primary", dark ? "#7aa2f7" : "#2e5cc5")
  const ansi = dark
    ? {
        black: "#1b1f27",
        red: "#f7768e",
        green: "#9ece6a",
        yellow: "#e0af68",
        blue: "#7aa2f7",
        magenta: "#bb9af7",
        cyan: "#7dcfff",
        white: "#c0caf5",
        brightBlack: "#565f89",
        brightRed: "#ff899d",
        brightGreen: "#b9f27c",
        brightYellow: "#ffc777",
        brightBlue: "#8db0ff",
        brightMagenta: "#c7a9ff",
        brightCyan: "#a4daff",
        brightWhite: "#ffffff",
      }
    : {
        black: "#1f2328",
        red: "#c4222f",
        green: "#1f7a36",
        yellow: "#8f5e00",
        blue: "#2459c9",
        magenta: "#8250df",
        cyan: "#0f7a8a",
        white: "#6e7781",
        brightBlack: "#57606a",
        brightRed: "#a40e26",
        brightGreen: "#1a7f37",
        brightYellow: "#633c01",
        brightBlue: "#1d4fb8",
        brightMagenta: "#6639ba",
        brightCyan: "#0b6777",
        brightWhite: "#24292f",
      }
  return {
    background,
    foreground,
    cursor: accent,
    cursorAccent: background,
    selectionBackground: dark ? "#3b4a70" : "#b6ccf5",
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
      attributeFilter: ["data-mode", "data-theme", "class"],
    })

    return () => {
      closed = true
      window.clearTimeout(timer)
      observer.disconnect()
      mode.disconnect()
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
