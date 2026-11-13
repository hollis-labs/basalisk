import { ChevronDown, ChevronUp, X } from "lucide-react"
import { type RefObject, useEffect, useRef, useState } from "react"

/**
 * Find in a note (⌘F): in the editor it selects each match in the text; in the rendered view it
 * highlights them (CSS Custom Highlight API) and scrolls to the current one. Enter and ⇧Enter
 * step, Esc closes (in the editor, leaving the current match selected).
 */
export function NotesFind({
  editor,
  view,
  onClose,
}: {
  editor?: RefObject<HTMLTextAreaElement | null>
  view?: RefObject<HTMLElement | null>
  onClose: () => void
}) {
  const [q, setQ] = useState("")
  const [count, setCount] = useState(0)
  const [at, setAt] = useState(0)
  const input = useRef<HTMLInputElement>(null)
  const matches = useRef<{ start: number; end: number }[] | Range[]>([])

  useEffect(() => {
    input.current?.focus()
    input.current?.select()
  }, [])

  // Find every match when the query changes.
  useEffect(() => {
    const needle = q.toLowerCase()
    if (editor?.current) {
      const text = editor.current.value.toLowerCase()
      const found: { start: number; end: number }[] = []
      if (needle)
        for (
          let i = text.indexOf(needle);
          i >= 0 && found.length < 2000;
          i = text.indexOf(needle, i + needle.length)
        )
          found.push({ start: i, end: i + needle.length })
      matches.current = found
    } else if (view?.current) {
      const found: Range[] = []
      if (needle) {
        const walker = document.createTreeWalker(view.current, NodeFilter.SHOW_TEXT)
        for (let n = walker.nextNode(); n && found.length < 2000; n = walker.nextNode()) {
          const t = (n.textContent ?? "").toLowerCase()
          for (let i = t.indexOf(needle); i >= 0; i = t.indexOf(needle, i + needle.length)) {
            const r = document.createRange()
            r.setStart(n, i)
            r.setEnd(n, i + needle.length)
            found.push(r)
          }
        }
      }
      matches.current = found
      highlightAll(found)
    }
    setCount(matches.current.length)
    setAt(0)
  }, [q, editor, view])

  // Show the current match (count: a new search with the same index still moves to its first match).
  // biome-ignore lint/correctness/useExhaustiveDependencies: count re-runs it for a new search
  useEffect(() => {
    const m = matches.current[at]
    if (!m) {
      highlightCurrent(null)
      return
    }
    if (m instanceof Range) {
      highlightCurrent(m)
      const box = view?.current
      const scroller = box?.closest<HTMLElement>(".notes-scroll, .notes-preview") ?? box
      if (scroller) {
        const r = m.getBoundingClientRect()
        const s = scroller.getBoundingClientRect()
        if (r.top < s.top || r.bottom > s.bottom) scroller.scrollTop += r.top - s.top - s.height / 3
      }
    } else if (editor?.current) {
      const el = editor.current
      el.setSelectionRange(m.start, m.end)
      const line = el.value.slice(0, m.start).split("\n").length - 1
      const lh = Number.parseFloat(getComputedStyle(el).lineHeight) || 18
      el.scrollTop = Math.max(0, line * lh - el.clientHeight / 3)
    }
  }, [at, count, editor, view])

  useEffect(() => () => highlightAll([]), [])

  const step = (d: number) => count && setAt((i) => (i + d + count) % count)
  const close = () => {
    highlightAll([])
    const m = matches.current[at]
    onClose()
    if (editor?.current && m && !(m instanceof Range)) {
      editor.current.focus()
      editor.current.setSelectionRange(m.start, m.end)
    } else view?.current?.closest<HTMLElement>(".notes-scroll")?.focus()
  }
  return (
    <div className="notes-find">
      <input
        ref={input}
        value={q}
        spellCheck={false}
        aria-label="Find in note"
        placeholder="Find in note"
        onChange={(e) => setQ(e.target.value)}
        onKeyDown={(e) => {
          e.stopPropagation()
          if (e.key === "Enter") {
            e.preventDefault()
            step(e.shiftKey ? -1 : 1)
          } else if (e.key === "Escape") {
            e.preventDefault()
            close()
          } else if (e.metaKey && e.code === "KeyG") {
            e.preventDefault()
            step(e.shiftKey ? -1 : 1)
          }
        }}
      />
      <span className="notes-find-count">
        {q ? (count ? `${at + 1} of ${count}` : "No matches") : ""}
      </span>
      <button type="button" title="Previous (⇧↵)" onClick={() => step(-1)}>
        <ChevronUp aria-label="Previous match" size={14} strokeWidth={1.6} />
      </button>
      <button type="button" title="Next (↵)" onClick={() => step(1)}>
        <ChevronDown aria-label="Next match" size={14} strokeWidth={1.6} />
      </button>
      <button type="button" title="Close (Esc)" onClick={close}>
        <X aria-label="Close find" size={14} strokeWidth={1.6} />
      </button>
    </div>
  )
}

// The CSS Custom Highlight API, where the web view has it (WebKit 17.2+, Chrome 105+).
type Highlights = { set(name: string, h: unknown): void; delete(name: string): void }
const registry = (): Highlights | null =>
  (globalThis as { CSS?: { highlights?: Highlights } }).CSS?.highlights ?? null
const HighlightCtor = (globalThis as { Highlight?: new (...r: Range[]) => unknown }).Highlight

function highlightAll(ranges: Range[]) {
  const reg = registry()
  if (!reg || !HighlightCtor) return
  if (ranges.length) reg.set("notes-find", new HighlightCtor(...ranges))
  else {
    reg.delete("notes-find")
    reg.delete("notes-find-current")
  }
}
function highlightCurrent(r: Range | null) {
  const reg = registry()
  if (!reg || !HighlightCtor) return
  if (r) reg.set("notes-find-current", new HighlightCtor(r))
  else reg.delete("notes-find-current")
}
