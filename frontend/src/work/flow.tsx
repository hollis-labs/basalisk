/**
 * Flow: the items as nodes and their links as edges, drawn by the Diagram's renderer (lanes,
 * cards, routed edges, the legend), for plot lines, branching scenes and dependencies. The
 * view's relations (rel) are the edges; each relation has its own line, which the legend
 * names. Drag a node to place it (kept in the view's layout, like the canvas); drag from the
 * dot on a selected node to another node to link them with the chosen relation (the link is
 * kept on the source item); select an edge and press Delete to take the link out. Lanes can
 * group the nodes by kind, by stage or by parent. Tidy lays it all out.
 */
import { Maximize, Minus, Plus, Trash2 } from "lucide-react"
import {
  type PointerEvent as RPointerEvent,
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react"
import { type WorkCard, type WorkSpace, type WorkView, work } from "../api/work"
import { type Direction, tidy } from "../apps/diagram/layout"
import type { DEdge, DGroup, Diagram, DNode, EdgeStyle, Tone } from "../apps/diagram/model"
import { boxOf } from "../apps/diagram/model"
import { Defs, extent, LEGEND_H, laneHeads, legendOf, Scene } from "../apps/diagram/render"
import { routeAll } from "../apps/diagram/route"
import { paletteFor } from "../apps/diagram/theme"
import { KindChips } from "./chips"
import { label, message } from "./util"

/** The relations a flow can draw, the line each gets and what the legend calls it. */
const RELS: { rel: string; style: EdgeStyle; name: string }[] = [
  { rel: "leads-to", style: "flow", name: "leads to" },
  { rel: "depends-on", style: "feedback", name: "depends on" },
  { rel: "appears-in", style: "data", name: "appears in" },
  { rel: "uses", style: "human", name: "uses" },
  { rel: "relates", style: "muted", name: "relates" },
]
const DEFAULT_RELS = ["leads-to", "depends-on", "appears-in"]

/** Work kind colours as the Diagram's tones. */
const TONE: Record<string, Tone> = {
  amber: "amber",
  blue: "blue",
  red: "red",
  teal: "teal",
  violet: "violet",
  slate: "slate",
  gray: "slate",
  green: "green",
  orange: "amber",
  rose: "pink",
}

type Lanes = "" | "kind" | "stage" | "parent"
const edgeId = (from: string, rel: string, to: string) => `${from}>${rel}>${to}`

export function Flow({
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
  const uid = `f${useId().replace(/[^a-zA-Z0-9]/g, "")}`
  const kinds = useMemo(() => new Map(space.kinds.map((k) => [k.kind, k])), [space.kinds])
  const shownKinds = view.kinds.length ? view.kinds : space.kinds.map((k) => k.kind)
  const rels = view.rel?.length ? view.rel : DEFAULT_RELS
  const lanes = (
    ["kind", "stage", "parent"].includes(view.group_by ?? "") ? view.group_by : ""
  ) as Lanes
  const [newRel, setNewRel] = useState(rels[0] ?? "leads-to")
  const [moved, setMoved] = useState<Record<string, { x: number; y: number }>>({})
  const [sel, setSel] = useState<{ type: "node" | "edge"; id: string } | null>(null)
  const [wire, setWire] = useState<{ from: string; x: number; y: number } | null>(null)
  const [vp, setVp] = useState({ x: 40, y: 30, k: 1 })
  const svg = useRef<SVGSVGElement>(null)
  const root = useRef<HTMLDivElement>(null)
  const drag = useRef<{
    kind: "pan" | "node"
    id?: string
    sx: number
    sy: number
    ox: number
    oy: number
    moved: boolean
  } | null>(null)
  const [deskTheme, setDeskTheme] = useState(() =>
    document.documentElement.getAttribute("data-desk-theme"),
  )
  useEffect(() => {
    const o = new MutationObserver(() =>
      setDeskTheme(document.documentElement.getAttribute("data-desk-theme")),
    )
    o.observe(document.documentElement, { attributes: true, attributeFilter: ["data-desk-theme"] })
    return () => o.disconnect()
  }, [])
  // biome-ignore lint/correctness/useExhaustiveDependencies: a new desktop theme makes a new palette
  const pal = useMemo(() => paletteFor("app"), [deskTheme])

  // ---- The items as a diagram ----
  // Items with a link of a drawn relation (either end); the others only when asked for.
  const showUnlinked = (view.filter as { unlinked?: boolean } | undefined)?.unlinked === true
  const { items, unlinked } = useMemo(() => {
    const all = space.items.filter((c) => !c.cut && shownKinds.includes(c.kind))
    const ids = new Set(all.map((c) => c.id))
    const linked = new Set<string>()
    for (const c of all)
      for (const l of c.links)
        if (rels.includes(l.rel) && ids.has(l.to)) {
          linked.add(c.id)
          linked.add(l.to)
        }
    return {
      items: showUnlinked ? all : all.filter((c) => linked.has(c.id)),
      unlinked: all.filter((c) => !linked.has(c.id)).length,
    }
  }, [space.items, shownKinds, rels, showUnlinked])
  const saved = useMemo(() => {
    const cards = (view.layout as { cards?: Record<string, { x: number; y: number }> } | undefined)
      ?.cards
    return cards && typeof cards === "object" ? cards : {}
  }, [view.layout])
  const dia: Diagram = useMemo(() => {
    const byId = new Map(items.map((c) => [c.id, c]))
    const groupOf = (c: WorkCard): string | undefined =>
      lanes === "kind"
        ? c.kind
        : lanes === "stage"
          ? c.stage || "no stage"
          : lanes === "parent"
            ? c.parent || undefined
            : undefined
    const groups: DGroup[] = []
    if (lanes)
      for (const c of items) {
        const g = groupOf(c)
        if (!g || groups.some((x) => x.id === g)) continue
        groups.push({
          id: g,
          label:
            lanes === "kind"
              ? (kinds.get(g)?.plural ?? label(g))
              : lanes === "parent"
                ? (space.items.find((x) => x.id === g)?.title ?? g)
                : label(g),
        })
      }
    const nodes: DNode[] = items.map((c) => {
      const k = kinds.get(c.kind)
      return {
        id: c.id,
        label: c.title,
        kind: "step",
        icon: k?.icon,
        tone: TONE[k?.color ?? ""] ?? "slate",
        subtitle: [k?.label ?? c.kind, c.stage ? label(c.stage) : ""].filter(Boolean).join(" · "),
        group: groupOf(c),
        x: Number.NaN,
        y: Number.NaN,
      }
    })
    const edges: DEdge[] = []
    for (const c of items)
      for (const l of c.links) {
        const r = RELS.find((x) => x.rel === l.rel)
        if (!r || !rels.includes(l.rel) || !byId.has(l.to)) continue
        edges.push({ id: edgeId(c.id, l.rel, l.to), from: c.id, to: l.to, style: r.style })
      }
    const legend: Partial<Record<EdgeStyle, string>> = {}
    for (const r of RELS) if (rels.includes(r.rel)) legend[r.style] = r.name
    const draft: Diagram = { diagram: "v1", groups, nodes, edges, legend }
    // Places: the view's, else the layout's (so a new item lands somewhere sensible).
    const auto = new Map(
      tidy({ ...draft, nodes: nodes.map((n) => ({ ...n, x: 0, y: 0 })) }).nodes.map((n) => [
        n.id,
        n,
      ]),
    )
    draft.nodes = nodes.map((n) => {
      const p = moved[n.id] ?? saved[n.id] ?? auto.get(n.id) ?? { x: 0, y: 0 }
      return { ...n, x: p.x, y: p.y }
    })
    return draft
  }, [items, kinds, lanes, rels, saved, moved, space.items])
  const routes = useMemo(() => routeAll(dia.nodes, dia.edges, laneHeads(dia)), [dia])

  // ---- Keeping places, links ----
  const savePlaces = async (cards: Record<string, { x: number; y: number }>) => {
    try {
      await work.layout(ws, view.id, { cards })
      setError("")
    } catch (e) {
      setError(message(e))
    }
    changed()
  }
  const relink = async (
    from: string,
    change: (links: { rel: string; to: string }[]) => { rel: string; to: string }[],
  ) => {
    try {
      const d = await work.item(ws, from)
      const next = change(d.links)
      if (JSON.stringify(next) === JSON.stringify(d.links)) return
      await work.edit(ws, from, { base_rev: d.rev, links: next })
      setError("")
    } catch (e) {
      setError(message(e))
    }
    changed()
  }
  const removeEdge = (id: string) => {
    const [from, rel, to] = id.split(">")
    setSel(null)
    void relink(from, (ls) => ls.filter((l) => !(l.rel === rel && l.to === to)))
  }

  // ---- Pointer: pan, move, link ----
  const at = (e: { clientX: number; clientY: number }) => {
    const box = svg.current?.getBoundingClientRect()
    return {
      x: (e.clientX - (box?.left ?? 0) - vp.x) / vp.k,
      y: (e.clientY - (box?.top ?? 0) - vp.y) / vp.k,
    }
  }
  const down = (e: RPointerEvent<SVGSVGElement>) => {
    if (e.button !== 0) return
    root.current?.focus({ preventScroll: true })
    const t = e.target as Element
    const node = t.closest<SVGGElement>("[data-node]")
    const edge = t.closest<SVGGElement>("[data-edge]")
    e.currentTarget.setPointerCapture(e.pointerId)
    if (t.closest("[data-handle]") && node) {
      const p = at(e)
      setWire({ from: node.dataset.node ?? "", x: p.x, y: p.y })
      return
    }
    if (node) {
      const id = node.dataset.node ?? ""
      const n = dia.nodes.find((x) => x.id === id)
      setSel({ type: "node", id })
      if (n)
        drag.current = {
          kind: "node",
          id,
          sx: e.clientX,
          sy: e.clientY,
          ox: n.x,
          oy: n.y,
          moved: false,
        }
      return
    }
    if (edge) return setSel({ type: "edge", id: edge.dataset.edge ?? "" })
    setSel(null)
    drag.current = { kind: "pan", sx: e.clientX, sy: e.clientY, ox: vp.x, oy: vp.y, moved: false }
  }
  const move = (e: RPointerEvent<SVGSVGElement>) => {
    if (wire) {
      const p = at(e)
      return setWire({ ...wire, x: p.x, y: p.y })
    }
    const d = drag.current
    if (!d) return
    const dx = e.clientX - d.sx
    const dy = e.clientY - d.sy
    if (Math.abs(dx) + Math.abs(dy) > 3) d.moved = true
    if (d.kind === "pan") setVp((v) => ({ ...v, x: d.ox + dx, y: d.oy + dy }))
    else if (d.id && d.moved) {
      const id = d.id
      setMoved((m) => ({ ...m, [id]: { x: d.ox + dx / vp.k, y: d.oy + dy / vp.k } }))
    }
  }
  const up = (e: RPointerEvent<SVGSVGElement>) => {
    if (wire) {
      const p = at(e)
      const to = dia.nodes.find((n) => {
        const b = boxOf(n)
        return (
          n.id !== wire.from && p.x >= b.x && p.x <= b.x + b.w && p.y >= b.y && p.y <= b.y + b.h
        )
      })
      const from = wire.from
      setWire(null)
      if (to)
        void relink(from, (ls) =>
          ls.some((l) => l.rel === newRel && l.to === to.id)
            ? ls
            : [...ls, { rel: newRel, to: to.id }],
        )
      return
    }
    const d = drag.current
    drag.current = null
    if (d?.kind === "node" && d.id) {
      if (!d.moved) pick(d.id)
      else {
        const p = moved[d.id]
        if (p) void savePlaces({ [d.id]: { x: Math.round(p.x), y: Math.round(p.y) } })
      }
    }
  }
  // Wheel: a trackpad pans, a wheel or ⌘/Ctrl zooms (a native listener: React's is passive).
  useEffect(() => {
    const el = svg.current
    if (!el) return
    const onWheel = (e: WheelEvent) => {
      e.preventDefault()
      const box = el.getBoundingClientRect()
      setVp((v) => {
        if (!e.ctrlKey && !e.metaKey && e.deltaMode === 0 && !Number.isInteger(e.deltaY))
          return { ...v, x: v.x - e.deltaX, y: v.y - e.deltaY }
        const k = Math.min(2.5, Math.max(0.2, v.k * (e.deltaY < 0 ? 1.1 : 1 / 1.1)))
        const px = e.clientX - box.left
        const py = e.clientY - box.top
        return { k, x: px - ((px - v.x) / v.k) * k, y: py - ((py - v.y) / v.k) * k }
      })
    }
    el.addEventListener("wheel", onWheel, { passive: false })
    return () => el.removeEventListener("wheel", onWheel)
  })
  const fit = useCallback(() => {
    const box = svg.current?.getBoundingClientRect()
    if (!box || !dia.nodes.length) return
    const ext = extent(dia)
    const h = ext.h + (legendOf(dia).length ? LEGEND_H + 44 : 0)
    const k = Math.min(
      1.2,
      Math.max(0.2, Math.min((box.width - 80) / ext.w, (box.height - 80) / h)),
    )
    setVp({
      k,
      x: (box.width - ext.w * k) / 2 - ext.x * k,
      y: (box.height - h * k) / 2 - ext.y * k,
    })
  }, [dia])
  const fitted = useRef("")
  useEffect(() => {
    if (fitted.current !== view.id && dia.nodes.length) {
      fitted.current = view.id
      requestAnimationFrame(fit)
    }
  }, [view.id, dia.nodes.length, fit])
  // A read-back that moved a node replaces this window's own move.
  // biome-ignore lint/correctness/useExhaustiveDependencies: a newer view revision resets local moves
  useEffect(() => setMoved({}), [view.rev])

  const saveView = async (set: Record<string, unknown>) => {
    try {
      await work.saveView(ws, view.id, set)
    } catch (e) {
      setError(message(e))
    }
    changed()
  }
  const tidyNow = (dir: Direction) => {
    const laid = tidy(dia, dir)
    const cards: Record<string, { x: number; y: number }> = {}
    for (const n of laid.nodes) cards[n.id] = { x: Math.round(n.x), y: Math.round(n.y) }
    setMoved(cards)
    void savePlaces(cards).then(() => requestAnimationFrame(fit))
  }

  const scenesSel =
    sel ??
    (picked && dia.nodes.some((n) => n.id === picked)
      ? { type: "node" as const, id: picked }
      : null)

  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: the view's own keys (Delete, Esc)
    <div
      ref={root}
      className="work-boardwrap work-flow"
      tabIndex={-1}
      onKeyDown={(e) => {
        const tag = (e.target as HTMLElement).tagName
        if (tag === "INPUT" || tag === "SELECT" || tag === "TEXTAREA") return
        if ((e.key === "Delete" || e.key === "Backspace") && sel?.type === "edge") {
          e.preventDefault()
          removeEdge(sel.id)
        } else if (e.key === "Escape") setSel(null)
      }}
    >
      <div className="work-filter">
        <KindChips
          kinds={space.kinds}
          shown={shownKinds}
          set={(k) => void saveView({ kinds: k })}
        />
        <span className="work-spacer" />
      </div>
      <div className="work-filter work-flowbar">
        <span className="work-dim">Edges</span>
        {RELS.map((r) => {
          const on = rels.includes(r.rel)
          return (
            <button
              key={r.rel}
              type="button"
              className="work-chip"
              data-on={on}
              onClick={() =>
                void saveView({ rel: on ? rels.filter((x) => x !== r.rel) : [...rels, r.rel] })
              }
            >
              {r.name}
            </button>
          )
        })}
        <button
          type="button"
          className="work-chip"
          data-on={showUnlinked}
          title={
            showUnlinked
              ? "Hide items with no links drawn here"
              : "Show items with no links drawn here, to link them"
          }
          onClick={() =>
            void saveView({ filter: { ...(view.filter ?? {}), unlinked: !showUnlinked } })
          }
        >
          unlinked ({unlinked})
        </button>
        <span className="work-spacer" />
        <label className="work-dim">
          New links:{" "}
          <select
            className="work-select"
            value={newRel}
            onChange={(e) => setNewRel(e.target.value)}
            aria-label="Relation for new links"
          >
            {RELS.map((r) => (
              <option key={r.rel} value={r.rel}>
                {r.name}
              </option>
            ))}
          </select>
        </label>
        <select
          className="work-select"
          value={lanes}
          onChange={(e) => void saveView({ group_by: e.target.value || null })}
          aria-label="Lanes"
        >
          <option value="">No lanes</option>
          <option value="kind">Lanes by kind</option>
          <option value="stage">Lanes by stage</option>
          <option value="parent">Lanes by parent</option>
        </select>
        <button
          type="button"
          className="desk-button"
          onClick={() => tidyNow("LR")}
          title="Lay it out left to right"
        >
          Tidy →
        </button>
        <button
          type="button"
          className="desk-button"
          onClick={() => tidyNow("TB")}
          title="Lay it out top to bottom"
        >
          Tidy ↓
        </button>
      </div>
      <div className="work-flowstage">
        <svg
          ref={svg}
          className="work-flowcanvas"
          style={{ background: pal.bg }}
          role="img"
          aria-label="Flow"
          onPointerDown={down}
          onPointerMove={move}
          onPointerUp={up}
        >
          <Defs uid={uid} pal={pal} grid="lines" />
          <g transform={`translate(${vp.x} ${vp.y}) scale(${vp.k})`}>
            <rect x={-50000} y={-50000} width={100000} height={100000} fill={`url(#${uid}-grid)`} />
            <Scene
              dia={dia}
              pal={pal}
              uid={uid}
              routes={routes}
              sel={scenesSel}
              handleOn={scenesSel?.type === "node" ? scenesSel.id : undefined}
            />
            {wire &&
              (() => {
                const a = dia.nodes.find((n) => n.id === wire.from)
                if (!a) return null
                const b = boxOf(a)
                return (
                  <line
                    x1={b.x + b.w}
                    y1={b.y + b.h / 2}
                    x2={wire.x}
                    y2={wire.y}
                    stroke={pal.select}
                    strokeWidth={2}
                    strokeDasharray="5 4"
                  />
                )
              })()}
          </g>
        </svg>
        {!dia.nodes.length && (
          <p className="work-empty work-flowhint">Nothing to draw: choose kinds above.</p>
        )}
        {dia.nodes.length > 0 && !dia.edges.length && (
          <p className="work-flowhint">
            No links yet: select a node, then drag from its dot to another node.
          </p>
        )}
        {sel?.type === "edge" && (
          <div className="work-flowedge">
            <span>
              {(() => {
                const [from, rel, to] = sel.id.split(">")
                const t = (id: string) => items.find((c) => c.id === id)?.title ?? id
                return `${t(from)} ${RELS.find((r) => r.rel === rel)?.name ?? rel} ${t(to)}`
              })()}
            </span>
            <button type="button" className="desk-button" onClick={() => removeEdge(sel.id)}>
              <Trash2 size={12} /> Remove link
            </button>
          </div>
        )}
        <div className="work-zoom work-flowzoom">
          <button
            type="button"
            className="work-icon"
            title="Zoom out"
            aria-label="Zoom out"
            onClick={() => setVp((v) => ({ ...v, k: Math.max(0.2, v.k / 1.2) }))}
          >
            <Minus size={13} />
          </button>
          <span className="work-dim">{Math.round(vp.k * 100)}%</span>
          <button
            type="button"
            className="work-icon"
            title="Zoom in"
            aria-label="Zoom in"
            onClick={() => setVp((v) => ({ ...v, k: Math.min(2.5, v.k * 1.2) }))}
          >
            <Plus size={13} />
          </button>
          <button type="button" className="work-icon" title="Fit" aria-label="Fit" onClick={fit}>
            <Maximize size={13} />
          </button>
        </div>
      </div>
    </div>
  )
}
