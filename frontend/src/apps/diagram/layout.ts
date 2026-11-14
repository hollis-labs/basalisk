/**
 * Tidy: a layered layout. Nodes are ranked along the flow (the longest path from a source,
 * edges that close a loop left out), each lane gets its own band of ranks so lanes never
 * overlap, and the nodes of a rank are ordered by where their neighbours are (a few sweeps
 * of the barycentre heuristic) and centred. Left to right ("LR") or top to bottom ("TB").
 */
import { type Diagram, type DNode, nodeSize, parse } from "./model"

export type Direction = "LR" | "TB"

const GAP_RANK = 130 // between ranks (columns in LR)
const GAP_NODE = 60 // between nodes of a rank
const GAP_LANE = 96 // extra room between lanes
const GAP_BAND = 100 // between the bands of a wrapped lane
const WRAP = 3 // slots a lane takes side by side before it wraps

/** Ranks by longest path from the sources; edges that close a cycle are ignored. */
function ranks(nodes: DNode[], edges: { from: string; to: string }[]): Map<string, number> {
  const out = new Map<string, string[]>()
  for (const n of nodes) out.set(n.id, [])
  for (const e of edges)
    if (out.has(e.from) && out.has(e.to) && e.from !== e.to) out.get(e.from)?.push(e.to)
  // Drop back edges (depth-first, in node order).
  const state = new Map<string, 0 | 1 | 2>()
  const forward: [string, string][] = []
  const visit = (id: string) => {
    state.set(id, 1)
    for (const t of out.get(id) ?? []) {
      const s = state.get(t) ?? 0
      if (s === 1) continue // closes a loop
      forward.push([id, t])
      if (s === 0) visit(t)
    }
    state.set(id, 2)
  }
  for (const n of nodes) if (!state.get(n.id)) visit(n.id)
  const rank = new Map(nodes.map((n) => [n.id, 0]))
  // Longest path: relax until nothing moves (the graph is now acyclic).
  for (let i = 0; i < nodes.length; i++) {
    let moved = false
    for (const [a, b] of forward) {
      const r = (rank.get(a) ?? 0) + 1
      if (r > (rank.get(b) ?? 0)) {
        rank.set(b, r)
        moved = true
      }
    }
    if (!moved) break
  }
  return rank
}

/** A new arrangement of the diagram's nodes (lanes, ranks, order), keeping everything else. */
export function tidy(dia: Diagram, dir: Direction = "LR"): Diagram {
  const nodes = dia.nodes
  if (!nodes.length) return dia
  const rank = ranks(nodes, dia.edges)
  // Lanes in the order their nodes come in the flow; nodes in no lane share one, unframed.
  const laneOf = (n: DNode) => n.group ?? "\u0000"
  const laneIds = [...new Set(nodes.map(laneOf))]
  const avg = (lane: string) => {
    const r = nodes.filter((n) => laneOf(n) === lane).map((n) => rank.get(n.id) ?? 0)
    return r.reduce((a, b) => a + b, 0) / r.length
  }
  laneIds.sort((a, b) => avg(a) - avg(b))
  // Each lane gets consecutive ranks: its own ranks, renumbered from where the last ended.
  const slot = new Map<string, number>()
  let next = 0
  for (const lane of laneIds) {
    const members = nodes.filter((n) => laneOf(n) === lane)
    const own = [...new Set(members.map((n) => rank.get(n.id) ?? 0))].sort((a, b) => a - b)
    for (const m of members) slot.set(m.id, next + own.indexOf(rank.get(m.id) ?? 0))
    next += own.length
  }
  // Order within each slot by the barycentre of neighbours (down then up, a few times).
  const bySlot = new Map<number, DNode[]>()
  for (const n of nodes) {
    const s = slot.get(n.id) ?? 0
    bySlot.set(s, [...(bySlot.get(s) ?? []), n])
  }
  const order = new Map<string, number>()
  for (const list of bySlot.values())
    list.forEach((n, i) => {
      order.set(n.id, i)
    })
  const neighbours = new Map<string, string[]>(nodes.map((n) => [n.id, []]))
  for (const e of dia.edges) {
    neighbours.get(e.from)?.push(e.to)
    neighbours.get(e.to)?.push(e.from)
  }
  const slots = [...bySlot.keys()].sort((a, b) => a - b)
  for (let pass = 0; pass < 4; pass++) {
    const seq = pass % 2 === 0 ? slots : [...slots].reverse()
    for (const s of seq) {
      const list = bySlot.get(s) ?? []
      const key = (n: DNode) => {
        const ns = (neighbours.get(n.id) ?? []).filter((m) => slot.get(m) !== s)
        if (!ns.length) return order.get(n.id) ?? 0
        return ns.reduce((a, m) => a + (order.get(m) ?? 0), 0) / ns.length
      }
      list.sort((a, b) => key(a) - key(b))
      list.forEach((n, i) => {
        order.set(n.id, i)
      })
    }
  }
  // Places. Each lane takes up to WRAP slots side by side (columns in LR, rows in TB); a
  // longer lane wraps into further bands, every other one running back (a snake), so a long
  // chain stays compact. A slot's nodes are stacked across and centred in their band.
  const along = (n: DNode) => (dir === "LR" ? nodeSize(n).w : nodeSize(n).h)
  const across = (n: DNode) => (dir === "LR" ? nodeSize(n).h : nodeSize(n).w)
  const span = (list: DNode[]) => list.reduce((a, n) => a + across(n) + GAP_NODE, -GAP_NODE)
  const placed = new Map<string, { x: number; y: number }>()
  const ORIGIN = 60
  const TOP = 110 // room above for a lane's title
  let lanePos = ORIGIN
  for (const lane of laneIds) {
    const own = slots.filter((s) => laneOf((bySlot.get(s) ?? [])[0]) === lane)
    const chunks: number[][] = []
    for (let i = 0; i < own.length; i += WRAP) chunks.push(own.slice(i, i + WRAP))
    const cols = Math.min(WRAP, own.length)
    // A column's size along the flow: the widest node any band puts there.
    const colSize = Array.from({ length: cols }, (_, c) =>
      Math.max(
        0,
        ...chunks.flatMap((ch, b) => {
          const s = (b % 2 ? [...ch].reverse() : ch)[c]
          return s === undefined ? [] : (bySlot.get(s) ?? []).map(along)
        }),
      ),
    )
    const colStart = colSize.map((_, c) =>
      colSize.slice(0, c).reduce((a, v) => a + v + GAP_RANK, 0),
    )
    let bandPos = TOP + ORIGIN
    for (const [b, ch] of chunks.entries()) {
      const seq = b % 2 ? [...ch].reverse() : ch
      // Reversed bands are right-aligned so the snake turns in place.
      const shift = b % 2 ? cols - seq.length : 0
      const bandSpan = Math.max(...seq.map((s) => span(bySlot.get(s) ?? [])))
      for (const [i, s] of seq.entries()) {
        const list = bySlot.get(s) ?? []
        const c = i + shift
        let a = bandPos + (bandSpan - span(list)) / 2
        for (const n of list) {
          const off = lanePos + colStart[c] + (colSize[c] - along(n)) / 2
          placed.set(n.id, dir === "LR" ? { x: off, y: a } : { x: a, y: off })
          a += across(n) + GAP_NODE
        }
      }
      bandPos += bandSpan + GAP_BAND
    }
    lanePos += colStart[cols - 1] + colSize[cols - 1] + GAP_LANE + GAP_RANK / 2
  }
  return {
    ...dia,
    nodes: nodes.map((n) => ({ ...n, ...(placed.get(n.id) ?? {}) })),
    // Sides chosen by hand may not suit the new places.
    edges: dia.edges.map(({ fromSide: _f, toSide: _t, ...e }) => e),
  }
}

/** A diagram from its JSON: as parse reads it, laid out when none of its nodes has a place. */
export function fromBody(text: string): Diagram {
  const d = parse(text)
  let raw: { nodes?: { x?: unknown; y?: unknown }[] } = {}
  try {
    raw = JSON.parse(text || "{}")
  } catch {
    return d
  }
  const nodes = Array.isArray(raw.nodes) ? raw.nodes : []
  const unplaced =
    nodes.length > 0 &&
    nodes.every((n) => !Number.isFinite(Number(n?.x)) || !Number.isFinite(Number(n?.y)))
  return unplaced ? tidy(d) : d
}
