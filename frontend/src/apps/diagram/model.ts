/**
 * A diagram as it is kept (the body of a collab "diagram" document): nodes, edges and
 * groups, as plain JSON the agent can read and write. Positions are part of it, so an
 * arrangement made by hand is kept; a node written without one is placed here.
 *
 * Everything past id, label, kind, group, note, x and y is optional presentation: a
 * subtitle, an icon, a tone, a step number, a tag, inner rows, a highlight; a lane's
 * subtitle and tone; an edge's style (which the legend explains); the diagram's theme.
 * A diagram written with none of it draws as before.
 */
import { SANS, wrap } from "./text"

export type NodeKind =
  | "service"
  | "store"
  | "queue"
  | "client"
  | "external"
  | "actor"
  | "agent"
  | "step"
  | "decision"
  | "start"
  | "end"
  | "note"
export const KINDS: NodeKind[] = [
  "service",
  "store",
  "queue",
  "client",
  "external",
  "actor",
  "agent",
  "step",
  "decision",
  "start",
  "end",
  "note",
]

export type Tone = "blue" | "amber" | "red" | "green" | "violet" | "teal" | "pink" | "slate"
export const TONES: Tone[] = ["blue", "amber", "red", "green", "violet", "teal", "pink", "slate"]

/** Edge styles; the legend says what each means (a diagram may rename them). */
export type EdgeStyle = "flow" | "human" | "feedback" | "repair" | "async" | "data" | "muted"
export const STYLES: EdgeStyle[] = ["flow", "human", "feedback", "repair", "async", "data", "muted"]
export const STYLE_LABEL: Record<EdgeStyle, string> = {
  flow: "flow",
  human: "human decision",
  feedback: "feedback",
  repair: "repair loop",
  async: "async",
  data: "data",
  muted: "secondary",
}

export type Side = "top" | "right" | "bottom" | "left"
export const SIDES: Side[] = ["top", "right", "bottom", "left"]

export type Theme = "dark" | "light" | "neon"
export const THEMES: Theme[] = ["dark", "light", "neon"]

export interface DNode {
  id: string
  label: string
  kind?: NodeKind
  group?: string
  note?: string
  /** A second line under the label (monospace, muted). */
  subtitle?: string
  /** A lucide icon name (see ICON_NAMES); a kind has a default. */
  icon?: string
  tone?: Tone
  /** A step number, drawn as a badge. */
  step?: number
  /** A small pill in the corner ("RETRY", "BETA"). */
  tag?: string
  /** Inner rows ("FIX BATCH 1"). */
  items?: string[]
  highlight?: boolean
  /** A wider card than the default. */
  w?: number
  x: number
  y: number
}
export interface DEdge {
  id: string
  from: string
  to: string
  label?: string
  style?: EdgeStyle
  /** The sides it leaves and enters by; chosen from the places when not given. */
  fromSide?: Side
  toSide?: Side
  both?: boolean
}
export interface DGroup {
  id: string
  label: string
  subtitle?: string
  tone?: Tone
}
export interface Diagram {
  diagram: "v1"
  theme?: Theme
  /** Renamed legend entries, or false for no legend. */
  legend?: Partial<Record<EdgeStyle, string>> | false
  nodes: DNode[]
  edges: DEdge[]
  groups: DGroup[]
}

export const NODE_W = 236
export const NODE_H = 66

export const empty = (): Diagram => ({ diagram: "v1", nodes: [], edges: [], groups: [] })

const str = (v: unknown) => (typeof v === "string" && v.trim() ? v : undefined)
const pick = <T extends string>(v: unknown, of: readonly T[]) =>
  of.includes(v as T) ? (v as T) : undefined

/** Read a diagram's JSON, forgiving what an author left out (ids, positions, lists). */
export function parse(text: string): Diagram {
  let raw: Record<string, unknown> = {}
  try {
    raw = JSON.parse(text || "{}") as Record<string, unknown>
  } catch {
    return empty()
  }
  const seen = new Set<string>()
  const nodes: DNode[] = []
  const unplaced: DNode[] = []
  for (const n of Array.isArray(raw.nodes) ? (raw.nodes as Record<string, unknown>[]) : []) {
    if (!n || typeof n !== "object") continue
    const id = String(n.id ?? n.label ?? "").trim()
    if (!id || seen.has(id)) continue
    seen.add(id)
    const step = Number(n.step)
    const w = Number(n.w)
    const node: DNode = {
      id,
      label: String(n.label ?? id),
      kind: pick(n.kind, KINDS),
      group: n.group ? String(n.group) : undefined,
      note: str(n.note),
      subtitle: str(n.subtitle),
      icon: str(n.icon),
      tone: pick(n.tone, TONES),
      step: Number.isFinite(step) && step > 0 ? Math.round(step) : undefined,
      tag: str(n.tag),
      items: Array.isArray(n.items) ? n.items.map(String).filter(Boolean).slice(0, 12) : undefined,
      highlight: n.highlight === true ? true : undefined,
      w: Number.isFinite(w) && w >= 120 && w <= 600 ? Math.round(w) : undefined,
      x: Number(n.x),
      y: Number(n.y),
    }
    if (node.items && !node.items.length) node.items = undefined
    if (Number.isFinite(node.x) && Number.isFinite(node.y)) nodes.push(node)
    else unplaced.push(node)
  }
  // Nodes with no place go in rows under what is already placed.
  const floor = nodes.length ? Math.max(...nodes.map((n) => n.y + nodeSize(n).h)) + 70 : 60
  unplaced.forEach((n, i) => {
    n.x = 60 + (i % 4) * (NODE_W + 80)
    n.y = floor + Math.floor(i / 4) * (NODE_H + 90)
    nodes.push(n)
  })
  const edges: DEdge[] = []
  const edgeIds = new Set<string>()
  for (const e of Array.isArray(raw.edges) ? (raw.edges as Record<string, unknown>[]) : []) {
    if (!e || !seen.has(String(e.from)) || !seen.has(String(e.to))) continue
    let id = String(e.id ?? `${e.from}->${e.to}`)
    while (edgeIds.has(id)) id += "'"
    edgeIds.add(id)
    edges.push({
      id,
      from: String(e.from),
      to: String(e.to),
      label: str(e.label),
      style: pick(e.style, STYLES),
      fromSide: pick(e.fromSide ?? e.from_side, SIDES),
      toSide: pick(e.toSide ?? e.to_side, SIDES),
      both: e.both === true ? true : undefined,
    })
  }
  const groups: DGroup[] = (
    Array.isArray(raw.groups) ? (raw.groups as Record<string, unknown>[]) : []
  )
    .filter((g) => g?.id)
    .map((g) => ({
      id: String(g.id),
      label: String(g.label ?? g.id),
      subtitle: str(g.subtitle),
      tone: pick(g.tone, TONES),
    }))
  // A group named only on a node still exists.
  for (const n of nodes)
    if (n.group && !groups.some((g) => g.id === n.group))
      groups.push({ id: n.group, label: n.group })
  let legend: Diagram["legend"]
  if (raw.legend === false) legend = false
  else if (raw.legend && typeof raw.legend === "object") {
    const l: Partial<Record<EdgeStyle, string>> = {}
    for (const [k, v] of Object.entries(raw.legend))
      if (STYLES.includes(k as EdgeStyle) && str(v)) l[k as EdgeStyle] = String(v)
    legend = Object.keys(l).length ? l : undefined
  }
  return { diagram: "v1", theme: pick(raw.theme, THEMES), legend, nodes, edges, groups }
}

/** The JSON as it is saved: stable key order and whole-number places, so revisions compare cleanly. */
export function serialize(d: Diagram): string {
  const row = (xs: unknown[]) =>
    xs.length ? `[\n  ${xs.map((x) => JSON.stringify(x)).join(",\n  ")}\n ]` : "[]"
  const groups = d.groups
    .filter((g) => d.nodes.some((n) => n.group === g.id))
    .map((g) => ({
      id: g.id,
      label: g.label,
      ...(g.subtitle ? { subtitle: g.subtitle } : {}),
      ...(g.tone ? { tone: g.tone } : {}),
    }))
  const nodes = d.nodes.map((n) => ({
    id: n.id,
    label: n.label,
    ...(n.kind ? { kind: n.kind } : {}),
    ...(n.group ? { group: n.group } : {}),
    ...(n.subtitle ? { subtitle: n.subtitle } : {}),
    ...(n.icon ? { icon: n.icon } : {}),
    ...(n.tone ? { tone: n.tone } : {}),
    ...(n.step ? { step: n.step } : {}),
    ...(n.tag ? { tag: n.tag } : {}),
    ...(n.items?.length ? { items: n.items } : {}),
    ...(n.highlight ? { highlight: true } : {}),
    ...(n.w ? { w: n.w } : {}),
    ...(n.note ? { note: n.note } : {}),
    x: Math.round(n.x),
    y: Math.round(n.y),
  }))
  const edges = d.edges.map((e) => ({
    id: e.id,
    from: e.from,
    to: e.to,
    ...(e.label ? { label: e.label } : {}),
    ...(e.style ? { style: e.style } : {}),
    ...(e.fromSide ? { fromSide: e.fromSide } : {}),
    ...(e.toSide ? { toSide: e.toSide } : {}),
    ...(e.both ? { both: true } : {}),
  }))
  const head = [`"diagram": "v1"`]
  if (d.theme) head.push(`"theme": ${JSON.stringify(d.theme)}`)
  if (d.legend !== undefined) head.push(`"legend": ${JSON.stringify(d.legend)}`)
  return `{${head.join(", ")},\n "groups": ${row(groups)},\n "nodes": ${row(nodes)},\n "edges": ${row(edges)}}\n`
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

// ---- Sizes: a node's box follows what it shows ----

/** A card's measures: padding, the header with and without a subtitle, an inner row. */
export const CARD = {
  pad: 18,
  head: 66,
  headSub: 84,
  row: 32,
  rowH: 26,
  line: 19,
  lead: 48,
  tag: 24,
}

/** Kinds that draw an icon chip by default (the others have none unless one is named). */
export const KIND_HAS_ICON = new Set<NodeKind>([
  "service",
  "store",
  "queue",
  "client",
  "external",
  "actor",
  "agent",
])

/** Where a card's title starts, and how wide it may be (a step or an icon takes the left). */
export function titleBox(n: DNode, w: number): { x: number; max: number } {
  const lead =
    !!n.step || (n.kind !== "note" && (!!n.icon || KIND_HAS_ICON.has(n.kind ?? "service")))
  const x = lead ? CARD.pad + CARD.lead : CARD.pad
  return { x, max: w - x - CARD.pad }
}
/** The lines a card's title takes (one or two). */
export function titleLines(n: DNode, w: number): number {
  const { max } = titleBox(n, w)
  return wrap(n.label, max, `600 15px ${SANS}`, 2).length
}
export const ITEM_H = CARD.row
/** A node's size: its width (given, or by kind) and a height that holds its lines and rows. */
export function nodeSize(n: DNode): { w: number; h: number } {
  switch (n.kind) {
    case "decision":
      return { w: n.w ?? 172, h: 112 }
    case "start":
    case "end":
      return { w: n.w ?? 172, h: 52 }
    default: {
      const w = n.w ?? NODE_W
      let h =
        (n.subtitle ? CARD.headSub : CARD.head) +
        (titleLines(n, w) - 1) * CARD.line +
        (n.tag ? CARD.tag : 0)
      if (n.items?.length) h += n.items.length * CARD.row + 6
      return { w, h }
    }
  }
}

/** A node's box. */
export const boxOf = (n: DNode) => {
  const s = nodeSize(n)
  return { x: n.x, y: n.y, w: s.w, h: s.h }
}

export type Change = "added" | "removed" | "changed" | "moved" | "same"

const shown = (n: DNode) =>
  JSON.stringify([
    n.label,
    n.kind,
    n.group,
    n.note ?? "",
    n.subtitle,
    n.icon,
    n.tone,
    n.step,
    n.tag,
    n.items,
    n.highlight,
    n.w,
  ])

/** How each node and edge of `after` differs from `before` (and what `before` had that is gone). */
export function compare(before: Diagram, after: Diagram) {
  const nodes = new Map<string, Change>()
  const edges = new Map<string, Change>()
  const was = new Map(before.nodes.map((n) => [n.id, n]))
  for (const n of after.nodes) {
    const o = was.get(n.id)
    if (!o) nodes.set(n.id, "added")
    else if (shown(o) !== shown(n)) nodes.set(n.id, "changed")
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
    edges.set(
      e.id,
      !o
        ? "added"
        : (o.label ?? "") !== (e.label ?? "") || (o.style ?? "") !== (e.style ?? "")
          ? "changed"
          : "same",
    )
  }
  const goneEdges = before.edges.filter((e) => !after.edges.some((x) => key(x) === key(e)))
  for (const e of goneEdges) edges.set(e.id, "removed")
  return { nodes, edges, goneNodes, goneEdges }
}
