import { Plus, X } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { terminal as api, type TerminalSession } from "../api/shell"
import { type DeckKeys, XTermView } from "./xterm"

const FONT_KEY = "basalisk.terminal.fontSize"
const ACTIVE_KEY = "basalisk.terminal.active"
const DEFAULT_FONT = 14

const ADOPT_EVENT = "basalisk:terminal-adopt"

/**
 * Shows a session created elsewhere (Files' "open terminal here") as the active tab: now,
 * if the deck is running, or when it next starts.
 */
export function adoptTerminal(session: TerminalSession) {
  window.localStorage.setItem(ACTIVE_KEY, session.id)
  window.dispatchEvent(new CustomEvent(ADOPT_EVENT, { detail: session }))
}

function storedFont(): number {
  const n = Number(window.localStorage.getItem(FONT_KEY))
  return n >= 9 && n <= 32 ? n : DEFAULT_FONT
}

/**
 * The terminal tabs. Sessions live in the backend, so the deck starts by adopting whatever
 * is already running (after a reload, for instance) and opens a first shell when there is
 * none. Every tab stays mounted; switching only changes which one is shown.
 */
export function TerminalDeck({ epoch, onEmpty }: { epoch: number; onEmpty: () => void }) {
  const [sessions, setSessions] = useState<TerminalSession[]>([])
  const [active, setActive] = useState("")
  const [fontSize, setFontSize] = useState(storedFont)
  const [error, setError] = useState("")
  const [loaded, setLoaded] = useState(false)
  const creating = useRef(false)

  const select = useCallback((id: string) => {
    setActive(id)
    window.localStorage.setItem(ACTIVE_KEY, id)
  }, [])

  const create = useCallback(async () => {
    if (creating.current) return
    creating.current = true
    try {
      const s = await api.create()
      setSessions((list) => [...list, s])
      select(s.id)
      setError("")
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not open a terminal.")
    } finally {
      creating.current = false
    }
  }, [select])

  useEffect(() => {
    let live = true
    void (async () => {
      try {
        const state = await api.state()
        if (!live) return
        const running = state.sessions.filter((s) => !s.exited)
        setSessions(running)
        const wanted = window.localStorage.getItem(ACTIVE_KEY) ?? ""
        if (running.length)
          select(running.find((s) => s.id === wanted)?.id ?? running[running.length - 1].id)
        setLoaded(true)
        if (!running.length) void create()
      } catch (e) {
        if (live) setError(e instanceof Error ? e.message : "Could not reach the terminal.")
      }
    })()
    return () => {
      live = false
    }
  }, [create, select])

  useEffect(() => {
    const onAdopt = (e: Event) => {
      const s = (e as CustomEvent<TerminalSession>).detail
      setSessions((list) => (list.some((x) => x.id === s.id) ? list : [...list, s]))
      select(s.id)
    }
    window.addEventListener(ADOPT_EVENT, onAdopt)
    return () => window.removeEventListener(ADOPT_EVENT, onAdopt)
  }, [select])

  const remove = useCallback(
    (id: string) => {
      setSessions((list) => {
        const at = list.findIndex((s) => s.id === id)
        const next = list.filter((s) => s.id !== id)
        setActive((current) => {
          if (current !== id) return current
          const neighbour = next[Math.min(at, next.length - 1)]?.id ?? ""
          window.localStorage.setItem(ACTIVE_KEY, neighbour)
          return neighbour
        })
        if (!next.length) onEmpty()
        return next
      })
    },
    [onEmpty],
  )

  const close = useCallback(
    (id: string) => {
      void api.close(id).catch(() => undefined)
      remove(id)
    },
    [remove],
  )

  // The shell ended on its own (exit, ctrl-d): forget the session and drop the tab.
  const exited = useCallback(
    (id: string) => {
      void api.close(id).catch(() => undefined)
      remove(id)
    },
    [remove],
  )

  const keys = useMemo<DeckKeys>(
    () => ({
      newTab: () => void create(),
      closeTab: () => active && close(active),
      selectTab: (index) => {
        const s = sessions[index]
        if (s) select(s.id)
      },
      stepTab: (delta) => {
        if (!sessions.length) return
        const at = sessions.findIndex((s) => s.id === active)
        select(sessions[(at + delta + sessions.length) % sessions.length].id)
      },
      zoom: (delta) =>
        setFontSize((size) => {
          const next = delta === 0 ? DEFAULT_FONT : Math.max(9, Math.min(32, size + delta))
          window.localStorage.setItem(FONT_KEY, String(next))
          return next
        }),
    }),
    [active, close, create, select, sessions],
  )

  return (
    <div className="terminal-deck">
      <div className="terminal-tabs" role="tablist" aria-label="Terminal sessions">
        {sessions.map((s, i) => (
          <div key={s.id} className="terminal-tab" data-active={s.id === active}>
            <button
              type="button"
              role="tab"
              aria-selected={s.id === active}
              className="terminal-tab-label"
              onClick={() => select(s.id)}
            >
              <span className="terminal-tab-index">{i + 1}</span>
              {s.title}
            </button>
            <button
              type="button"
              className="terminal-tab-close"
              aria-label={`Close terminal ${i + 1}`}
              onClick={() => close(s.id)}
            >
              <X aria-hidden="true" size={13} />
            </button>
          </div>
        ))}
        <button
          type="button"
          className="terminal-new"
          aria-label="New terminal"
          title="New terminal (⌘T)"
          onClick={() => void create()}
        >
          <Plus aria-hidden="true" size={15} />
        </button>
        <span className="terminal-hint">
          ⌘T new · ⌘W close · ⌘1-9 switch · ⌘K clear · ⌃` toggle
        </span>
      </div>
      <div className="terminal-stage">
        {sessions.map((s) => (
          <XTermView
            key={s.id}
            id={s.id}
            active={s.id === active}
            epoch={epoch}
            fontSize={fontSize}
            keys={keys}
            onExit={exited}
          />
        ))}
        {loaded && !sessions.length && (
          <div className="terminal-empty">
            <p>{error || "No terminal is open."}</p>
            <button type="button" className="terminal-empty-action" onClick={() => void create()}>
              Open a terminal
            </button>
          </div>
        )}
        {!loaded && error && (
          <div className="terminal-empty">
            <p>{error}</p>
          </div>
        )}
      </div>
    </div>
  )
}
