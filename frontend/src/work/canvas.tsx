/**
 * Canvas: cards placed freely, stacks (titled piles of cards) and free notes, on a plane you
 * pan (drag the background, or scroll) and zoom (⌘ or Ctrl with scroll, or the buttons).
 * Drag a card from the tray onto the canvas or into a stack, between stacks, or back to the
 * tray to take it off. A stack can be bound to a stage or a parent: dropping a card there
 * also sets that on the item. The arrangement is kept in the view item (layout), patched by
 * id so another window's changes are not written over; the pan and zoom are this window's.
 */
import { Minus, Plus, StickyNote, X } from "lucide-react"
import { type PointerEvent as RPointerEvent, useEffect, useMemo, useRef, useState } from "react"
import {
  type WorkCanvasNote,
  type WorkCard,
  type WorkLayoutPatch,
  type WorkPos,
  type WorkSpace,
  type WorkStack,
  type WorkView,
  work,
} from "../api/work"
import { KindChips } from "./chips"
import { kindIcon, label, message } from "./util"

const newId = () => {
  const abc = "abcdefghijklmnopqrstuvwxyz0123456789"
  const b = new Uint8Array(8)
  crypto.getRandomValues(b)
  return Array.from(b, (x) => abc[x % abc.length]).join("")
}

interface Layout {
  cards: Record<string, WorkPos>
  stacks: WorkStack[]
  notes: WorkCanvasNote[]
}
const layoutOf = (v: WorkView): Layout => {
  const l = (v.layout ?? {}) as Partial<Layout>
  return {
    cards: l.cards && typeof l.cards === "object" ? l.cards : {},
    stacks: Array.isArray(l.stacks) ? l.stacks.map((s) => ({ ...s, items: s.items ?? [] })) : [],
    notes: Array.isArray(l.notes) ? l.notes : [],
  }
}

type Drag =
  | {
      type: "card"
      id: string
      from: "canvas" | "tray" | string // a stack id
      dx: number
      dy: number
      x: number
      y: number
      moved: boolean
    }
  | {
      type: "stack" | "note"
      id: string
      dx: number
      dy: number
      x: number
      y: number
      moved: boolean
    }
  | { type: "pan"; sx: number; sy: number; px: number; py: number }

export function Canvas({
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
  const kinds = useMemo(() => new Map(space.kinds.map((k) => [k.kind, k])), [space.kinds])
  const shownKinds = view.kinds.length ? view.kinds : space.kinds.map((k) => k.kind)
  const [lay, setLay] = useState<Layout>(() => layoutOf(view))
  const [pan, setPan] = useState({ x: 40, y: 40 })
  const [zoom, setZoom] = useState(1)
  const [drag, setDrag] = useState<Drag | null>(null)
  const [editNote, setEditNote] = useState<string | null>(null)
  const [editStack, setEditStack] = useState<string | null>(null)
  const plane = useRef<HTMLDivElement>(null)
  const dragRef = useRef<Drag | null>(null)
  dragRef.current = drag

  // Take the view's arrangement when it changes in Caster, unless a drag is under way.
  // biome-ignore lint/correctness/useExhaustiveDependencies: a newer revision of the view resets it
  useEffect(() => {
    if (!dragRef.current) setLay(layoutOf(view))
  }, [view.rev])

  const byId = useMemo(
    () => new Map(space.items.filter((c) => !c.cut).map((c) => [c.id, c])),
    [space.items],
  )
  const shown = (c: WorkCard | undefined): c is WorkCard => !!c && shownKinds.includes(c.kind)
  const inStack = new Set(lay.stacks.flatMap((s) => s.items))
  const tray = space.items.filter(
    (c) => !c.cut && shownKinds.includes(c.kind) && !lay.cards[c.id] && !inStack.has(c.id),
  )

  const toPlane = (cx: number, cy: number) => {
    const r = plane.current?.getBoundingClientRect()
    return { x: (cx - (r?.left ?? 0) - pan.x) / zoom, y: (cy - (r?.top ?? 0) - pan.y) / zoom }
  }

  const save = async (patch: WorkLayoutPatch) => {
    try {
      await work.layout(ws, view.id, patch)
      setError("")
    } catch (e) {
      setError(message(e))
    }
    changed()
  }

  // A card dropped into a bound stack: set that stage or parent on the item too.
  const bindDrop = async (st: WorkStack, id: string) => {
    const c = byId.get(id)
    if (!st.bind || !c) return
    try {
      if (st.bind.key === "stage") {
        if (c.stage === st.bind.value) return
        if (!kinds.get(c.kind)?.stages.includes(st.bind.value)) {
          setError(
            `A ${kinds.get(c.kind)?.label.toLowerCase() ?? c.kind} has no stage "${st.bind.value}"; it is in the stack, its stage is as it was.`,
          )
          return
        }
        await work.move(ws, id, "stage", st.bind.value)
      } else if (c.parent !== st.bind.value) {
        await work.place(ws, id, st.bind.value, "", "")
      }
    } catch (e) {
      setError(`In the stack, but not changed: ${message(e)}`)
    }
  }

  // ---- Dragging: pointer events on the window while a drag is under way ----
  // biome-ignore lint/correctness/useExhaustiveDependencies: one listener pair a drag; the drag is read through a ref, the rest from the latest render
  useEffect(() => {
    if (!drag) return
    const move = (e: PointerEvent) => {
      const d = dragRef.current
      if (!d) return
      if (d.type === "pan") {
        setPan({ x: d.px + e.clientX - d.sx, y: d.py + e.clientY - d.sy })
        return
      }
      const moved = d.moved || Math.abs(e.clientX - d.x) + Math.abs(e.clientY - d.y) > 4
      setDrag({ ...d, x: e.clientX, y: e.clientY, moved })
    }
    const up = (e: PointerEvent) => {
      const d = dragRef.current
      setDrag(null)
      if (!d || d.type === "pan") return
      if (!d.moved) {
        if (d.type === "card") pick(d.id)
        return
      }
      const p = toPlane(e.clientX - d.dx, e.clientY - d.dy)
      const pos = { x: Math.round(p.x), y: Math.round(p.y) }
      if (d.type === "stack") {
        const st = lay.stacks.find((s) => s.id === d.id)
        if (!st) return
        const next = { ...st, ...pos }
        setLay((l) => ({ ...l, stacks: l.stacks.map((s) => (s.id === d.id ? next : s)) }))
        void save({ stacks: { [d.id]: next } })
        return
      }
      if (d.type === "note") {
        const n = lay.notes.find((x) => x.id === d.id)
        if (!n) return
        const next = { ...n, ...pos }
        setLay((l) => ({ ...l, notes: l.notes.map((x) => (x.id === d.id ? next : x)) }))
        void save({ notes: { [d.id]: next } })
        return
      }
      if (d.type !== "card") return
      // A card: into a stack, back to the tray, or onto the canvas.
      const under = document.elementFromPoint(e.clientX, e.clientY)
      const stackEl = under?.closest<HTMLElement>("[data-stack]")
      const id = d.id
      const out = (l: Layout): Layout => ({
        cards: Object.fromEntries(Object.entries(l.cards).filter(([k]) => k !== id)),
        stacks: l.stacks.map((s) => ({ ...s, items: s.items.filter((x) => x !== id) })),
        notes: l.notes,
      })
      if (stackEl?.dataset.stack) {
        const sid = stackEl.dataset.stack
        const st = lay.stacks.find((s) => s.id === sid)
        if (!st || d.from === sid) return
        const next = { ...st, items: [...st.items.filter((x) => x !== id), id] }
        setLay((l) => {
          const o = out(l)
          return { ...o, stacks: o.stacks.map((s) => (s.id === sid ? next : s)) }
        })
        void save({ stacks: { [sid]: next } }).then(() => bindDrop(next, id).then(changed))
        return
      }
      if (under?.closest(".work-tray")) {
        if (d.from === "tray") return
        setLay(out)
        if (d.from === "canvas") void save({ cards: { [id]: null } })
        else {
          const st = lay.stacks.find((s) => s.id === d.from)
          if (st)
            void save({ stacks: { [st.id]: { ...st, items: st.items.filter((x) => x !== id) } } })
        }
        return
      }
      if (under?.closest(".work-canvas")) {
        setLay((l) => {
          const o = out(l)
          return { ...o, cards: { ...o.cards, [id]: pos } }
        })
        void save({ cards: { [id]: pos } })
      }
    }
    window.addEventListener("pointermove", move)
    window.addEventListener("pointerup", up)
    return () => {
      window.removeEventListener("pointermove", move)
      window.removeEventListener("pointerup", up)
    }
  }, [drag !== null, lay, pan, zoom])

  const grab = (
    e: RPointerEvent,
    d:
      | Omit<Extract<Drag, { type: "card" }>, "dx" | "dy" | "x" | "y" | "moved">
      | { type: "stack" | "note"; id: string },
  ) => {
    if (e.button !== 0) return
    e.stopPropagation()
    // Where it was grabbed, in screen pixels from its corner, so it does not jump.
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
    const box =
      d.type === "stack" || d.type === "note"
        ? ((e.currentTarget as HTMLElement).parentElement?.getBoundingClientRect() ?? r)
        : r
    setDrag({
      ...d,
      dx: e.clientX - box.left,
      dy: e.clientY - box.top,
      x: e.clientX,
      y: e.clientY,
      moved: false,
    } as Drag)
  }

  const live = (d: Drag | null, type: string, id: string) =>
    d && d.type === type && "id" in d && d.id === id && d.moved ? d : null
  const placeOf = (type: "card" | "stack" | "note", id: string, at: WorkPos) => {
    const d = live(drag, type, id)
    if (!d || ("from" in d && d.from !== "canvas")) return at
    const p = toPlane(d.x - d.dx, d.y - d.dy)
    return { x: p.x, y: p.y }
  }

  const cardFace = (c: WorkCard, compact = false) => {
    const k = kinds.get(c.kind)
    const Icon = kindIcon(k?.icon)
    return (
      <>
        <span className="work-card-title">
          <Icon size={12} className="work-kindicon" /> {c.title}
        </span>
        {!compact && (
          <span className="work-card-meta">
            {k?.label ?? c.kind}
            {c.stage ? ` · ${label(c.stage)}` : ""}
            {c.words > 0 ? ` · ${c.words} words` : ""}
          </span>
        )}
      </>
    )
  }

  const addStack = () => {
    const r = plane.current?.getBoundingClientRect()
    const c = toPlane((r?.left ?? 0) + 80, (r?.top ?? 0) + 60)
    const st: WorkStack = {
      id: newId(),
      title: "New stack",
      x: Math.round(c.x),
      y: Math.round(c.y),
      items: [],
    }
    setLay((l) => ({ ...l, stacks: [...l.stacks, st] }))
    setEditStack(st.id)
    void save({ stacks: { [st.id]: st } })
  }
  const addNote = () => {
    const r = plane.current?.getBoundingClientRect()
    const c = toPlane((r?.left ?? 0) + 120, (r?.top ?? 0) + 120)
    const n: WorkCanvasNote = { id: newId(), x: Math.round(c.x), y: Math.round(c.y), text: "" }
    setLay((l) => ({ ...l, notes: [...l.notes, n] }))
    setEditNote(n.id)
    void save({ notes: { [n.id]: n } })
  }
  const zoomBy = (f: number, cx?: number, cy?: number) => {
    const r = plane.current?.getBoundingClientRect()
    const ax = cx ?? (r ? r.left + r.width / 2 : 0)
    const ay = cy ?? (r ? r.top + r.height / 2 : 0)
    const next = Math.min(2.5, Math.max(0.25, zoom * f))
    // Keep the point under the pointer where it is.
    const p = toPlane(ax, ay)
    setPan({ x: ax - (r?.left ?? 0) - p.x * next, y: ay - (r?.top ?? 0) - p.y * next })
    setZoom(next)
  }

  // What a stack can be bound to: the shown kinds' stages, and the items that hold others.
  const stages = [
    ...new Set(space.kinds.filter((k) => shownKinds.includes(k.kind)).flatMap((k) => k.stages)),
  ]
  const parents = space.items.filter((c) => !c.cut && (kinds.get(c.kind)?.children.length ?? 0) > 0)

  const dragged = drag && drag.type === "card" && drag.moved && drag.from !== "canvas" ? drag : null
  const draggedCard = dragged ? byId.get(dragged.id) : undefined

  return (
    <div className="work-boardwrap">
      <div className="work-filter">
        <KindChips
          kinds={space.kinds}
          shown={shownKinds}
          set={async (k) => {
            try {
              await work.saveView(ws, view.id, { kinds: k })
            } catch (e) {
              setError(message(e))
            }
            changed()
          }}
        />
        <span className="work-spacer" />
        <button type="button" className="desk-button" onClick={addStack}>
          <Plus size={12} /> Stack
        </button>
        <button type="button" className="desk-button" onClick={addNote}>
          <StickyNote size={12} /> Note
        </button>
        <span className="work-zoom">
          <button
            type="button"
            className="work-icon"
            title="Zoom out"
            onClick={() => zoomBy(1 / 1.2)}
          >
            <Minus size={13} />
          </button>
          <button
            type="button"
            className="work-link"
            title="Back to 100%"
            onClick={() => {
              setZoom(1)
              setPan({ x: 40, y: 40 })
            }}
          >
            {Math.round(zoom * 100)}%
          </button>
          <button type="button" className="work-icon" title="Zoom in" onClick={() => zoomBy(1.2)}>
            <Plus size={13} />
          </button>
        </span>
      </div>
      <div className="work-cal">
        <div
          ref={plane}
          className="work-canvas"
          data-panning={drag?.type === "pan"}
          onPointerDown={(e) => {
            if (e.button !== 0) return
            // (An open note keeps its text: the click blurs it, and blur saves.)
            setDrag({ type: "pan", sx: e.clientX, sy: e.clientY, px: pan.x, py: pan.y })
          }}
          onWheel={(e) => {
            if (e.metaKey || e.ctrlKey) zoomBy(e.deltaY < 0 ? 1.1 : 1 / 1.1, e.clientX, e.clientY)
            else setPan((p) => ({ x: p.x - e.deltaX, y: p.y - e.deltaY }))
          }}
        >
          <div
            className="work-plane"
            style={{ transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom})` }}
          >
            {lay.notes.map((n) => {
              const at = placeOf("note", n.id, n)
              return (
                <div
                  key={n.id}
                  className="work-cnote"
                  data-dragging={live(drag, "note", n.id) ? true : undefined}
                  style={{ left: at.x, top: at.y }}
                >
                  <div
                    className="work-cnote-bar"
                    onPointerDown={(e) => grab(e, { type: "note", id: n.id })}
                  >
                    <span className="work-spacer" />
                    <button
                      type="button"
                      className="work-icon"
                      title="Take the note off"
                      onPointerDown={(e) => e.stopPropagation()}
                      onClick={() => {
                        setLay((l) => ({ ...l, notes: l.notes.filter((x) => x.id !== n.id) }))
                        void save({ notes: { [n.id]: null } })
                      }}
                    >
                      <X size={11} />
                    </button>
                  </div>
                  {editNote === n.id ? (
                    <NoteText
                      text={n.text}
                      done={(text) => {
                        setEditNote(null)
                        if (text === n.text) return
                        const next = { ...n, text }
                        setLay((l) => ({
                          ...l,
                          notes: l.notes.map((x) => (x.id === n.id ? next : x)),
                        }))
                        void save({ notes: { [n.id]: next } })
                      }}
                    />
                  ) : (
                    // biome-ignore lint/a11y/noStaticElementInteractions: double-click to write
                    <div
                      className="work-cnote-text"
                      data-empty={!n.text}
                      onPointerDown={(e) => e.stopPropagation()}
                      onDoubleClick={() => setEditNote(n.id)}
                      title="Double-click to write"
                    >
                      {n.text || "Double-click to write"}
                    </div>
                  )}
                </div>
              )
            })}
            {lay.stacks.map((st) => {
              const at = placeOf("stack", st.id, st)
              const cards = st.items.map((id) => byId.get(id)).filter(shown)
              return (
                <section
                  key={st.id}
                  className="work-stack"
                  data-stack={st.id}
                  data-over={dragged || (drag?.type === "card" && drag.moved) ? true : undefined}
                  data-dragging={live(drag, "stack", st.id) ? true : undefined}
                  style={{ left: at.x, top: at.y }}
                  onPointerDown={(e) => e.stopPropagation()}
                >
                  <header onPointerDown={(e) => grab(e, { type: "stack", id: st.id })}>
                    {editStack === st.id ? (
                      <input
                        // biome-ignore lint/a11y/noAutofocus: the stack just made, or the title double-clicked
                        autoFocus
                        defaultValue={st.title}
                        onPointerDown={(e) => e.stopPropagation()}
                        onKeyDown={(e) => {
                          if (e.key === "Enter") e.currentTarget.blur()
                          if (e.key === "Escape") setEditStack(null)
                        }}
                        onBlur={(e) => {
                          setEditStack(null)
                          const title = e.target.value.trim() || st.title
                          if (title === st.title) return
                          const next = { ...st, title }
                          setLay((l) => ({
                            ...l,
                            stacks: l.stacks.map((s) => (s.id === st.id ? next : s)),
                          }))
                          void save({ stacks: { [st.id]: next } })
                        }}
                        aria-label="Stack title"
                      />
                    ) : (
                      // biome-ignore lint/a11y/noStaticElementInteractions: double-click to rename
                      <span
                        className="work-stack-title"
                        onDoubleClick={() => setEditStack(st.id)}
                        title="Double-click to rename"
                      >
                        {st.title}
                      </span>
                    )}
                    <span className="work-dim">{cards.length}</span>
                    <span className="work-spacer" />
                    <button
                      type="button"
                      className="work-icon"
                      title="Take the stack off (its cards go back to the tray)"
                      onPointerDown={(e) => e.stopPropagation()}
                      onClick={() => {
                        setLay((l) => ({ ...l, stacks: l.stacks.filter((s) => s.id !== st.id) }))
                        void save({ stacks: { [st.id]: null } })
                      }}
                    >
                      <X size={12} />
                    </button>
                  </header>
                  <select
                    className="work-select work-stack-bind"
                    value={st.bind ? `${st.bind.key}:${st.bind.value}` : ""}
                    onPointerDown={(e) => e.stopPropagation()}
                    onChange={(e) => {
                      const v = e.target.value
                      const i = v.indexOf(":")
                      const bind = v
                        ? { key: v.slice(0, i) as "stage" | "parent", value: v.slice(i + 1) }
                        : undefined
                      const next = { ...st, bind }
                      setLay((l) => ({
                        ...l,
                        stacks: l.stacks.map((s) => (s.id === st.id ? next : s)),
                      }))
                      void save({ stacks: { [st.id]: next } })
                    }}
                    aria-label="Dropping a card here"
                    title="What dropping a card here sets on it"
                  >
                    <option value="">Just a pile</option>
                    {stages.map((s) => (
                      <option key={s} value={`stage:${s}`}>
                        Sets stage: {label(s)}
                      </option>
                    ))}
                    {parents.map((p) => (
                      <option key={p.id} value={`parent:${p.id}`}>
                        Puts in: {p.title}
                      </option>
                    ))}
                  </select>
                  <div className="work-stack-items">
                    {cards.length === 0 && (
                      <p className="work-dim work-stack-empty">Drop cards here</p>
                    )}
                    {cards.map((c) => (
                      <div
                        key={c.id}
                        className="work-card work-ccard"
                        data-picked={picked === c.id}
                        data-color={kinds.get(c.kind)?.color}
                        data-dragging={live(drag, "card", c.id) ? true : undefined}
                        onPointerDown={(e) => grab(e, { type: "card", id: c.id, from: st.id })}
                      >
                        {cardFace(c, true)}
                      </div>
                    ))}
                  </div>
                </section>
              )
            })}
            {Object.entries(lay.cards).map(([id, pos]) => {
              const c = byId.get(id)
              if (!shown(c)) return null
              const at = placeOf("card", id, pos)
              return (
                <div
                  key={id}
                  className="work-card work-ccard work-onplane"
                  data-picked={picked === id}
                  data-color={kinds.get(c.kind)?.color}
                  data-dragging={live(drag, "card", id) ? true : undefined}
                  style={{ left: at.x, top: at.y }}
                  onPointerDown={(e) => grab(e, { type: "card", id, from: "canvas" })}
                >
                  {cardFace(c)}
                </div>
              )
            })}
          </div>
        </div>
        <aside
          className="work-calside work-tray"
          data-over={drag?.type === "card" && drag.moved ? true : undefined}
        >
          <h3>
            Not on the canvas <span>{tray.length}</span>
          </h3>
          {tray.length === 0 && <p className="work-rowtext">Drop a card here to take it off.</p>}
          {tray.map((c) => (
            <div
              key={c.id}
              className="work-calitem"
              data-color={kinds.get(c.kind)?.color}
              data-picked={picked === c.id}
              onPointerDown={(e) => grab(e, { type: "card", id: c.id, from: "tray" })}
              title="Drag onto the canvas or into a stack"
            >
              {(() => {
                const Icon = kindIcon(kinds.get(c.kind)?.icon)
                return <Icon size={11} className="work-kindicon" />
              })()}
              <span className="work-calitem-title">{c.title}</span>
            </div>
          ))}
        </aside>
      </div>
      {dragged && draggedCard && (
        <div
          className="work-card work-ccard work-ghost"
          data-color={kinds.get(draggedCard.kind)?.color}
          style={{ left: dragged.x - dragged.dx, top: dragged.y - dragged.dy }}
        >
          {cardFace(draggedCard)}
        </div>
      )}
    </div>
  )
}

function NoteText({ text, done }: { text: string; done: (t: string) => void }) {
  const [t, setT] = useState(text)
  return (
    <textarea
      // biome-ignore lint/a11y/noAutofocus: the note the owner opened to write
      autoFocus
      className="work-cnote-edit"
      value={t}
      onChange={(e) => setT(e.target.value)}
      onPointerDown={(e) => e.stopPropagation()}
      onBlur={() => done(t)}
      onKeyDown={(e) => {
        if (e.key === "Escape") done(text)
        if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) e.currentTarget.blur()
      }}
      rows={4}
      aria-label="Note"
    />
  )
}
