import { ChevronDown, ChevronUp, X } from "lucide-react"
import { type RefObject, useEffect, useRef, useState } from "react"

/**
 * Find in page: a bar under the address field. Typing searches as you go; Enter and ⇧Enter
 * (or ⌘G and ⇧⌘G, handled by the window) step through matches; Esc closes it.
 */
export function FindBar({
  inputRef,
  find,
  onClose,
}: {
  inputRef: RefObject<HTMLInputElement | null>
  /** Runs a search; resolves to whether anything matched. */
  find: (query: string, backwards: boolean) => Promise<boolean>
  onClose: () => void
}) {
  const [query, setQuery] = useState("")
  const [missing, setMissing] = useState(false)
  const latest = useRef(0)

  // Search as the query changes, a beat after the last key.
  useEffect(() => {
    if (!query) {
      setMissing(false)
      return
    }
    const n = ++latest.current
    const timer = window.setTimeout(() => {
      void find(query, false).then((found) => n === latest.current && setMissing(!found))
    }, 120)
    return () => window.clearTimeout(timer)
  }, [query, find])

  const step = (backwards: boolean) => {
    if (!query) return
    const n = ++latest.current
    void find(query, backwards).then((found) => n === latest.current && setMissing(!found))
  }

  return (
    <div className="browser-find" data-missing={missing}>
      <input
        ref={inputRef}
        value={query}
        spellCheck={false}
        aria-label="Find in page"
        placeholder="Find in page"
        onChange={(e) => setQuery(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault()
            step(e.shiftKey)
          } else if (e.key === "Escape") {
            e.preventDefault()
            e.stopPropagation()
            onClose()
          }
        }}
      />
      {missing && <span className="browser-find-none">No matches</span>}
      <button
        type="button"
        className="files-icon"
        title="Previous (⇧⌘G)"
        onClick={() => step(true)}
      >
        <ChevronUp size={14} strokeWidth={1.6} aria-label="Previous match" />
      </button>
      <button type="button" className="files-icon" title="Next (⌘G)" onClick={() => step(false)}>
        <ChevronDown size={14} strokeWidth={1.6} aria-label="Next match" />
      </button>
      <button type="button" className="files-icon" title="Close (Esc)" onClick={onClose}>
        <X size={14} strokeWidth={1.6} aria-label="Close find" />
      </button>
    </div>
  )
}
