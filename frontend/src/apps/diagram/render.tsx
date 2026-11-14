/**
 * Drawing a diagram: plain SVG with its colours written in (a theme's palette), so what is
 * on screen is also what an export holds. Lanes (groups) are titled panels around their
 * nodes; nodes are cards with an icon or a step number, a title, a subtitle, a tag and inner
 * rows (or a diamond, a pill, a sticky note, by kind); edges are rounded orthogonal paths
 * with a port where they leave, an arrow where they arrive and a label chip; the legend
 * explains the edge styles in use.
 */
import {
  AlertTriangle,
  Bell,
  Bot,
  Box,
  Brain,
  Calendar,
  CheckCircle2,
  Clock,
  Cloud,
  Code,
  Cpu,
  CreditCard,
  Database,
  Download,
  Eye,
  FileText,
  Filter,
  Flag,
  Folder,
  GitBranch,
  GitMerge,
  GitPullRequest,
  Globe,
  HardDrive,
  Key,
  Layers,
  List,
  Lock,
  type LucideIcon,
  Mail,
  MessageSquare,
  Monitor,
  Network,
  Package,
  Play,
  RefreshCw,
  Rocket,
  Router,
  Search,
  Server,
  Settings,
  Shield,
  Smartphone,
  StickyNote,
  Terminal,
  Upload,
  User,
  Users,
  Workflow,
  Zap,
} from "lucide-react"
import type { ReactNode } from "react"
import {
  boxOf,
  type Change,
  type DEdge,
  type DGroup,
  type Diagram,
  type DNode,
  type EdgeStyle,
  ITEM_H,
  type NodeKind,
  STYLE_LABEL,
  STYLES,
  type Tone,
} from "./model"
import { pathOf, type Route } from "./route"
import { CHANGE_COLOR, PALETTES, type Palette, SHADOW } from "./theme"

export { PALETTES, type Palette }

/** Icons a node may name (lucide names). */
export const ICONS: Record<string, LucideIcon> = {
  "alert-triangle": AlertTriangle,
  bell: Bell,
  bot: Bot,
  box: Box,
  brain: Brain,
  calendar: Calendar,
  check: CheckCircle2,
  clock: Clock,
  cloud: Cloud,
  code: Code,
  cpu: Cpu,
  "credit-card": CreditCard,
  database: Database,
  download: Download,
  eye: Eye,
  "file-text": FileText,
  filter: Filter,
  flag: Flag,
  folder: Folder,
  "git-branch": GitBranch,
  "git-merge": GitMerge,
  "git-pull-request": GitPullRequest,
  globe: Globe,
  "hard-drive": HardDrive,
  key: Key,
  layers: Layers,
  list: List,
  lock: Lock,
  mail: Mail,
  message: MessageSquare,
  monitor: Monitor,
  network: Network,
  package: Package,
  play: Play,
  refresh: RefreshCw,
  rocket: Rocket,
  router: Router,
  search: Search,
  server: Server,
  settings: Settings,
  shield: Shield,
  smartphone: Smartphone,
  "sticky-note": StickyNote,
  terminal: Terminal,
  upload: Upload,
  user: User,
  users: Users,
  workflow: Workflow,
  zap: Zap,
}
export const ICON_NAMES = Object.keys(ICONS).sort()

const KIND_ICON: Partial<Record<NodeKind, string>> = {
  service: "server",
  store: "database",
  queue: "layers",
  client: "monitor",
  external: "cloud",
  actor: "user",
  agent: "bot",
  start: "play",
  end: "flag",
}
const KIND_TONE: Record<NodeKind, Tone> = {
  service: "blue",
  store: "teal",
  queue: "amber",
  client: "violet",
  external: "slate",
  actor: "violet",
  agent: "blue",
  step: "blue",
  decision: "amber",
  start: "green",
  end: "slate",
  note: "amber",
}

export const SANS = '-apple-system, "SF Pro Text", "Inter", "Helvetica Neue", Arial, sans-serif'
export const MONO = '"SF Mono", "JetBrains Mono", "IBM Plex Mono", Menlo, monospace'

/** a mixed into b by t (hex colours). */
export function mix(a: string, b: string, t: number): string {
  const p = (h: string) => {
    const s = h.replace("#", "")
    const f =
      s.length === 3
        ? s
            .split("")
            .map((c) => c + c)
            .join("")
        : s
    return [0, 2, 4].map((i) => Number.parseInt(f.slice(i, i + 2), 16))
  }
  const x = p(a)
  const y = p(b)
  return `#${x
    .map((v, i) => Math.round(v * t + y[i] * (1 - t)))
    .map((v) => v.toString(16).padStart(2, "0"))
    .join("")}`
}

let ctx: CanvasRenderingContext2D | null = null
/** The width of text in a font (for fitting labels). */
export function measure(text: string, font: string): number {
  if (typeof document === "undefined") return text.length * 7
  ctx ??= document.createElement("canvas").getContext("2d")
  if (!ctx) return text.length * 7
  ctx.font = font
  return ctx.measureText(text).width
}
function fit(text: string, max: number, font: string): string {
  if (measure(text, font) <= max) return text
  let t = text
  while (t.length > 1 && measure(`${t}…`, font) > max) t = t.slice(0, -1)
  return `${t}…`
}
function wrap(text: string, max: number, font: string, lines = 2): string[] {
  const words = text.split(/\s+/)
  const out: string[] = []
  let cur = ""
  for (const w of words) {
    const next = cur ? `${cur} ${w}` : w
    if (measure(next, font) <= max || !cur) cur = next
    else {
      out.push(cur)
      cur = w
    }
  }
  if (cur) out.push(cur)
  if (out.length > lines) {
    const kept = out.slice(0, lines)
    kept[lines - 1] = fit(`${kept[lines - 1]} ${out.slice(lines).join(" ")}`, max, font)
    return kept
  }
  return out
}

/** Colours of an edge style. */
export function edgeLook(s: EdgeStyle | undefined, pal: Palette) {
  switch (s) {
    case "human":
      return { color: pal.tones.amber, dash: "", width: 2 }
    case "feedback":
      return { color: pal.tones.blue, dash: "7 5", width: 1.8 }
    case "repair":
      return { color: pal.tones.red, dash: "7 5", width: 1.8 }
    case "async":
      return { color: pal.muted, dash: "2 5", width: 1.8 }
    case "data":
      return { color: pal.tones.green, dash: "", width: 2 }
    case "muted":
      return { color: pal.muted, dash: "", width: 1.3 }
    default:
      return { color: pal.tones.blue, dash: "", width: 2 }
  }
}

/** Lanes: each group's panel around its nodes. */
export function laneBoxes(dia: Diagram) {
  return dia.groups
    .map((g) => {
      const members = dia.nodes.filter((n) => n.group === g.id).map(boxOf)
      if (!members.length) return null
      const top = g.subtitle ? 74 : 52
      const x = Math.min(...members.map((b) => b.x)) - 26
      const y = Math.min(...members.map((b) => b.y)) - top
      return {
        g,
        x,
        y,
        w: Math.max(...members.map((b) => b.x + b.w)) + 26 - x,
        h: Math.max(...members.map((b) => b.y + b.h)) + 26 - y,
      }
    })
    .filter((l): l is { g: DGroup; x: number; y: number; w: number; h: number } => l !== null)
}

/** The extent of everything drawn (nodes and lanes), without the legend. */
export function extent(dia: Diagram, extra: DNode[] = []) {
  const boxes = [...dia.nodes, ...extra].map(boxOf)
  const lanes = laneBoxes(dia)
  const all = [...boxes, ...lanes]
  if (!all.length) return { x: 0, y: 0, w: 400, h: 240 }
  const x0 = Math.min(...all.map((b) => b.x))
  const y0 = Math.min(...all.map((b) => b.y))
  const x1 = Math.max(...all.map((b) => b.x + b.w))
  const y1 = Math.max(...all.map((b) => b.y + b.h))
  return { x: x0, y: y0, w: x1 - x0, h: y1 - y0 }
}

/** The edge styles in use, in their order, with what the legend calls them. */
export function legendOf(dia: Diagram): { style: EdgeStyle; label: string }[] {
  if (dia.legend === false) return []
  const used = new Set(dia.edges.map((e) => e.style ?? "flow"))
  if (used.size < 2 && !dia.legend) return []
  const names = dia.legend || {}
  return STYLES.filter((s) => used.has(s)).map((s) => ({
    style: s,
    label: names[s] ?? STYLE_LABEL[s],
  }))
}
export const LEGEND_H = 64

/** Markers and filters a drawing uses; ids are prefixed so two windows do not clash. */
export function Defs({ uid, pal }: { uid: string; pal: Palette }) {
  const colors = new Set([
    ...STYLES.map((s) => edgeLook(s, pal).color),
    pal.select,
    ...Object.values(CHANGE_COLOR),
  ])
  return (
    <defs>
      {[...colors].map((c) => (
        <marker
          key={c}
          id={`${uid}-arrow-${c.replace("#", "")}`}
          viewBox="0 0 10 10"
          refX="9"
          refY="5"
          markerWidth="7"
          markerHeight="7"
          markerUnits="userSpaceOnUse"
          orient="auto-start-reverse"
        >
          <path d="M0 1L10 5L0 9z" fill={c} />
        </marker>
      ))}
      <pattern id={`${uid}-grid`} width="22" height="22" patternUnits="userSpaceOnUse">
        <circle cx="1" cy="1" r="1" fill={pal.grid} />
      </pattern>
      {/* In user space: a straight line has no height, and a box-relative region would hide it. */}
      <filter
        id={`${uid}-glow`}
        filterUnits="userSpaceOnUse"
        x="-20000"
        y="-20000"
        width="40000"
        height="40000"
      >
        <feGaussianBlur stdDeviation="3.2" result="b" />
        <feMerge>
          <feMergeNode in="b" />
          <feMergeNode in="SourceGraphic" />
        </feMerge>
      </filter>
      <filter id={`${uid}-shadow`} x="-20%" y="-20%" width="140%" height="160%">
        <feDropShadow dx="0" dy="2" stdDeviation="3" floodColor={SHADOW} floodOpacity="0.18" />
      </filter>
    </defs>
  )
}

export interface SceneProps {
  dia: Diagram
  pal: Palette
  uid: string
  routes: Map<string, Route>
  /** Removed nodes and edges shown in a comparison, at their old places. */
  gone?: { nodes: DNode[]; edges: DEdge[] }
  changes?: { nodes: Map<string, Change>; edges: Map<string, Change> }
  sel?: { type: "node" | "edge" | "group"; id: string } | null
  marksOn?: (type: "node" | "edge", id: string) => number
  active?: string
  /** A connector being dragged from a node. */
  handleOn?: string
  legend?: boolean
}

/** Everything drawn, in plane coordinates. */
export function Scene({
  dia,
  pal,
  uid,
  routes,
  gone,
  changes,
  sel,
  marksOn,
  active,
  handleOn,
  legend = true,
}: SceneProps) {
  const glow = pal.glow ? `url(#${uid}-glow)` : undefined
  const nodes = [...(gone?.nodes ?? []), ...dia.nodes]
  const edges = [...(gone?.edges ?? []), ...dia.edges]
  const lanes = laneBoxes(dia)
  const ext = extent(dia, gone?.nodes)
  const legendItems = legend ? legendOf(dia) : []
  const arrow = (c: string) => `url(#${uid}-arrow-${c.replace("#", "")})`

  return (
    <>
      {lanes.map((l) => {
        const tone = l.g.tone ? pal.tones[l.g.tone] : undefined
        const on = sel?.type === "group" && sel.id === l.g.id
        return (
          <g key={l.g.id} data-group={l.g.id} className="dia-lane">
            <rect
              x={l.x}
              y={l.y}
              width={l.w}
              height={l.h}
              rx={16}
              fill={tone ? mix(tone, pal.bg, 0.07) : pal.laneFill}
              stroke={on ? pal.select : tone ? mix(tone, pal.bg, 0.4) : pal.laneStroke}
              strokeWidth={on ? 2 : 1.2}
            />
            <text
              x={l.x + 24}
              y={l.y + 30}
              fontFamily={MONO}
              fontSize={11.5}
              fontWeight={600}
              letterSpacing="0.2em"
              fill={tone ?? pal.laneTitle}
            >
              {l.g.label.toUpperCase()}
            </text>
            {l.g.subtitle && (
              <text x={l.x + 24} y={l.y + 52} fontFamily={SANS} fontSize={12.5} fill={pal.muted}>
                {fit(l.g.subtitle, l.w - 48, `12.5px ${SANS}`)}
              </text>
            )}
          </g>
        )
      })}

      {edges.map((e) => {
        const r = routes.get(e.id)
        if (!r) return null
        const change = changes?.edges.get(e.id) ?? "same"
        const look = edgeLook(e.style, pal)
        const on = sel?.type === "edge" && sel.id === e.id
        const color =
          change !== "same" && change !== "moved"
            ? CHANGE_COLOR[change]
            : on
              ? pal.select
              : look.color
        const d = pathOf(r.points)
        const n = marksOn?.("edge", e.id) ?? 0
        const label = `${e.label ?? ""}${n ? `  ●${n}` : ""}`.trim()
        const chipFont = `600 9.5px ${MONO}`
        const tw = label ? measure(label.toUpperCase(), chipFont) + label.length * 0.9 : 0
        return (
          <g
            key={`${change}:${e.id}`}
            data-edge={e.id}
            className="dia-edge"
            opacity={change === "removed" ? 0.75 : 1}
          >
            <path d={d} fill="none" stroke="transparent" strokeWidth={14} className="dia-hit" />
            <path
              d={d}
              fill="none"
              stroke={color}
              strokeWidth={on ? look.width + 0.8 : look.width}
              strokeDasharray={change === "removed" ? "6 5" : look.dash || undefined}
              strokeLinecap="round"
              strokeLinejoin="round"
              markerEnd={arrow(color)}
              markerStart={e.both ? arrow(color) : undefined}
              filter={glow}
            />
            {!e.both && (
              <circle
                cx={r.from.x}
                cy={r.from.y}
                r={3.6}
                fill={pal.bg}
                stroke={color}
                strokeWidth={1.6}
              />
            )}
            {label && (
              <g transform={`translate(${r.mid.x} ${r.mid.y})`}>
                <rect
                  x={-tw / 2 - 7}
                  y={-9}
                  width={tw + 14}
                  height={18}
                  rx={4}
                  fill={pal.bg}
                  stroke={pal.glow ? mix(color, pal.bg, 0.5) : "none"}
                />
                <text
                  textAnchor="middle"
                  y={3.4}
                  fontFamily={MONO}
                  fontSize={9.5}
                  fontWeight={600}
                  letterSpacing="0.09em"
                  fill={color}
                >
                  {label.toUpperCase()}
                </text>
              </g>
            )}
          </g>
        )
      })}

      {nodes.map((n) => (
        <NodeArt
          key={`${changes?.nodes.get(n.id) === "removed" ? "gone:" : ""}${n.id}`}
          n={n}
          pal={pal}
          uid={uid}
          change={changes?.nodes.get(n.id) ?? "same"}
          on={sel?.type === "node" && sel.id === n.id}
          marks={marksOn?.("node", n.id) ?? 0}
          active={!!active && active === n.id}
          handle={handleOn === n.id}
        />
      ))}

      {legendItems.length > 0 && (
        <g transform={`translate(${ext.x} ${ext.y + ext.h + 34})`} className="dia-legend-art">
          <line
            x1={0}
            x2={Math.max(ext.w, 520)}
            y1={0}
            y2={0}
            stroke={pal.laneStroke}
            strokeWidth={1}
          />
          <text
            x={0}
            y={34}
            fontFamily={MONO}
            fontSize={11}
            fontWeight={600}
            letterSpacing="0.2em"
            fill={pal.laneTitle}
          >
            FLOW
          </text>
          {legendItems.map((it, i) => {
            const look = edgeLook(it.style, pal)
            const x = 80 + i * 190
            return (
              <g key={it.style} transform={`translate(${x} 30)`}>
                <line
                  x1={0}
                  x2={48}
                  y1={0}
                  y2={0}
                  stroke={look.color}
                  strokeWidth={look.width}
                  strokeDasharray={look.dash || undefined}
                  markerEnd={arrow(look.color)}
                  filter={glow}
                />
                <text x={60} y={4} fontFamily={SANS} fontSize={12} fill={pal.text}>
                  {it.label}
                </text>
              </g>
            )
          })}
        </g>
      )}
    </>
  )
}

function NodeArt({
  n,
  pal,
  uid,
  change,
  on,
  marks,
  active,
  handle,
}: {
  n: DNode
  pal: Palette
  uid: string
  change: Change
  on: boolean
  marks: number
  active: boolean
  handle: boolean
}) {
  const b = boxOf(n)
  const kind = n.kind ?? "service"
  const T = pal.tones[n.tone ?? KIND_TONE[kind]]
  const changed = change !== "same" && change !== "moved" ? CHANGE_COLOR[change] : undefined
  const glow = pal.glow ? `url(#${uid}-glow)` : undefined
  const shadow = pal.glow ? undefined : `url(#${uid}-shadow)`
  const toned = !!n.tone || pal.glow || n.highlight
  const stroke = changed ?? (n.highlight ? T : toned ? mix(T, pal.card, 0.6) : pal.cardStroke)
  const strokeW = changed || n.highlight ? 2 : toned ? 1.5 : 1.2
  const fill = n.highlight
    ? mix(T, pal.card, 0.1)
    : changed
      ? mix(changed, pal.card, 0.1)
      : pal.card
  const dash =
    change === "removed"
      ? "6 4"
      : kind === "external"
        ? "5 4"
        : change === "moved"
          ? "2 3"
          : undefined
  const Icon = ICONS[n.icon ?? KIND_ICON[kind] ?? ""] as LucideIcon | undefined
  const titleFont = `600 13.5px ${SANS}`
  let body: ReactNode

  if (kind === "decision") {
    const lines = wrap(n.label, b.w * 0.62, `600 12.5px ${SANS}`, 3)
    body = (
      <>
        <polygon
          points={`${b.w / 2},0 ${b.w},${b.h / 2} ${b.w / 2},${b.h} 0,${b.h / 2}`}
          fill={mix(T, pal.card, 0.12)}
          stroke={changed ?? T}
          strokeWidth={1.8}
          strokeDasharray={dash}
          filter={glow}
        />
        {lines.map((l, i) => (
          <text
            key={l}
            x={b.w / 2}
            y={b.h / 2 + 4 + (i - (lines.length - 1) / 2) * 15}
            textAnchor="middle"
            fontFamily={SANS}
            fontSize={12.5}
            fontWeight={600}
            fill={pal.text}
          >
            {l}
          </text>
        ))}
      </>
    )
  } else if (kind === "start" || kind === "end") {
    body = (
      <>
        <rect
          width={b.w}
          height={b.h}
          rx={b.h / 2}
          fill={kind === "start" ? mix(T, pal.card, 0.16) : pal.card}
          stroke={changed ?? T}
          strokeWidth={1.6}
          strokeDasharray={dash}
          filter={glow ?? shadow}
        />
        {Icon && <Icon x={16} y={b.h / 2 - 8} width={16} height={16} stroke={T} strokeWidth={2} />}
        <text
          x={Icon ? 40 : b.w / 2}
          y={b.h / 2 + 4.5}
          textAnchor={Icon ? "start" : "middle"}
          fontFamily={SANS}
          fontSize={13}
          fontWeight={600}
          fill={pal.text}
        >
          {fit(n.label, b.w - (Icon ? 54 : 24), titleFont)}
        </text>
      </>
    )
  } else {
    const isNote = kind === "note"
    const left = 14
    const badge = n.step ? (
      <g transform={`translate(${left + 12} ${n.subtitle ? 26 : 24})`}>
        <circle r={12} fill={n.highlight || n.tone ? T : pal.card} stroke={T} strokeWidth={1.5} />
        <text
          textAnchor="middle"
          y={4.5}
          fontFamily={SANS}
          fontSize={12}
          fontWeight={700}
          fill={n.highlight || n.tone ? pal.card : T}
        >
          {n.step}
        </text>
      </g>
    ) : Icon && !isNote ? (
      <g transform={`translate(${left} ${n.subtitle ? 13 : 11})`}>
        <rect width={26} height={26} rx={7} fill={mix(T, pal.card, 0.16)} />
        <Icon x={5.5} y={5.5} width={15} height={15} stroke={T} strokeWidth={2} />
      </g>
    ) : null
    const tx = badge ? left + 36 : left
    const tagW = n.tag
      ? measure(n.tag.toUpperCase(), `600 9px ${MONO}`) + n.tag.length * 0.8 + 14
      : 0
    const titleMax = b.w - tx - 12 - (n.tag ? tagW + 6 : 0)
    const headH = n.subtitle ? 62 : 48
    body = (
      <>
        <rect
          width={b.w}
          height={b.h}
          rx={isNote ? 4 : 11}
          fill={isNote ? pal.note : fill}
          stroke={isNote && !changed ? "none" : stroke}
          strokeWidth={strokeW}
          strokeDasharray={dash}
          filter={pal.glow && toned ? glow : shadow}
        />
        {badge}
        <text
          x={tx}
          y={n.subtitle ? 29 : 29}
          fontFamily={SANS}
          fontSize={13.5}
          fontWeight={600}
          fill={pal.text}
        >
          {fit(n.label, titleMax, titleFont)}
        </text>
        {n.subtitle && (
          <text x={tx} y={46} fontFamily={MONO} fontSize={10.5} fill={pal.muted}>
            {fit(n.subtitle, b.w - tx - 12, `10.5px ${MONO}`)}
          </text>
        )}
        {n.tag && (
          <g transform={`translate(${b.w - 10 - tagW} 10)`}>
            <rect width={tagW} height={17} rx={4} fill={mix(T, pal.card, 0.18)} />
            <text
              x={tagW / 2}
              y={11.8}
              textAnchor="middle"
              fontFamily={MONO}
              fontSize={9}
              fontWeight={600}
              letterSpacing="0.08em"
              fill={T}
            >
              {n.tag.toUpperCase()}
            </text>
          </g>
        )}
        {n.items?.map((it, i) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: rows may repeat; their order is what they are
          <g key={`${i}:${it}`} transform={`translate(${left} ${headH + 4 + i * ITEM_H})`}>
            <rect
              width={b.w - left * 2}
              height={ITEM_H - 6}
              rx={4}
              fill={pal.row}
              stroke={pal.cardStroke}
              strokeWidth={1}
            />
            <rect x={8} y={(ITEM_H - 6) / 2 - 4} width={8} height={8} rx={1.5} fill={T} />
            <text
              x={24}
              y={(ITEM_H - 6) / 2 + 3.6}
              fontFamily={MONO}
              fontSize={10}
              letterSpacing="0.06em"
              fill={pal.text}
            >
              {fit(it.toUpperCase(), b.w - left * 2 - 32, `10px ${MONO}`)}
            </text>
          </g>
        ))}
      </>
    )
  }

  return (
    <g
      data-node={n.id}
      className="dia-node"
      transform={`translate(${b.x} ${b.y})`}
      opacity={change === "removed" ? 0.6 : 1}
    >
      {(on || active) && (
        <rect
          x={-5}
          y={-5}
          width={b.w + 10}
          height={b.h + 10}
          rx={kind === "start" || kind === "end" ? (b.h + 10) / 2 : 14}
          fill="none"
          stroke={pal.select}
          strokeWidth={on ? 2 : 1.5}
          strokeDasharray={active && !on ? "4 3" : undefined}
        />
      )}
      {body}
      {marks > 0 && (
        <g transform={`translate(${b.w - 2} 2)`}>
          <circle r={9} fill={pal.select} />
          <text
            textAnchor="middle"
            y={3.5}
            fontFamily={SANS}
            fontSize={10}
            fontWeight={700}
            fill={pal.bg}
          >
            {marks}
          </text>
        </g>
      )}
      {handle && (
        <circle
          data-handle
          cx={b.w}
          cy={b.h / 2}
          r={7}
          fill={pal.select}
          stroke={pal.bg}
          strokeWidth={2}
          className="dia-handle"
        />
      )}
    </g>
  )
}
