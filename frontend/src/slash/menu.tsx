/**
 * Slash commands in the agent's box: when what is typed is one word starting with "/" that
 * begins a known command, the commands are offered above the box. Up and Down move, Tab or
 * Enter takes one, Esc puts the list away. A path ("/Users/…") begins no command, so it is
 * never in the way.
 *
 * The commands are the router's (each rule's help line, GET /api/agent/automation) plus the
 * panel's own /new and /help. Keys are taken on the box itself, before the panel's handler,
 * and only while the list is showing.
 */
import { type RefObject, useEffect, useMemo, useRef, useState } from "react"
import { request } from "../api/account"
import "./menu.css"

export interface SlashCommand {
  /** "/open" */
  name: string
  /** "<app>", when it takes something after it. */
  arg: string
  about: string
}

const OWN: SlashCommand[] = [
  { name: "/new", arg: "", about: "Start a new conversation." },
  { name: "/help", arg: "", about: "List what runs without the model." },
]

/** "/open <app>: open an app…" as a command, or null. */
export function parseHelp(help: string): SlashCommand | null {
  const m = /^(\/[A-Za-z0-9][\w-]*)((?:\s+[^:\s][^:]*)?)\s*:\s*(.*)$/s.exec(help.trim())
  if (!m) return null
  return { name: m[1].toLowerCase(), arg: m[2].trim(), about: m[3].trim() }
}

let loaded: Promise<SlashCommand[]> | null = null
function commands(): Promise<SlashCommand[]> {
  loaded ??= request<{ rules?: { help?: string }[] }>("agent/automation")
    .then((a) => (a.rules ?? []).map((r) => (r.help ? parseHelp(r.help) : null)))
    .catch(() => [] as (SlashCommand | null)[])
    .then((found) => {
      const out = new Map<string, SlashCommand>()
      for (const c of [...found, ...OWN]) if (c && !out.has(c.name)) out.set(c.name, c)
      return [...out.values()]
    })
  return loaded
}

/** The commands the text could be the start of: only while it is one word starting with "/". */
export function matching(text: string, all: SlashCommand[]): SlashCommand[] {
  if (!text.startsWith("/") || /\s/.test(text)) return []
  const t = text.toLowerCase()
  return all.filter((c) => c.name.startsWith(t))
}

export function SlashMenu({
  box,
  text,
  setText,
}: {
  box: RefObject<HTMLTextAreaElement | null>
  text: string
  setText: (t: string) => void
}) {
  const [all, setAll] = useState<SlashCommand[]>([])
  const [pick, setPick] = useState(0)
  // Put away with Esc: stays away until the text stops being a command.
  const [away, setAway] = useState(false)
  useEffect(() => {
    if (text.startsWith("/") && !all.length) void commands().then(setAll)
  }, [text, all.length])
  useEffect(() => {
    if (!text.startsWith("/") || /\s/.test(text)) setAway(false)
  }, [text])

  const shown = useMemo(() => (away ? [] : matching(text, all)), [away, text, all])
  const at = Math.min(pick, Math.max(0, shown.length - 1))
  // biome-ignore lint/correctness/useExhaustiveDependencies: back to the first when the list changes
  useEffect(() => setPick(0), [shown.length, text])

  const take = (c: SlashCommand) => {
    const next = c.arg ? `${c.name} ` : c.name
    setText(next)
    const el = box.current
    if (el)
      requestAnimationFrame(() => {
        el.focus()
        el.setSelectionRange(next.length, next.length)
      })
  }

  // The box's keys, while the list is showing. Kept in a ref so the listener is added once.
  const state = useRef({ shown, at, text, take })
  state.current = { shown, at, text, take }
  useEffect(() => {
    const el = box.current
    if (!el) return
    const onKey = (e: KeyboardEvent) => {
      const { shown, at, text, take } = state.current
      if (!shown.length || e.isComposing || e.metaKey || e.ctrlKey || e.altKey) return
      const stop = () => {
        e.preventDefault()
        e.stopPropagation()
      }
      switch (e.key) {
        case "ArrowDown":
          stop()
          setPick((at + 1) % shown.length)
          break
        case "ArrowUp":
          stop()
          setPick((at - 1 + shown.length) % shown.length)
          break
        case "Tab":
          if (e.shiftKey) return
          stop()
          take(shown[at])
          break
        case "Enter": {
          if (e.shiftKey) return
          // Typed in full and needing nothing after it: Enter sends it, as always.
          const c = shown[at]
          if (c.name === text.toLowerCase() && !c.arg) return
          stop()
          take(c)
          break
        }
        case "Escape":
          stop()
          setAway(true)
          break
      }
    }
    el.addEventListener("keydown", onKey)
    return () => el.removeEventListener("keydown", onKey)
  }, [box])

  if (!shown.length) return null
  return (
    <div className="slash-anchor">
      <div className="slash-menu" role="listbox" aria-label="Commands">
        {shown.map((c, i) => (
          <div
            key={c.name}
            role="option"
            tabIndex={-1}
            aria-selected={i === at}
            className="slash-item"
            data-on={i === at}
            // Keep the keyboard in the box.
            onMouseDown={(e) => e.preventDefault()}
            onMouseEnter={() => setPick(i)}
            onClick={() => take(c)}
            onKeyDown={() => undefined}
          >
            <span className="slash-name">
              {c.name}
              {c.arg && <span className="slash-arg"> {c.arg}</span>}
            </span>
            <span className="slash-about">{c.about}</span>
          </div>
        ))}
      </div>
    </div>
  )
}
