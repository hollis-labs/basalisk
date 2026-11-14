/**
 * Board: cards in columns by stage or by an enum field; dragging a card to another column
 * sets that value (one new revision of the item, nothing else changed). Columns come from the
 * shown kinds' stages (or the field's options); a value no column names goes to "Other",
 * never lost. The kind filter and grouping are kept in the view item.
 */
import { Plus } from "lucide-react"
import { type DragEvent, useMemo, useState } from "react"
import { type WorkCard, type WorkSpace, type WorkView, work } from "../api/work"
import { ago, kindIcon, label, message } from "./util"

const OTHER = "\u0000other"

export function Board({
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
  view: WorkView | null
  picked: string | null
  pick: (id: string) => void
  changed: () => void
  setError: (s: string) => void
}) {
  const kinds = space.kinds
  const groupBy = view?.group_by || "stage"
  const shownKinds = view?.kinds.length ? view.kinds : kinds.map((k) => k.kind)
  // The board's own optimistic changes, until the next read-back shows them.
  const [moved, setMoved] = useState<Record<string, string>>({})
  const [making, setMaking] = useState<string | null>(null)

  // What a card's group is, and the enum fields that can group the board.
  const groupOf = (c: WorkCard) =>
    moved[c.id] ?? (groupBy === "stage" ? c.stage : String(c.fields[groupBy] ?? ""))
  const enumFields = useMemo(() => {
    const out = new Map<string, { key: string; label: string; options: string[] }>()
    for (const k of kinds)
      for (const f of k.fields)
        if (f.type === "enum" && !out.has(f.key))
          out.set(f.key, { key: f.key, label: f.label || f.key, options: f.options ?? [] })
    return [...out.values()]
  }, [kinds])

  const columns = useMemo(() => {
    const cols: string[] = []
    if (groupBy === "stage") {
      for (const k of kinds)
        if (shownKinds.includes(k.kind))
          for (const s of k.stages) if (!cols.includes(s)) cols.push(s)
    } else {
      cols.push(...(enumFields.find((f) => f.key === groupBy)?.options ?? []), "")
    }
    return cols
  }, [kinds, shownKinds, groupBy, enumFields])

  const cards = space.items.filter((c) => !c.cut && shownKinds.includes(c.kind))
  const byCol = new Map<string, WorkCard[]>()
  for (const c of cards) {
    const v = groupOf(c)
    const col = columns.includes(v) ? v : OTHER
    byCol.set(col, [...(byCol.get(col) ?? []), c])
  }
  const shownCols = [...columns, ...(byCol.has(OTHER) ? [OTHER] : [])]

  const saveView = async (set: Record<string, unknown>) => {
    try {
      if (view) await work.saveView(ws, view.id, set)
      else await work.newView(ws, { view: "board", title: "Board", ...set })
    } catch (e) {
      setError(message(e))
    }
    changed()
  }

  const drop = async (e: DragEvent, col: string) => {
    e.preventDefault()
    const id = e.dataTransfer.getData("text/x-work-item")
    const c = cards.find((x) => x.id === id)
    if (!c || col === OTHER || groupOf(c) === col) return
    const k = kinds.find((x) => x.kind === c.kind)
    if (groupBy === "stage" && k && !k.stages.includes(col)) {
      setError(`A ${k.label.toLowerCase()} has no stage "${col}".`)
      return
    }
    setMoved((m) => ({ ...m, [id]: col }))
    try {
      await work.move(ws, id, groupBy, col)
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

  const make = async (kind: string, value: string) => {
    setMaking(null)
    try {
      const k = kinds.find((x) => x.kind === kind)
      const stage = groupBy === "stage" && k?.stages.includes(value) ? value : undefined
      const fields = groupBy !== "stage" && value ? { [groupBy]: value } : undefined
      const r = await work.make(ws, { kind, stage, fields })
      pick(r.id)
    } catch (e) {
      setError(message(e))
    }
    changed()
  }

  const makeable = kinds.filter((k) => shownKinds.includes(k.kind))

  return (
    <div className="work-boardwrap">
      <div className="work-filter">
        {kinds.map((k) => {
          const on = shownKinds.includes(k.kind)
          const Icon = kindIcon(k.icon)
          return (
            <button
              type="button"
              key={k.kind}
              className="work-chip"
              data-on={on}
              data-color={k.color}
              onClick={() =>
                void saveView({
                  kinds: on
                    ? shownKinds.filter((x) => x !== k.kind)
                    : kinds
                        .map((x) => x.kind)
                        .filter((x) => x === k.kind || shownKinds.includes(x)),
                })
              }
              title={on ? `Hide ${k.plural || k.label}` : `Show ${k.plural || k.label}`}
            >
              <Icon size={12} /> {k.plural || k.label}
            </button>
          )
        })}
        <span className="work-spacer" />
        {enumFields.length > 0 && (
          <select
            className="work-select"
            value={groupBy}
            onChange={(e) => void saveView({ group_by: e.target.value })}
            aria-label="Columns by"
          >
            <option value="stage">By stage</option>
            {enumFields.map((f) => (
              <option key={f.key} value={f.key}>
                By {f.label.toLowerCase()}
              </option>
            ))}
          </select>
        )}
      </div>
      <div className="work-board">
        {shownCols.map((col) => {
          const list = byCol.get(col) ?? []
          return (
            // biome-ignore lint/a11y/noStaticElementInteractions: a drop target for cards
            <section
              key={col}
              className="work-col"
              onDragOver={(e) => e.preventDefault()}
              onDrop={(e) => void drop(e, col)}
            >
              <h3>
                {col === OTHER ? "Other" : col === "" ? "None" : label(col)}{" "}
                <span>{list.length}</span>
                {col !== OTHER && makeable.length > 0 && (
                  <button
                    type="button"
                    className="work-icon work-col-add"
                    title="New item here"
                    onClick={() => setMaking(making === col ? null : col)}
                  >
                    <Plus size={12} />
                  </button>
                )}
              </h3>
              {making === col && (
                <div className="work-makemenu">
                  {makeable.map((k) => {
                    const Icon = kindIcon(k.icon)
                    return (
                      <button type="button" key={k.kind} onClick={() => void make(k.kind, col)}>
                        <Icon size={12} /> {k.label}
                      </button>
                    )
                  })}
                </div>
              )}
              {list.map((c) => {
                const k = kinds.find((x) => x.kind === c.kind)
                const Icon = kindIcon(k?.icon)
                const date = c.fields.publish_on ?? c.fields.due
                return (
                  <button
                    type="button"
                    key={c.id}
                    className="work-card"
                    data-picked={picked === c.id}
                    data-color={k?.color}
                    draggable
                    onDragStart={(e) => e.dataTransfer.setData("text/x-work-item", c.id)}
                    onClick={() => pick(c.id)}
                    title={`${k?.label ?? c.kind} ${c.id}`}
                  >
                    <span className="work-card-title">
                      <Icon size={12} className="work-kindicon" /> {c.title}
                    </span>
                    {c.problem ? (
                      <span className="work-problem-inline">{c.problem}</span>
                    ) : (
                      c.excerpt && <span className="work-card-text">{c.excerpt}</span>
                    )}
                    <span className="work-card-meta">
                      {date ? `${String(date)} · ` : ""}
                      {c.words > 0 ? `${c.words} words · ` : ""}
                      {c.children.length > 0 ? `${c.children.length} inside · ` : ""}
                      {ago(c.updated)}
                    </span>
                  </button>
                )
              })}
            </section>
          )
        })}
      </div>
    </div>
  )
}
