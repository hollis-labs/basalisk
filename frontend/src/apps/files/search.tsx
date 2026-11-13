import { FileText, Folder, Search } from "lucide-react"
import { type KeyboardEvent, useEffect, useRef, useState } from "react"
import { request } from "../../api/account"
import { type FileEntry, type FileRoot, files } from "../../api/files"
import { baseOf, parentOf } from "./model"

interface Match {
  path: string
  line: number
  text: string
}

export type Hit = { kind: "name"; entry: FileEntry } | { kind: "line"; match: Match }

/**
 * Search under the folder a Files window shows: by name (recursive, on the server) or by
 * contents (ripgrep where the place has it). Results replace the listing until Escape.
 */
export function SearchPane({
  root,
  folder,
  onOpen,
  onClose,
}: {
  root: FileRoot
  folder: string
  onOpen: (hit: Hit) => void
  onClose: () => void
}) {
  const [q, setQ] = useState("")
  const [mode, setMode] = useState<"names" | "contents">("names")
  const [hits, setHits] = useState<Hit[]>([])
  const [state, setState] = useState<{ busy: boolean; note: string }>({ busy: false, note: "" })
  const [at, setAt] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => inputRef.current?.focus(), [])

  useEffect(() => {
    const query = q.trim()
    if (query.length < 2) {
      setHits([])
      setState({ busy: false, note: query ? "Type at least two characters." : "" })
      return
    }
    let live = true
    setState({ busy: true, note: "" })
    const timer = window.setTimeout(() => {
      const run =
        mode === "names"
          ? files.search(root.id, folder, query).then((r) => ({
              hits: r.entries.map((entry) => ({ kind: "name" as const, entry })),
              truncated: r.truncated,
              engine: "",
            }))
          : request<{ matches: Match[]; truncated: boolean; engine: string }>(
              `files/grep?root=${encodeURIComponent(root.id)}&path=${encodeURIComponent(folder)}&q=${encodeURIComponent(query)}`,
            ).then((r) => ({
              hits: r.matches.map((match) => ({ kind: "line" as const, match })),
              truncated: r.truncated,
              engine: r.engine,
            }))
      run
        .then((r) => {
          if (!live) return
          setHits(r.hits)
          setAt(0)
          const n = r.hits.length
          setState({
            busy: false,
            note: `${n || "No"} ${mode === "names" ? "match" : "line"}${n === 1 ? "" : mode === "names" ? "es" : "s"}${r.truncated ? " (stopped early; narrow the search)" : ""}${r.engine === "scan" ? " · searched without ripgrep" : ""}`,
          })
        })
        .catch((e: Error) => live && setState({ busy: false, note: e.message }))
    }, 250)
    return () => {
      live = false
      window.clearTimeout(timer)
    }
  }, [q, mode, root.id, folder])

  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    let handled = true
    if (e.key === "Escape") onClose()
    else if (e.key === "ArrowDown") setAt((i) => Math.min(hits.length - 1, i + 1))
    else if (e.key === "ArrowUp") setAt((i) => Math.max(0, i - 1))
    else if (e.key === "Enter" && hits[at]) onOpen(hits[at])
    else if (e.key === "Tab" || (e.metaKey && e.altKey && e.code === "KeyF"))
      setMode((m) => (m === "names" ? "contents" : "names"))
    else handled = false
    if (handled) {
      e.preventDefault()
      e.stopPropagation()
    }
  }

  const where = folder ? `${root.label} / ${folder}` : root.label
  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: the search's own keys
    <div className="files-search" onKeyDown={onKey}>
      <div className="files-search-bar">
        <Search aria-hidden="true" size={13} strokeWidth={1.6} />
        <input
          ref={inputRef}
          value={q}
          spellCheck={false}
          aria-label={`Search ${mode} under ${where}`}
          placeholder={`Search ${mode} under ${where}`}
          onChange={(e) => setQ(e.target.value)}
        />
        <div className="files-search-modes" role="tablist">
          {(["names", "contents"] as const).map((m) => (
            <button
              key={m}
              type="button"
              role="tab"
              aria-selected={mode === m}
              data-on={mode === m}
              tabIndex={-1}
              onClick={() => setMode(m)}
            >
              {m}
            </button>
          ))}
        </div>
      </div>
      <div className="files-search-list" role="listbox" aria-label="Search results">
        {hits.map((h, i) => {
          const p = h.kind === "name" ? h.entry.path : h.match.path
          const shownPath = folder && p.startsWith(`${folder}/`) ? p.slice(folder.length + 1) : p
          return (
            // biome-ignore lint/a11y/useKeyWithClickEvents: the search handles the keyboard
            <div
              key={h.kind === "name" ? p : `${p}:${h.match.line}`}
              role="option"
              aria-selected={i === at}
              data-selected={i === at}
              tabIndex={-1}
              className="files-search-hit"
              onClick={() => setAt(i)}
              onDoubleClick={() => onOpen(h)}
            >
              {h.kind === "name" && h.entry.dir ? (
                <Folder size={14} strokeWidth={1.6} aria-hidden="true" />
              ) : (
                <FileText size={14} strokeWidth={1.6} aria-hidden="true" />
              )}
              <span className="files-search-name">
                {h.kind === "name" ? baseOf(p) : `${baseOf(p)}:${h.match.line}`}
              </span>
              <span className="files-search-where">
                {h.kind === "name" ? parentOf(shownPath) || "." : h.match.text}
              </span>
            </div>
          )
        })}
      </div>
      <footer className="files-foot">
        <span>{state.busy ? "Searching…" : state.note}</span>
        <span className="files-hint">↵ open · tab names/contents · esc back to the folder</span>
      </footer>
    </div>
  )
}
