/**
 * Completions while writing scratch: after "+", "@" or "#" at the start of a word, the
 * projects, agents and tags already in use; after "::" (or ":") at the start of a line,
 * the kinds. Up and Down move, Tab or Enter takes one, Esc puts the list away. Typing on
 * is always allowed: a name that is not in the list is simply a new one.
 */
import { type RefObject, useEffect, useMemo, useRef, useState } from "react"
import { type ScratchFacets, scratch } from "../../api/scratch"
import "./complete.css"

const KINDS: [string, string][] = [
  ["follow-up", "something not to forget"],
  ["idea", "an idea"],
  ["todo", "a thing to do"],
  ["q", "an open question"],
  ["note", "a note"],
  ["config", "tie what follows: project=… task=…"],
]

interface Option {
  /** What replaces the word being typed. */
  insert: string
  label: string
  hint?: string
}

let cached: ScratchFacets | null = null
let cachedAt = 0
async function facets(): Promise<ScratchFacets | null> {
  if (cached && Date.now() - cachedAt < 30_000) return cached
  try {
    cached = (await scratch.facets()).facets
    cachedAt = Date.now()
  } catch {
    // The list is then only what was known before.
  }
  return cached
}

/** The word being typed at the caret, if it is one that completes. */
function wordAt(
  text: string,
  caret: number,
): { start: number; sigil: string; typed: string } | null {
  const before = text.slice(0, caret)
  const line = before.slice(before.lastIndexOf("\n") + 1)
  const directive = /^(\s*)(::?)([a-z-]*)$/i.exec(line)
  if (directive)
    return {
      start: caret - directive[2].length - directive[3].length,
      sigil: "::",
      typed: directive[3].toLowerCase(),
    }
  const token = /(?:^|\s)([+@#])([\w.-]*)$/.exec(before)
  if (!token) return null
  return { start: caret - token[2].length - 1, sigil: token[1], typed: token[2].toLowerCase() }
}

export function ScratchComplete({
  box,
  text,
  setText,
}: {
  box: RefObject<HTMLTextAreaElement | null>
  text: string
  setText: (t: string) => void
}) {
  const [known, setKnown] = useState<ScratchFacets | null>(cached)
  const [caret, setCaret] = useState(0)
  const [pick, setPick] = useState(0)
  const [away, setAway] = useState("")

  // The caret, as it moves.
  useEffect(() => {
    const el = box.current
    if (!el) return
    const note = () => setCaret(el.selectionStart ?? 0)
    for (const ev of ["keyup", "click", "input", "focus"]) el.addEventListener(ev, note)
    return () => {
      for (const ev of ["keyup", "click", "input", "focus"]) el.removeEventListener(ev, note)
    }
  }, [box])

  const word = useMemo(() => wordAt(text, Math.min(caret, text.length)), [text, caret])
  useEffect(() => {
    if (word && word.sigil !== "::") void facets().then(setKnown)
  }, [word])

  const options = useMemo<Option[]>(() => {
    if (!word) return []
    if (word.sigil === "::")
      return KINDS.filter(([k]) => k.startsWith(word.typed) && k !== word.typed).map(
        ([k, hint]) => ({
          insert: `::${k} `,
          label: `::${k}`,
          hint,
        }),
      )
    const list =
      word.sigil === "+" ? known?.projects : word.sigil === "@" ? known?.agents : known?.tags
    return (list ?? [])
      .filter((c) => c.value.startsWith(word.typed) && c.value !== word.typed)
      .slice(0, 8)
      .map((c) => ({
        insert: `${word.sigil}${c.value} `,
        label: `${word.sigil}${c.value}`,
        hint: `${c.n}`,
      }))
  }, [word, known])

  const key = word ? `${word.start}:${word.sigil}` : ""
  const shown = away === key ? [] : options
  const at = Math.min(pick, Math.max(0, shown.length - 1))
  // biome-ignore lint/correctness/useExhaustiveDependencies: back to the first when the list changes
  useEffect(() => setPick(0), [shown.length, key])

  const take = (o: Option) => {
    if (!word) return
    const end = Math.min(caret, text.length)
    const next = text.slice(0, word.start) + o.insert + text.slice(end)
    setText(next)
    const pos = word.start + o.insert.length
    const el = box.current
    if (el)
      requestAnimationFrame(() => {
        el.focus()
        el.setSelectionRange(pos, pos)
        setCaret(pos)
      })
  }

  // The box's keys while the list shows, ahead of the box's own handler.
  const state = useRef({ shown, at, take, key })
  state.current = { shown, at, take, key }
  useEffect(() => {
    const el = box.current
    if (!el) return
    const onKey = (e: KeyboardEvent) => {
      const { shown, at, take, key } = state.current
      if (!shown.length || e.isComposing || e.metaKey || e.ctrlKey || e.altKey) return
      const stop = () => {
        e.preventDefault()
        e.stopImmediatePropagation()
      }
      if (e.key === "ArrowDown") {
        stop()
        setPick((at + 1) % shown.length)
      } else if (e.key === "ArrowUp") {
        stop()
        setPick((at - 1 + shown.length) % shown.length)
      } else if (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey)) {
        stop()
        take(shown[at])
      } else if (e.key === "Escape") {
        stop()
        setAway(key)
      }
    }
    // Capture: before the textarea's own React handler, which would send on Enter.
    el.addEventListener("keydown", onKey, true)
    return () => el.removeEventListener("keydown", onKey, true)
  }, [box])

  if (!shown.length) return null
  return (
    <div className="scratch-complete-anchor">
      <div className="scratch-complete" role="listbox" aria-label="Completions">
        {shown.map((o, i) => (
          <div
            key={o.label}
            role="option"
            tabIndex={-1}
            aria-selected={i === at}
            className="scratch-complete-item"
            data-on={i === at}
            onMouseDown={(e) => e.preventDefault()}
            onMouseEnter={() => setPick(i)}
            onClick={() => take(o)}
            onKeyDown={() => undefined}
          >
            <span className="scratch-complete-label">{o.label}</span>
            {o.hint && <span className="scratch-complete-hint">{o.hint}</span>}
          </div>
        ))}
      </div>
    </div>
  )
}
