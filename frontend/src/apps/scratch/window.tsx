/**
 * The Scratch window: the owner's quick notes, commands, follow-ups and ideas. Each is a
 * small item of its own; what is shown is a view put together from them (a day, a project,
 * a task, the open follow-ups, a search), editable in place. See docs/scratch.md.
 */
import { Archive, Copy, CornerDownLeft, Play, Search } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { runTool } from "../../agent/tools"
import {
  type Count,
  SCRATCH_CHANGED,
  type ScratchFacets,
  type ScratchItem,
  type ScratchView,
  scratch,
} from "../../api/scratch"
import { CiteText } from "../../cite/ui"
import { useDesk } from "../../desktop/context"
import { moment } from "../frag/context"
import "./scratch.css"

const KIND_LABEL: Record<string, string> = {
  note: "note",
  todo: "todo",
  "follow-up": "follow-up",
  idea: "idea",
  cmd: "command",
  question: "question",
}
const CHECKABLE = new Set(["todo", "follow-up", "question"])

function dayLabel(day: string, today: string): string {
  if (day === today) return "Today"
  const d = new Date(`${day}T12:00:00`)
  const t = new Date(`${today}T12:00:00`)
  const diff = Math.round((t.getTime() - d.getTime()) / 86_400_000)
  if (diff === 1) return "Yesterday"
  return d.toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric" })
}

function viewTitle(v: ScratchView, today: string): string {
  if (v.q) return `Search: ${v.q}`
  if (v.open) return "Open follow-ups"
  if (v.archived) return "Put away"
  if (v.day) return dayLabel(v.day, today)
  if (v.kind) return `${KIND_LABEL[v.kind] ?? v.kind}s`
  if (v.rel && v.value) return `${v.rel}: ${v.value}`
  return "Everything"
}

const sameView = (a: ScratchView, b: ScratchView) => JSON.stringify(a) === JSON.stringify(b)

export function ScratchWindow() {
  const desk = useDesk()
  const [today, setToday] = useState("")
  const [view, setView] = useState<ScratchView>({})
  const [items, setItems] = useState<ScratchItem[]>([])
  const [facets, setFacets] = useState<ScratchFacets | null>(null)
  const [draft, setDraft] = useState("")
  const [find, setFind] = useState("")
  const [error, setError] = useState("")
  const [note, setNote] = useState("")
  const box = useRef<HTMLTextAreaElement>(null)
  const started = useRef(false)

  const load = useCallback(async (v: ScratchView) => {
    try {
      const [list, f] = await Promise.all([scratch.list(v), scratch.facets()])
      setItems(list.items)
      setFacets(f.facets)
      setToday(list.today)
      setError("")
      return list.today
    } catch (e) {
      setError(e instanceof Error ? e.message : "The scratch could not be read.")
      return ""
    }
  }, [])

  // The window opens on today.
  useEffect(() => {
    if (started.current) return
    started.current = true
    void scratch
      .facets()
      .then((f) => {
        setToday(f.today)
        setView({ day: f.today })
      })
      .catch(() => setView({}))
  }, [])

  useEffect(() => {
    void load(view)
  }, [view, load])

  // Scratch added elsewhere (the capture box, the agent) shows up here.
  useEffect(() => {
    const again = () => void load(view)
    window.addEventListener(SCRATCH_CHANGED, again)
    return () => window.removeEventListener(SCRATCH_CHANGED, again)
  }, [view, load])

  const add = async () => {
    const text = draft.trim()
    if (!text) return
    try {
      const at = await moment(desk).catch(() => undefined)
      await scratch.add(text, at)
      setDraft("")
      // A new item is written today: show it if the view would not.
      if (view.day && view.day !== today) setView({ day: today })
      else await load(view)
    } catch (e) {
      setError(e instanceof Error ? e.message : "It could not be kept.")
    }
  }

  const change = async (id: string, c: { text?: string; done?: boolean; archived?: boolean }) => {
    try {
      await scratch.update(id, c)
      await load(view)
    } catch (e) {
      setError(e instanceof Error ? e.message : "The item could not be changed.")
    }
  }

  const copyPage = async () => {
    try {
      const page = await scratch.page(view, `Scratch: ${viewTitle(view, today)}`)
      await navigator.clipboard.writeText(page)
      setNote("Copied this view as a markdown page.")
      window.setTimeout(() => setNote(""), 2500)
    } catch (e) {
      setError(e instanceof Error ? e.message : "The page could not be copied.")
    }
  }

  // Items of several days are shown under their day.
  const groups = useMemo(() => {
    const out: { day: string; items: ScratchItem[] }[] = []
    for (const it of items) {
      const last = out[out.length - 1]
      if (last && last.day === it.day) last.items.push(it)
      else out.push({ day: it.day, items: [it] })
    }
    return out.reverse()
  }, [items])

  const pick = (v: ScratchView) => {
    setFind("")
    setView(v)
  }

  return (
    <div className="scratch">
      <nav className="scratch-side" aria-label="Views">
        <form
          className="scratch-find"
          onSubmit={(e) => {
            e.preventDefault()
            setView(find.trim() ? { q: find.trim() } : { day: today })
          }}
        >
          <Search size={13} aria-hidden="true" />
          <input
            value={find}
            placeholder="Search scratch"
            aria-label="Search scratch"
            onChange={(e) => setFind(e.target.value)}
          />
        </form>
        <SideRow
          label="Today"
          on={sameView(view, { day: today })}
          go={() => pick({ day: today })}
        />
        <SideRow
          label="Open follow-ups"
          n={facets?.open}
          on={!!view.open}
          go={() => pick({ open: true })}
        />
        <SideRow label="Everything" n={facets?.total} on={sameView(view, {})} go={() => pick({})} />
        <SideGroup
          title="Days"
          counts={(facets?.days ?? []).slice(0, 14)}
          label={(d) => dayLabel(d, today)}
          on={(d) => view.day === d}
          go={(d) => pick({ day: d })}
        />
        <SideGroup
          title="Kinds"
          counts={facets?.kinds ?? []}
          label={(k) => KIND_LABEL[k] ?? k}
          on={(k) => view.kind === k}
          go={(k) => pick({ kind: k })}
        />
        {(
          [
            ["Projects", "project", facets?.projects],
            ["Tasks", "task", facets?.tasks],
            ["Agents", "agent", facets?.agents],
            ["Tags", "tag", facets?.tags],
          ] as const
        ).map(([title, rel, counts]) => (
          <SideGroup
            key={rel}
            title={title}
            counts={counts ?? []}
            label={(v) => v}
            on={(v) => view.rel === rel && view.value === v}
            go={(v) => pick({ rel, value: v })}
          />
        ))}
        <SideRow label="Put away" on={!!view.archived} go={() => pick({ archived: true })} />
      </nav>

      <section className="scratch-main">
        <header className="scratch-head">
          <h2>{viewTitle(view, today)}</h2>
          <span className="scratch-count">
            {items.length} {items.length === 1 ? "item" : "items"}
          </span>
          <span className="scratch-spacer" />
          <button
            type="button"
            className="scratch-act"
            title="Copy this view as a markdown page"
            disabled={!items.length}
            onClick={() => void copyPage()}
          >
            <Copy size={13} /> page
          </button>
        </header>

        <div className="scratch-add">
          <textarea
            ref={box}
            rows={1}
            value={draft}
            placeholder="Jot it down.  ::idea …  ::follow-up …  $ command  [] todo  +project @agent #tag"
            aria-label="New scratch"
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                e.preventDefault()
                void add()
              }
            }}
          />
          <button
            type="button"
            className="scratch-act"
            title="Keep it (Enter)"
            disabled={!draft.trim()}
            onClick={() => void add()}
          >
            <CornerDownLeft size={13} />
          </button>
        </div>

        {error && <p className="scratch-error">{error}</p>}
        {note && <p className="scratch-note">{note}</p>}

        <div className="scratch-list">
          {!items.length && !error && (
            <p className="scratch-empty">
              Nothing here yet. Type above, or use the capture box (⌥⇧S) from anywhere.
            </p>
          )}
          {groups.map((g) => (
            <div key={g.day} className="scratch-group">
              {groups.length > 1 && <div className="scratch-day">{dayLabel(g.day, today)}</div>}
              {g.items.map((it) => (
                <ItemRow
                  key={it.id}
                  item={it}
                  change={change}
                  pick={pick}
                  run={(command) => void runTool("terminal_run", { command }, desk)}
                />
              ))}
            </div>
          ))}
        </div>
      </section>
    </div>
  )
}

function SideRow({ label, n, on, go }: { label: string; n?: number; on: boolean; go: () => void }) {
  return (
    <button type="button" className="scratch-row" data-on={on} onClick={go}>
      <span className="scratch-row-label">{label}</span>
      {n !== undefined && n > 0 && <span className="scratch-row-n">{n}</span>}
    </button>
  )
}

function SideGroup({
  title,
  counts,
  label,
  on,
  go,
}: {
  title: string
  counts: Count[]
  label: (value: string) => string
  on: (value: string) => boolean
  go: (value: string) => void
}) {
  if (!counts.length) return null
  return (
    <div className="scratch-sidegroup">
      <div className="scratch-sidetitle">{title}</div>
      {counts.map((c) => (
        <SideRow
          key={c.value}
          label={label(c.value)}
          n={c.n}
          on={on(c.value)}
          go={() => go(c.value)}
        />
      ))}
    </div>
  )
}

function ItemRow({
  item,
  change,
  pick,
  run,
}: {
  item: ScratchItem
  change: (id: string, c: { text?: string; done?: boolean; archived?: boolean }) => Promise<void>
  pick: (v: ScratchView) => void
  run: (command: string) => void
}) {
  const [editing, setEditing] = useState(false)
  const [text, setText] = useState(item.text)
  const area = useRef<HTMLTextAreaElement>(null)
  useEffect(() => {
    if (editing) {
      setText(item.text)
      requestAnimationFrame(() => {
        const el = area.current
        if (!el) return
        el.focus()
        el.setSelectionRange(el.value.length, el.value.length)
      })
    }
  }, [editing, item.text])

  const save = () => {
    setEditing(false)
    if (text.trim() && text !== item.text) void change(item.id, { text })
  }
  const time = new Date(item.created_at).toLocaleTimeString(undefined, {
    hour: "numeric",
    minute: "2-digit",
  })

  return (
    <div className="scratch-item" data-kind={item.kind} data-done={item.done}>
      <div className="scratch-gutter">
        {CHECKABLE.has(item.kind) ? (
          <input
            type="checkbox"
            className="scratch-check"
            checked={item.done}
            title={item.done ? "Mark not done" : "Mark done"}
            aria-label={item.done ? "Mark not done" : "Mark done"}
            onChange={() => void change(item.id, { done: !item.done })}
          />
        ) : (
          <span className="scratch-dot" aria-hidden="true" />
        )}
      </div>
      <div className="scratch-body">
        {editing ? (
          <textarea
            ref={area}
            className="scratch-edit"
            value={text}
            rows={Math.min(12, text.split("\n").length + 1)}
            onChange={(e) => setText(e.target.value)}
            onBlur={save}
            onKeyDown={(e) => {
              if (e.key === "Escape") {
                e.preventDefault()
                e.stopPropagation()
                setEditing(false)
              } else if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                e.preventDefault()
                save()
              }
            }}
          />
        ) : (
          // biome-ignore lint/a11y/noStaticElementInteractions: a double click starts editing; the button beside it does too
          <div
            className="scratch-text"
            data-mono={item.kind === "cmd"}
            onDoubleClick={() => setEditing(true)}
          >
            {item.kind === "cmd" ? item.text : <CiteText text={item.text} />}
          </div>
        )}
        <div className="scratch-meta">
          {item.kind !== "note" && (
            <button
              type="button"
              className="scratch-chip"
              onClick={() => pick({ kind: item.kind })}
            >
              {KIND_LABEL[item.kind] ?? item.kind}
            </button>
          )}
          {item.links.map((l) => (
            <button
              key={`${l.rel}:${l.value}`}
              type="button"
              className="scratch-chip"
              data-rel={l.rel}
              title={`Everything tied to this ${l.rel}`}
              onClick={() => pick({ rel: l.rel, value: l.value })}
            >
              {l.rel === "project" ? "+" : l.rel === "agent" ? "@" : l.rel === "tag" ? "#" : ""}
              {l.value}
            </button>
          ))}
          <span className="scratch-time">{time}</span>
          {item.origin === "agent" && <span className="scratch-time">by the agent</span>}
          {!item.synced && (
            <span
              className="scratch-time"
              title={item.sync_error || "Not written to Caster yet; it will be."}
            >
              {item.sync_error ? "not in Caster" : "saving…"}
            </span>
          )}
        </div>
      </div>
      <div className="scratch-tools">
        {item.kind === "cmd" && (
          <button
            type="button"
            className="scratch-tool"
            title="Run it in the terminal"
            onClick={() => run(item.text)}
          >
            <Play size={12} />
          </button>
        )}
        <button
          type="button"
          className="scratch-tool"
          title="Copy"
          onClick={() => void navigator.clipboard.writeText(item.text)}
        >
          <Copy size={12} />
        </button>
        <button
          type="button"
          className="scratch-tool"
          title={item.archived ? "Bring it back" : "Put it away"}
          onClick={() => void change(item.id, { archived: !item.archived })}
        >
          <Archive size={12} />
        </button>
      </div>
    </div>
  )
}
