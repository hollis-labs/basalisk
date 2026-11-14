/**
 * Calendar: items placed by the view's date field (publish_on in Content), by month or by
 * week. Dragging an item to another day sets that date (one new revision, nothing else
 * changed); items with no date wait in the side list, and dropping one there takes the date
 * out. The kinds shown and the date field are kept in the view item; month or week is this
 * window's own.
 */
import { ChevronLeft, ChevronRight, Plus } from "lucide-react"
import { type DragEvent, useMemo, useState } from "react"
import { type WorkCard, type WorkSpace, type WorkView, work } from "../api/work"
import { KindChips } from "./chips"
import { kindIcon, label, message } from "./util"

const NONE = ""
const DAYS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"]

/** A local date as YYYY-MM-DD. */
export const ymd = (d: Date) =>
  `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`
const addDays = (d: Date, n: number) => new Date(d.getFullYear(), d.getMonth(), d.getDate() + n)
/** The Monday on or before d. */
const monday = (d: Date) => addDays(d, -((d.getDay() + 6) % 7))
/** A field's value as a day, or "" (a time keeps its date part). */
const dayOf = (v: unknown) =>
  typeof v === "string" && /^\d{4}-\d{2}-\d{2}/.test(v) ? v.slice(0, 10) : NONE

export function Calendar({
  ws,
  space,
  view,
  picked,
  pick,
  changed,
  setError,
}: {
  ws: string
  space: WorkSpace
  view: WorkView
  picked: string | null
  pick: (id: string) => void
  changed: () => void
  setError: (s: string) => void
}) {
  const kinds = space.kinds
  const shownKinds = view.kinds.length ? view.kinds : kinds.map((k) => k.kind)
  const [mode, setMode] = useState<"month" | "week">("month")
  const [anchor, setAnchor] = useState(() => new Date())
  const [moved, setMoved] = useState<Record<string, string>>({})
  const [making, setMaking] = useState<string | null>(null)

  // The date fields the shown kinds have; the view's, or the first of them.
  const dateFields = useMemo(() => {
    const out = new Map<string, string>()
    for (const k of kinds)
      if (shownKinds.includes(k.kind))
        for (const f of k.fields)
          if (f.type === "date" && !out.has(f.key)) out.set(f.key, f.label || f.key)
    return [...out.entries()]
  }, [kinds, shownKinds])
  const field =
    (view.date_field && dateFields.some(([k]) => k === view.date_field) && view.date_field) ||
    dateFields[0]?.[0] ||
    "publish_on"
  const dated = kinds.filter((k) => k.fields.some((f) => f.key === field && f.type === "date"))
  const datedKinds = new Set(dated.map((k) => k.kind))

  const cards = space.items.filter(
    (c) => !c.cut && shownKinds.includes(c.kind) && datedKinds.has(c.kind),
  )
  const dayOfCard = (c: WorkCard) => moved[c.id] ?? dayOf(c.fields[field])
  const byDay = new Map<string, WorkCard[]>()
  for (const c of cards) {
    const d = dayOfCard(c)
    byDay.set(d, [...(byDay.get(d) ?? []), c])
  }

  // The days on screen: whole weeks covering the month, or the anchor's week.
  const first =
    mode === "month" ? monday(new Date(anchor.getFullYear(), anchor.getMonth(), 1)) : monday(anchor)
  const weeks =
    mode === "month"
      ? Math.ceil(
          ((addDays(new Date(anchor.getFullYear(), anchor.getMonth() + 1, 1), -1).getTime() -
            first.getTime()) /
            86_400_000 +
            1) /
            7,
        )
      : 1
  const days = Array.from({ length: weeks * 7 }, (_, i) => addDays(first, i))
  const today = ymd(new Date())
  const heading =
    mode === "month"
      ? anchor.toLocaleDateString(undefined, { month: "long", year: "numeric" })
      : `${first.toLocaleDateString(undefined, { month: "short", day: "numeric" })} – ${addDays(
          first,
          6,
        ).toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" })}`
  const step = (n: number) =>
    setAnchor((a) =>
      mode === "month" ? new Date(a.getFullYear(), a.getMonth() + n, 1) : addDays(a, 7 * n),
    )

  const saveView = async (set: Record<string, unknown>) => {
    try {
      await work.saveView(ws, view.id, set)
    } catch (e) {
      setError(message(e))
    }
    changed()
  }

  const drop = async (e: DragEvent, day: string) => {
    e.preventDefault()
    const id = e.dataTransfer.getData("text/x-work-item")
    const c = cards.find((x) => x.id === id)
    if (!c || dayOfCard(c) === day) return
    setMoved((m) => ({ ...m, [id]: day }))
    try {
      await work.move(ws, id, field, day)
      setError("")
    } catch (err) {
      setError(message(err))
    }
    setMoved((m) => {
      const { [id]: _, ...rest } = m
      return rest
    })
    changed()
  }

  const make = async (kind: string, day: string) => {
    setMaking(null)
    try {
      const r = await work.make(ws, { kind, fields: { [field]: day } })
      pick(r.id)
    } catch (e) {
      setError(message(e))
    }
    changed()
  }
  const makeable = dated.filter((k) => shownKinds.includes(k.kind))

  const chip = (c: WorkCard) => {
    const k = kinds.find((x) => x.kind === c.kind)
    const Icon = kindIcon(k?.icon)
    return (
      <button
        type="button"
        key={c.id}
        className="work-calitem"
        data-picked={picked === c.id}
        data-color={k?.color}
        data-stage={c.stage}
        draggable
        onDragStart={(e) => e.dataTransfer.setData("text/x-work-item", c.id)}
        onClick={() => pick(c.id)}
        title={`${k?.label ?? c.kind} · ${label(c.stage) || "no stage"} · ${c.title}`}
      >
        <Icon size={11} className="work-kindicon" />
        <span className="work-calitem-title">{c.title}</span>
      </button>
    )
  }

  const undated = byDay.get(NONE) ?? []
  return (
    <div className="work-boardwrap">
      <div className="work-filter">
        <KindChips kinds={kinds} shown={shownKinds} set={(k) => void saveView({ kinds: k })} />
        <span className="work-spacer" />
        {dateFields.length > 1 && (
          <select
            className="work-select"
            value={field}
            onChange={(e) => void saveView({ date_field: e.target.value })}
            aria-label="Placed by"
          >
            {dateFields.map(([key, name]) => (
              <option key={key} value={key}>
                By {name.toLowerCase()}
              </option>
            ))}
          </select>
        )}
      </div>
      <div className="work-calbar">
        <button type="button" className="work-icon" onClick={() => step(-1)} title="Earlier">
          <ChevronLeft size={15} />
        </button>
        <button type="button" className="work-icon" onClick={() => step(1)} title="Later">
          <ChevronRight size={15} />
        </button>
        <button type="button" className="desk-button" onClick={() => setAnchor(new Date())}>
          Today
        </button>
        <span className="work-calhead">{heading}</span>
        <span className="work-spacer" />
        <nav className="work-tabs">
          {(["month", "week"] as const).map((m) => (
            <button type="button" key={m} data-on={mode === m} onClick={() => setMode(m)}>
              {label(m)}
            </button>
          ))}
        </nav>
      </div>
      <div className="work-cal">
        <div className="work-calgrid" data-mode={mode}>
          {DAYS.map((d) => (
            <div key={d} className="work-caldow">
              {d}
            </div>
          ))}
          {days.map((d) => {
            const day = ymd(d)
            const list = byDay.get(day) ?? []
            return (
              // biome-ignore lint/a11y/noStaticElementInteractions: a drop target for items
              <section
                key={day}
                className="work-calday"
                data-day={day}
                data-out={mode === "month" && d.getMonth() !== anchor.getMonth()}
                data-today={day === today}
                onDragOver={(e) => e.preventDefault()}
                onDrop={(e) => void drop(e, day)}
              >
                <header>
                  <span>{d.getDate()}</span>
                  {makeable.length > 0 && (
                    <button
                      type="button"
                      className="work-icon work-caladd"
                      title="New item on this day"
                      onClick={() => setMaking(making === day ? null : day)}
                    >
                      <Plus size={11} />
                    </button>
                  )}
                </header>
                {making === day && (
                  <div className="work-makemenu">
                    {makeable.map((k) => {
                      const Icon = kindIcon(k.icon)
                      return (
                        <button type="button" key={k.kind} onClick={() => void make(k.kind, day)}>
                          <Icon size={12} /> {k.label}
                        </button>
                      )
                    })}
                  </div>
                )}
                {list.map(chip)}
              </section>
            )
          })}
        </div>
        <aside
          className="work-calside"
          onDragOver={(e) => e.preventDefault()}
          onDrop={(e) => void drop(e, NONE)}
        >
          <h3>
            No date <span>{undated.length}</span>
          </h3>
          {undated.length === 0 && (
            <p className="work-rowtext">Drop an item here to take its date out.</p>
          )}
          {undated.map(chip)}
        </aside>
      </div>
    </div>
  )
}
