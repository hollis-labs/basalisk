/**
 * Outline: the children tree, from the view's root or from the top. Drag a row to reorder it
 * or to move it under another parent (drop on a row's top or bottom edge: before or after it;
 * on its middle: inside it, when its kind holds the row's). Moving changes both parents'
 * children and the item's parent in Caster; the top level's order is kept in the view
 * (layout.order). Word counts are rolled up; cut items stay in the header's cut list.
 */
import { ChevronDown, ChevronRight, Plus } from "lucide-react"
import { type DragEvent, useMemo, useState } from "react"
import { type WorkCard, type WorkKind, type WorkSpace, type WorkView, work } from "../api/work"
import { KindChips } from "./chips"
import { kindIcon, label, message } from "./util"

type Zone = "before" | "inside" | "after"
interface Node {
  card: WorkCard
  parent: string
  kids: Node[]
  words: number
}

export function Outline({
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
  const [closed, setClosed] = useState<Set<string>>(new Set())
  const [over, setOver] = useState<{ id: string; zone: Zone } | null>(null)
  const [dragging, setDragging] = useState<string | null>(null)
  const [making, setMaking] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const { roots, byId } = useMemo(() => {
    const live = space.items.filter((c) => !c.cut && shownKinds.includes(c.kind))
    const byId = new Map(live.map((c) => [c.id, c]))
    const rank = new Map(space.kinds.map((k, i) => [k.kind, i]))
    const build = (c: WorkCard, parent: string, seen: Set<string>): Node => {
      seen.add(c.id)
      // The parent's children list is the order; an item naming this parent but missing from
      // the list goes at the end, never lost.
      const ids = [
        ...c.children,
        ...live.filter((x) => x.parent === c.id && !c.children.includes(x.id)).map((x) => x.id),
      ]
      const kids: Node[] = []
      for (const id of ids) {
        const k = byId.get(id)
        if (k && !seen.has(id)) kids.push(build(k, c.id, seen))
      }
      return { card: c, parent, kids, words: c.words + kids.reduce((n, k) => n + k.words, 0) }
    }
    const seen = new Set<string>()
    let roots: Node[]
    const root = view.root ? byId.get(view.root) : undefined
    if (root) roots = [build(root, root.parent ?? "", seen)]
    else {
      const order = Array.isArray(view.layout?.order) ? (view.layout.order as string[]) : []
      const top = live.filter((c) => !c.parent || !byId.has(c.parent))
      top.sort((a, b) => {
        const ia = order.indexOf(a.id)
        const ib = order.indexOf(b.id)
        if (ia !== ib) return (ia < 0 ? 1e9 : ia) - (ib < 0 ? 1e9 : ib)
        const ra = rank.get(a.kind) ?? 99
        const rb = rank.get(b.kind) ?? 99
        return ra !== rb ? ra - rb : a.title.localeCompare(b.title)
      })
      roots = top.map((c) => build(c, "", seen))
    }
    return { roots, byId }
  }, [space.items, space.kinds, shownKinds, view.root, view.layout])

  const holds = (parent: WorkKind | undefined, kind: string) =>
    !!parent && parent.children.includes(kind)
  const within = (id: string, inside: string): boolean => {
    for (let at: string | undefined = inside, n = 0; at && n < 64; n++) {
      if (at === id) return true
      at = byId.get(at)?.parent
    }
    return false
  }

  const zoneOf = (e: DragEvent, node: Node): Zone => {
    const r = e.currentTarget.getBoundingClientRect()
    const y = (e.clientY - r.top) / r.height
    const dragged = dragging ? byId.get(dragging) : undefined
    const canHold = !!dragged && holds(kinds.get(node.card.kind), dragged.kind)
    if (canHold && y > 0.25 && y < 0.75) return "inside"
    return y < 0.5 ? "before" : "after"
  }

  const drop = async (target: Node, zone: Zone, siblings: Node[]) => {
    const id = dragging
    setOver(null)
    setDragging(null)
    if (!id || id === target.card.id) return
    const c = byId.get(id)
    if (!c) return
    let parent: string
    let before: string
    if (zone === "inside") {
      parent = target.card.id
      before = ""
    } else {
      parent = target.parent
      const i = siblings.findIndex((s) => s.card.id === target.card.id)
      before = zone === "before" ? target.card.id : (siblings[i + 1]?.card.id ?? "")
      if (before === id) return // already there
    }
    if (parent && !holds(kinds.get(byId.get(parent)?.kind ?? ""), c.kind)) {
      const pk = kinds.get(byId.get(parent)?.kind ?? "")
      setError(
        `A ${pk?.label.toLowerCase() ?? "item"} does not hold a ${kinds.get(c.kind)?.label.toLowerCase() ?? c.kind}.`,
      )
      return
    }
    if (parent && within(id, parent)) {
      setError("An item cannot go inside itself.")
      return
    }
    setBusy(true)
    try {
      await work.place(ws, id, parent, before, view.id)
      setError("")
    } catch (e) {
      setError(message(e))
    }
    setBusy(false)
    changed()
  }

  const make = async (kind: string, parent: string) => {
    setMaking(null)
    try {
      const r = await work.make(ws, { kind, parent: parent || undefined })
      if (parent) setClosed((s) => new Set([...s].filter((x) => x !== parent)))
      pick(r.id)
    } catch (e) {
      setError(message(e))
    }
    changed()
  }

  const row = (node: Node, depth: number, siblings: Node[]) => {
    const c = node.card
    const k = kinds.get(c.kind)
    const Icon = kindIcon(k?.icon)
    const open = !closed.has(c.id)
    const room = (k?.children ?? []).filter((x) => shownKinds.includes(x) && kinds.has(x))
    return (
      <li key={c.id}>
        {/* biome-ignore lint/a11y/noStaticElementInteractions: a draggable row and a drop target */}
        <div
          className="work-orow"
          data-picked={picked === c.id}
          data-over={over?.id === c.id ? over.zone : undefined}
          data-color={k?.color}
          style={{ paddingLeft: 6 + depth * 18 }}
          draggable={!busy}
          onDragStart={(e) => {
            e.dataTransfer.setData("text/x-work-item", c.id)
            e.dataTransfer.effectAllowed = "move"
            setDragging(c.id)
          }}
          onDragEnd={() => {
            setDragging(null)
            setOver(null)
          }}
          onDragOver={(e) => {
            if (!dragging || dragging === c.id) return
            e.preventDefault()
            const zone = zoneOf(e, node)
            if (over?.id !== c.id || over.zone !== zone) setOver({ id: c.id, zone })
          }}
          onDragLeave={() => over?.id === c.id && setOver(null)}
          onDrop={(e) => {
            e.preventDefault()
            e.stopPropagation()
            void drop(node, zoneOf(e, node), siblings)
          }}
        >
          <button
            type="button"
            className="work-icon work-otwist"
            disabled={node.kids.length === 0}
            onClick={() =>
              setClosed((s) => {
                const n = new Set(s)
                if (n.has(c.id)) n.delete(c.id)
                else n.add(c.id)
                return n
              })
            }
            title={open ? "Fold" : "Unfold"}
            aria-label={open ? "Fold" : "Unfold"}
          >
            {node.kids.length > 0 &&
              (open ? <ChevronDown size={13} /> : <ChevronRight size={13} />)}
          </button>
          <button type="button" className="work-otitle" onClick={() => pick(c.id)}>
            <Icon size={13} className="work-kindicon" />
            <span className="work-otitle-text">{c.title}</span>
          </button>
          {c.stage && <span className="work-ostage">{label(c.stage)}</span>}
          <span className="work-owords" title="Words, with what it holds">
            {node.words > 0 ? node.words.toLocaleString() : ""}
          </span>
          {room.length > 0 && (
            <button
              type="button"
              className="work-icon work-oadd"
              title={`New ${room.map((x) => kinds.get(x)?.label.toLowerCase()).join(" or ")} in it`}
              onClick={() =>
                room.length === 1
                  ? void make(room[0], c.id)
                  : setMaking(making === c.id ? null : c.id)
              }
            >
              <Plus size={12} />
            </button>
          )}
        </div>
        {making === c.id && (
          <div className="work-makemenu work-omenu" style={{ marginLeft: 24 + depth * 18 }}>
            {room.map((x) => {
              const kk = kinds.get(x)
              const KI = kindIcon(kk?.icon)
              return (
                <button type="button" key={x} onClick={() => void make(x, c.id)}>
                  <KI size={12} /> {kk?.label}
                </button>
              )
            })}
          </div>
        )}
        {open && node.kids.length > 0 && (
          <ul>{node.kids.map((kid) => row(kid, depth + 1, node.kids))}</ul>
        )}
      </li>
    )
  }

  const total = roots.reduce((n, r) => n + r.words, 0)
  const topKinds = space.kinds.filter((k) => shownKinds.includes(k.kind))
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
        <span className="work-owords">{total.toLocaleString()} words</span>
        <button
          type="button"
          className="desk-button"
          onClick={() => setMaking(making === "\u0000top" ? null : "\u0000top")}
        >
          <Plus size={12} /> New
        </button>
      </div>
      {making === "\u0000top" && (
        <div className="work-makemenu work-omenu work-otop">
          {topKinds.map((k) => {
            const KI = kindIcon(k.icon)
            return (
              <button type="button" key={k.kind} onClick={() => void make(k.kind, "")}>
                <KI size={12} /> {k.label}
              </button>
            )
          })}
        </div>
      )}
      <div className="work-outline">
        {roots.length === 0 && (
          <p className="work-empty">
            Nothing here yet. Make a part, a chapter or a scene with New.
          </p>
        )}
        <ul>{roots.map((r) => row(r, 0, roots))}</ul>
        {/* biome-ignore lint/a11y/noStaticElementInteractions: a drop target: the top level's end */}
        <div
          className="work-otail"
          data-over={over?.id === "\u0000tail"}
          onDragOver={(e) => {
            if (!dragging) return
            e.preventDefault()
            if (over?.id !== "\u0000tail") setOver({ id: "\u0000tail", zone: "after" })
          }}
          onDragLeave={() => over?.id === "\u0000tail" && setOver(null)}
          onDrop={async (e) => {
            e.preventDefault()
            const id = dragging
            setOver(null)
            setDragging(null)
            if (!id || view.root) return
            setBusy(true)
            try {
              await work.place(ws, id, "", "", view.id)
              setError("")
            } catch (err) {
              setError(message(err))
            }
            setBusy(false)
            changed()
          }}
        >
          {dragging && !view.root ? "Drop here: to the top level, at the end" : ""}
        </div>
      </div>
    </div>
  )
}
