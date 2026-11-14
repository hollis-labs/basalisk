/**
 * Edges as right-angled paths between ports on the nodes' sides. The sides face each other
 * unless the edge says which; edges sharing a side are spread along it, ordered by where
 * their other end is, so a fan-in does not pile up. Each edge tries a few orthogonal routes
 * and keeps the one that crosses no other node's box, then the shortest with fewest bends.
 */
import { boxOf, type DEdge, type DNode, type Side } from "./model"

export interface Pt {
  x: number
  y: number
}
export interface Box {
  x: number
  y: number
  w: number
  h: number
}
export interface Route {
  id: string
  points: Pt[]
  from: Pt
  to: Pt
  fromSide: Side
  toSide: Side
  /** Where a label goes: the middle of the longest segment, and whether it runs across. */
  mid: Pt
  horizontal: boolean
}

const STUB = 24
const centre = (b: Box): Pt => ({ x: b.x + b.w / 2, y: b.y + b.h / 2 })
const normal: Record<Side, Pt> = {
  top: { x: 0, y: -1 },
  right: { x: 1, y: 0 },
  bottom: { x: 0, y: 1 },
  left: { x: -1, y: 0 },
}

/** The sides an edge leaves and enters by, from where the two boxes are. */
function sidesFor(a: Box, b: Box): [Side, Side] {
  const gap = 24
  if (b.x >= a.x + a.w + gap) return ["right", "left"]
  if (b.x + b.w + gap <= a.x) return ["left", "right"]
  if (b.y >= a.y + a.h) return ["bottom", "top"]
  if (b.y + b.h <= a.y) return ["top", "bottom"]
  const ca = centre(a)
  const cb = centre(b)
  return Math.abs(cb.x - ca.x) > Math.abs(cb.y - ca.y)
    ? cb.x > ca.x
      ? ["right", "left"]
      : ["left", "right"]
    : cb.y > ca.y
      ? ["bottom", "top"]
      : ["top", "bottom"]
}

/** A point along a side at fraction t (0..1). */
function along(b: Box, s: Side, t: number): Pt {
  switch (s) {
    case "top":
      return { x: b.x + b.w * t, y: b.y }
    case "bottom":
      return { x: b.x + b.w * t, y: b.y + b.h }
    case "left":
      return { x: b.x, y: b.y + b.h * t }
    default:
      return { x: b.x + b.w, y: b.y + b.h * t }
  }
}

function crosses(p: Pt, q: Pt, b: Box, pad = 6): boolean {
  const x0 = b.x - pad
  const x1 = b.x + b.w + pad
  const y0 = b.y - pad
  const y1 = b.y + b.h + pad
  if (p.y === q.y) {
    if (p.y <= y0 || p.y >= y1) return false
    return Math.max(p.x, q.x) > x0 && Math.min(p.x, q.x) < x1
  }
  if (p.x === q.x) {
    if (p.x <= x0 || p.x >= x1) return false
    return Math.max(p.y, q.y) > y0 && Math.min(p.y, q.y) < y1
  }
  return false
}

function cost(pts: Pt[], boxes: Box[], own: Box[], soft: Box[] = []): number {
  let c = 0
  for (let i = 0; i + 1 < pts.length; i++) {
    const p = pts[i]
    const q = pts[i + 1]
    c += Math.abs(p.x - q.x) + Math.abs(p.y - q.y)
    for (const b of boxes) if (crosses(p, q, b)) c += 4000
    // Running along a lane's title is worse than a detour, better than crossing a node.
    for (const b of soft) if (crosses(p, q, b, 0)) c += 700
    // The middle segments must not run back through the edge's own nodes.
    if (i > 0 && i + 2 < pts.length) for (const b of own) if (crosses(p, q, b, 2)) c += 4000
  }
  return c + (pts.length - 2) * 24
}

/** Drop repeated and collinear points. */
function simplify(pts: Pt[]): Pt[] {
  const out: Pt[] = []
  for (const p of pts) {
    const q = out[out.length - 1]
    if (q && Math.abs(q.x - p.x) < 0.5 && Math.abs(q.y - p.y) < 0.5) continue
    out.push({ x: Math.round(p.x * 2) / 2, y: Math.round(p.y * 2) / 2 })
  }
  for (let i = out.length - 2; i > 0; i--) {
    const a = out[i - 1]
    const b = out[i]
    const c = out[i + 1]
    if ((a.x === b.x && b.x === c.x) || (a.y === b.y && b.y === c.y)) out.splice(i, 1)
  }
  return out
}

/** Candidate orthogonal paths from port p (leaving along sp) to port q (entering along sq). */
function candidates(p: Pt, sp: Side, q: Pt, sq: Side, boxes: Box[]): Pt[][] {
  const p1 = { x: p.x + normal[sp].x * STUB, y: p.y + normal[sp].y * STUB }
  const q1 = { x: q.x + normal[sq].x * STUB, y: q.y + normal[sq].y * STUB }
  const xs = new Set([(p1.x + q1.x) / 2, p1.x, q1.x])
  const ys = new Set([(p1.y + q1.y) / 2, p1.y, q1.y])
  // Lines just outside the boxes in the way, to go around them.
  for (const b of boxes) {
    xs.add(b.x - 22)
    xs.add(b.x + b.w + 22)
    ys.add(b.y - 22)
    ys.add(b.y + b.h + 22)
  }
  const out: Pt[][] = [
    [p, p1, { x: q1.x, y: p1.y }, q1, q],
    [p, p1, { x: p1.x, y: q1.y }, q1, q],
  ]
  for (const x of xs) out.push([p, p1, { x, y: p1.y }, { x, y: q1.y }, q1, q])
  for (const y of ys) out.push([p, p1, { x: p1.x, y }, { x: q1.x, y }, q1, q])
  return out
}

/** Route every edge of a diagram; soft boxes (lanes' titles) are avoided when it is cheap. */
export function routeAll(nodes: DNode[], edges: DEdge[], soft: Box[] = []): Map<string, Route> {
  const box = new Map(nodes.map((n) => [n.id, boxOf(n)]))
  const sides = new Map<string, [Side, Side]>()
  // Which edges use each side of each node, to spread their ports.
  const users = new Map<string, { edge: string; end: "from" | "to"; other: Pt }[]>()
  for (const e of edges) {
    const a = box.get(e.from)
    const b = box.get(e.to)
    if (!a || !b) continue
    const auto = sidesFor(a, b)
    const s: [Side, Side] = [e.fromSide ?? auto[0], e.toSide ?? auto[1]]
    sides.set(e.id, s)
    const k1 = `${e.from}:${s[0]}`
    const k2 = `${e.to}:${s[1]}`
    users.set(k1, [...(users.get(k1) ?? []), { edge: e.id, end: "from", other: centre(b) }])
    users.set(k2, [...(users.get(k2) ?? []), { edge: e.id, end: "to", other: centre(a) }])
  }
  const port = new Map<string, Pt>()
  for (const [k, list] of users) {
    const [id, side] = k.split(":") as [string, Side]
    const b = box.get(id)
    if (!b) continue
    const horiz = side === "top" || side === "bottom"
    list.sort((u, v) => (horiz ? u.other.x - v.other.x : u.other.y - v.other.y))
    list.forEach((u, i) => {
      const t = list.length === 1 ? 0.5 : 0.2 + (0.6 * i) / (list.length - 1)
      port.set(`${u.edge}:${u.end}`, along(b, side, t))
    })
  }
  const all = [...box.entries()]
  const out = new Map<string, Route>()
  for (const e of edges) {
    const s = sides.get(e.id)
    const a = box.get(e.from)
    const b = box.get(e.to)
    const p = port.get(`${e.id}:from`)
    const q = port.get(`${e.id}:to`)
    if (!s || !a || !b || !p || !q) continue
    const others = all.filter(([id]) => id !== e.from && id !== e.to).map(([, bx]) => bx)
    const near = others.filter(
      (o) =>
        o.x < Math.max(a.x + a.w, b.x + b.w) + 60 &&
        o.x + o.w > Math.min(a.x, b.x) - 60 &&
        o.y < Math.max(a.y + a.h, b.y + b.h) + 60 &&
        o.y + o.h > Math.min(a.y, b.y) - 60,
    )
    let best: Pt[] = []
    let bestCost = Number.POSITIVE_INFINITY
    for (const c of candidates(p, s[0], q, s[1], [...near, ...soft])) {
      const pts = simplify(c)
      const k = cost(pts, near, [a, b], soft)
      if (k < bestCost) {
        bestCost = k
        best = pts
      }
    }
    let mid = { x: (p.x + q.x) / 2, y: (p.y + q.y) / 2 }
    let horizontal = true
    let longest = -1
    for (let i = 0; i + 1 < best.length; i++) {
      const u = best[i]
      const v = best[i + 1]
      const len = Math.abs(u.x - v.x) + Math.abs(u.y - v.y)
      if (len > longest) {
        longest = len
        mid = { x: (u.x + v.x) / 2, y: (u.y + v.y) / 2 }
        horizontal = u.y === v.y
      }
    }
    out.set(e.id, {
      id: e.id,
      points: best,
      from: p,
      to: q,
      fromSide: s[0],
      toSide: s[1],
      mid,
      horizontal,
    })
  }
  return out
}

/** An SVG path through the points with rounded corners. */
export function pathOf(pts: Pt[], r = 14): string {
  if (pts.length < 2) return ""
  let d = `M${pts[0].x} ${pts[0].y}`
  for (let i = 1; i < pts.length - 1; i++) {
    const a = pts[i - 1]
    const b = pts[i]
    const c = pts[i + 1]
    const l1 = Math.abs(a.x - b.x) + Math.abs(a.y - b.y)
    const l2 = Math.abs(b.x - c.x) + Math.abs(b.y - c.y)
    const k = Math.min(r, l1 / 2, l2 / 2)
    const ux = Math.sign(b.x - a.x)
    const uy = Math.sign(b.y - a.y)
    const vx = Math.sign(c.x - b.x)
    const vy = Math.sign(c.y - b.y)
    d += ` L${b.x - ux * k} ${b.y - uy * k} Q${b.x} ${b.y} ${b.x + vx * k} ${b.y + vy * k}`
  }
  const z = pts[pts.length - 1]
  return `${d} L${z.x} ${z.y}`
}
