/**
 * A diagram as it is kept (the body of a collab "diagram" document): nodes, edges and
 * groups, as plain JSON the agent can read and write. Positions are part of it, so an
 * arrangement made by hand is kept; a node written without one is placed here.
 */
export type NodeKind = "service" | "store" | "queue" | "client" | "external" | "actor" | "note"
export const KINDS: NodeKind[] = [
  "service",
  "store",
  "queue",
  "client",
  "external",
  "actor",
  "note",
]

export interface DNode {
  id: string
  label: string
  kind?: NodeKind
  group?: string
  note?: string
  x: number
  y: number
}
export interface DEdge {
  id: string
  from: string
  to: string
  label?: string
}
export interface DGroup {
  id: string
  label: string
}
export interface Diagram {
  diagram: "v1"
  nodes: DNode[]
  edges: DEdge[]
  groups: DGroup[]
}

export const NODE_W = 156
export const NODE_H = 54

export const empty = (): Diagram => ({ diagram: "v1", nodes: [], edges: [], groups: [] })

/** Read a diagram's JSON, forgiving what an author left out (ids, positions, lists). */
export function parse(text: string): Diagram {
  let raw: Partial<Diagram> = {}
  try {
    raw = JSON.parse(text || "{}") as Partial<Diagram>
  } catch {
    return empty()
  }
  const seen = new Set<string>()
  const nodes: DNode[] = []
  const unplaced: DNode[] = []
  for (const n of Array.isArray(raw.nodes) ? raw.nodes : []) {
    if (!n || typeof n !== "object") continue
    const id = String(n.id ?? n.label ?? "").trim()
    if (!id || seen.has(id)) continue
    seen.add(id)
    const node: DNode = {
      id,
      label: String(n.label ?? id),
      kind: KINDS.includes(n.kind as NodeKind) ? n.kind : undefined,
      group: n.group ? String(n.group) : undefined,
      note: n.note ? String(n.note) : undefined,
      x: Number(n.x),
      y: Number(n.y),
    }
    if (Number.isFinite(node.x) && Number.isFinite(node.y)) nodes.push(node)
    else unplaced.push(node)
  }
  // Nodes with no place go in rows under what is already placed.
  const floor = nodes.length ? Math.max(...nodes.map((n) => n.y)) + NODE_H + 60 : 60
  unplaced.forEach((n, i) => {
    n.x = 60 + (i % 4) * (NODE_W + 70)
    n.y = floor + Math.floor(i / 4) * (NODE_H + 70)
    nodes.push(n)
  })
  const edges: DEdge[] = []
  const edgeIds = new Set<string>()
  for (const e of Array.isArray(raw.edges) ? raw.edges : []) {
    if (!e || !seen.has(String(e.from)) || !seen.has(String(e.to))) continue
    let id = String(e.id ?? `${e.from}->${e.to}`)
    while (edgeIds.has(id)) id += "'"
    edgeIds.add(id)
    edges.push({
      id,
      from: String(e.from),
      to: String(e.to),
      label: e.label ? String(e.label) : undefined,
    })
  }
  const groups: DGroup[] = (Array.isArray(raw.groups) ? raw.groups : [])
    .filter((g) => g?.id)
    .map((g) => ({ id: String(g.id), label: String(g.label ?? g.id) }))
  // A group named only on a node still exists.
  for (const n of nodes)
    if (n.group && !groups.some((g) => g.id === n.group))
      groups.push({ id: n.group, label: n.group })
  return { diagram: "v1", nodes, edges, groups }
}

/** The JSON as it is saved: stable key order and whole-number places, so revisions compare cleanly. */
export function serialize(d: Diagram): string {
  const clean = {
    diagram: "v1",
    groups: d.groups.filter((g) => d.nodes.some((n) => n.group === g.id)),
    nodes: d.nodes.map((n) => ({
      id: n.id,
      label: n.label,
      ...(n.kind ? { kind: n.kind } : {}),
      ...(n.group ? { group: n.group } : {}),
      ...(n.note ? { note: n.note } : {}),
      x: Math.round(n.x),
      y: Math.round(n.y),
    })),
    edges: d.edges.map((e) => ({
      id: e.id,
      from: e.from,
      to: e.to,
      ...(e.label ? { label: e.label } : {}),
    })),
  }
  return `${JSON.stringify(clean, null, 1)}\n`
}

/** A new id that reads like the label and is not taken. */
export function freshId(label: string, taken: (id: string) => boolean): string {
  const base =
    label
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 24) || "node"
  let id = base
  for (let n = 2; taken(id); n++) id = `${base}-${n}`
  return id
}

export type Change = "added" | "removed" | "changed" | "moved" | "same"

/** How each node and edge of `after` differs from `before` (and what `before` had that is gone). */
export function compare(before: Diagram, after: Diagram) {
  const nodes = new Map<string, Change>()
  const edges = new Map<string, Change>()
  const was = new Map(before.nodes.map((n) => [n.id, n]))
  for (const n of after.nodes) {
    const o = was.get(n.id)
    if (!o) nodes.set(n.id, "added")
    else if (
      o.label !== n.label ||
      o.kind !== n.kind ||
      o.group !== n.group ||
      (o.note ?? "") !== (n.note ?? "")
    )
      nodes.set(n.id, "changed")
    else if (Math.round(o.x) !== Math.round(n.x) || Math.round(o.y) !== Math.round(n.y))
      nodes.set(n.id, "moved")
    else nodes.set(n.id, "same")
  }
  const goneNodes = before.nodes.filter((n) => !after.nodes.some((x) => x.id === n.id))
  for (const n of goneNodes) nodes.set(n.id, "removed")
  const key = (e: DEdge) => `${e.from}\u0000${e.to}`
  const wasEdge = new Map(before.edges.map((e) => [key(e), e]))
  for (const e of after.edges) {
    const o = wasEdge.get(key(e))
    edges.set(e.id, !o ? "added" : (o.label ?? "") !== (e.label ?? "") ? "changed" : "same")
  }
  const goneEdges = before.edges.filter((e) => !after.edges.some((x) => key(x) === key(e)))
  for (const e of goneEdges) edges.set(e.id, "removed")
  return { nodes, edges, goneNodes, goneEdges }
}

/** Where the line from a node's centre towards a point leaves the node's box. */
export function edgePoint(n: DNode, towardX: number, towardY: number) {
  const cx = n.x + NODE_W / 2
  const cy = n.y + NODE_H / 2
  const dx = towardX - cx
  const dy = towardY - cy
  if (!dx && !dy) return { x: cx, y: cy }
  const scale = Math.min(NODE_W / 2 / Math.abs(dx || 1e-9), NODE_H / 2 / Math.abs(dy || 1e-9))
  return { x: cx + dx * scale, y: cy + dy * scale }
}
