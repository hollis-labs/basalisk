import { AppWindow, type LucideIcon, Search } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"
import { chordFor, prettyChord } from "./keys"
import { allCommands, allProviders, appById, type DeskAPI, type PaletteItem } from "./registry"
import { useDeskSettings } from "./settings"

const RECENT = "basalisk.desk.palette.recent"

interface Row {
  id: string
  title: string
  /** Shown beside the title (provider rows). */
  subtitle?: string
  /** Matched on but not shown (a command's keywords). */
  match?: string
  section: string
  icon?: LucideIcon
  chord?: string
  run: (desk: DeskAPI) => void | Promise<void>
}

/**
 * How well `text` answers `query`: every query character must appear in order. Matches at
 * the start of the text or of a word, and runs of adjacent characters, score higher.
 * Returns 0 for no match.
 */
export function score(query: string, text: string): number {
  if (!query) return 1
  const q = query.toLowerCase()
  const t = text.toLowerCase()
  if (t.startsWith(q)) return 1000 - t.length
  const at = t.indexOf(q)
  if (at >= 0) return (/[\s:./_-]/.test(t[at - 1]) ? 700 : 500) - at
  let ti = 0
  let total = 0
  let streak = 0
  for (const ch of q) {
    const found = t.indexOf(ch, ti)
    if (found < 0) return 0
    streak = found === ti ? streak + 1 : 0
    total += 10 + streak * 6 + (found === 0 || /[\s:./_-]/.test(t[found - 1]) ? 12 : 0)
    ti = found + 1
  }
  return total
}

function recents(): string[] {
  try {
    return JSON.parse(window.localStorage.getItem(RECENT) ?? "[]") as string[]
  } catch {
    return []
  }
}

function remember(id: string) {
  try {
    window.localStorage.setItem(
      RECENT,
      JSON.stringify([id, ...recents().filter((r) => r !== id)].slice(0, 8)),
    )
  } catch {
    // Not remembered.
  }
}

/**
 * The command palette: one box for commands, apps, open windows and whatever the
 * registered providers find (files, notes, cited records). Typing filters; with nothing
 * typed it lists what was used last, then everything by section.
 */
export function PaletteDialog({ desk, onClose }: { desk: DeskAPI; onClose: () => void }) {
  const settings = useDeskSettings()
  const [query, setQuery] = useState("")
  const [selected, setSelected] = useState(0)
  const [found, setFound] = useState<PaletteItem[]>([])
  const list = useRef<HTMLDivElement>(null)

  // biome-ignore lint/correctness/useExhaustiveDependencies: built once per opening; the desktop does not change underneath an open palette
  const base = useMemo<Row[]>(() => {
    const commands: Row[] = allCommands()
      .filter((c) => !c.hidden && (!c.when || c.when(desk)))
      .map((c) => ({
        id: c.id,
        title: c.title,
        match: c.keywords,
        section: c.section,
        icon: c.icon,
        chord: chordFor(c.id, settings.keys),
        run: c.run,
      }))
    const windows: Row[] = desk.wmState().wins.map((w) => ({
      id: `window.focus.${w.id}`,
      title: w.title,
      section: "Open windows",
      icon: appById(w.app)?.icon ?? AppWindow,
      run: (d) => d.wm({ type: "focus", id: w.id }),
    }))
    return [...windows, ...commands]
  }, [])

  // Providers answer asynchronously; a slow one never blocks the list. A provider's rows
  // carry ids that start with "<provider id>:", which is how its newer answer replaces its older.
  useEffect(() => {
    const q = query.trim()
    if (!q) {
      setFound([])
      return
    }
    let live = true
    const timer = window.setTimeout(() => {
      for (const p of allProviders()) {
        void Promise.resolve(p.search(q, desk))
          .then((items) => {
            if (live && items.length)
              setFound((prev) => [...prev.filter((i) => !i.id.startsWith(`${p.id}:`)), ...items])
          })
          .catch(() => undefined)
      }
    }, 80)
    setFound([])
    return () => {
      live = false
      window.clearTimeout(timer)
    }
  }, [query, desk])

  const rows = useMemo<Row[]>(() => {
    const q = query.trim()
    if (!q) {
      const recent = recents()
      const first = recent.map((id) => base.find((r) => r.id === id)).filter((r): r is Row => !!r)
      const rest = base.filter((r) => !recent.includes(r.id))
      return [...first.map((r) => ({ ...r, section: "Recent" })), ...rest]
    }
    const ranked = base
      .map((r) => ({
        r,
        s: Math.max(score(q, r.title), score(q, r.match ?? "") * 0.6, score(q, r.section) * 0.4),
      }))
      .filter((x) => x.s > 0)
      .sort((a, b) => b.s - a.s)
      .map((x) => ({ ...x.r, section: "Results" }))
    return [...ranked, ...found]
  }, [base, found, query])

  // biome-ignore lint/correctness/useExhaustiveDependencies: a new query starts at the top
  useEffect(() => setSelected(0), [query])
  useEffect(() => {
    list.current
      ?.querySelector<HTMLElement>('[data-selected="true"]')
      ?.scrollIntoView({ block: "nearest" })
  }, [selected])

  const run = (row: Row | undefined) => {
    if (!row) return
    remember(row.id)
    onClose()
    void row.run(desk)
  }

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown" || (e.ctrlKey && e.key === "n")) {
      e.preventDefault()
      setSelected((i) => Math.min(i + 1, rows.length - 1))
    } else if (e.key === "ArrowUp" || (e.ctrlKey && e.key === "p")) {
      e.preventDefault()
      setSelected((i) => Math.max(i - 1, 0))
    } else if (e.key === "Enter") {
      e.preventDefault()
      run(rows[selected])
    } else if (e.key === "Escape") {
      e.preventDefault()
      onClose()
    }
  }

  let section = ""
  return (
    <div className="desk-palette-scrim" onPointerDown={onClose}>
      <div
        className="desk-palette"
        role="dialog"
        aria-label="Command palette"
        onPointerDown={(e) => e.stopPropagation()}
        onKeyDown={onKey}
      >
        <div className="desk-palette-input">
          <Search aria-hidden="true" size={16} strokeWidth={1.6} />
          <input
            // biome-ignore lint/a11y/noAutofocus: the palette exists to be typed into
            autoFocus
            value={query}
            placeholder="Run a command, open an app, switch window…"
            spellCheck={false}
            autoComplete="off"
            onChange={(e) => setQuery(e.target.value)}
            aria-controls="desk-palette-list"
          />
        </div>
        <div className="desk-palette-list" id="desk-palette-list" role="listbox" ref={list}>
          {rows.map((r, i) => {
            const heading = r.section !== section ? r.section : ""
            section = r.section
            const Icon = r.icon
            return (
              <div key={r.id}>
                {heading && <div className="desk-palette-section">{heading}</div>}
                <button
                  type="button"
                  role="option"
                  aria-selected={i === selected}
                  data-selected={i === selected}
                  className="desk-palette-row"
                  onPointerMove={() => i !== selected && setSelected(i)}
                  onClick={() => run(r)}
                >
                  {Icon ? (
                    <Icon aria-hidden="true" size={15} strokeWidth={1.6} />
                  ) : (
                    <span className="desk-palette-noicon" />
                  )}
                  <span className="desk-palette-title">{r.title}</span>
                  {r.subtitle && <span className="desk-palette-sub">{r.subtitle}</span>}
                  {r.chord && <kbd>{prettyChord(r.chord)}</kbd>}
                </button>
              </div>
            )
          })}
          {!rows.length && <div className="desk-palette-empty">Nothing matches.</div>}
        </div>
        <footer className="desk-palette-foot">
          <span>↑↓ move</span>
          <span>↩ run</span>
          <span>esc close</span>
        </footer>
      </div>
    </div>
  )
}
