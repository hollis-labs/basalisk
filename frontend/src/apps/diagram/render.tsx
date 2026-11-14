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
  Book,
  BookOpen,
  Bot,
  Box,
  Brain,
  Calendar,
  CalendarDays,
  CheckCircle2,
  Clapperboard,
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
  Gamepad2,
  Gem,
  GitBranch,
  GitMerge,
  GitPullRequest,
  Globe,
  HardDrive,
  Key,
  Layers,
  Lightbulb,
  List,
  Lock,
  type LucideIcon,
  Mail,
  Map as MapIcon,
  MapPin,
  MessageSquare,
  Milestone,
  Monitor,
  Network,
  Package,
  PenLine,
  Play,
  Radio,
  RefreshCw,
  Rocket,
  Router,
  Scroll,
  Search,
  Server,
  Settings,
  Shield,
  Smartphone,
  StickyNote,
  Swords,
  Target,
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
  CARD,
  type Change,
  type DEdge,
  type DGroup,
  type Diagram,
  type DNode,
  type EdgeStyle,
  type Grid,
  type NodeKind,
  STYLE_LABEL,
  STYLES,
  type Tone,
  titleBox,
} from "./model"
import { pathOf, type Route } from "./route"
import { fit, MONO, measure, SANS, wrap } from "./text"
import { CHANGE_COLOR, PALETTES, type Palette, SHADOW } from "./theme"

export { MONO, PALETTES, type Palette, SANS }

/** Icons a node may name (lucide names). */
export const ICONS: Record<string, LucideIcon> = {
  "alert-triangle": AlertTriangle,
  bell: Bell,
  book: Book,
  "book-open": BookOpen,
  "calendar-days": CalendarDays,
  clapperboard: Clapperboard,
  "gamepad-2": Gamepad2,
  gem: Gem,
  lightbulb: Lightbulb,
  map: MapIcon,
  "map-pin": MapPin,
  "message-square": MessageSquare,
  milestone: Milestone,
  "pen-line": PenLine,
  radio: Radio,
  scroll: Scroll,
  swords: Swords,
  target: Target,
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

/** Colours of an edge style. */
export function edgeLook(s: EdgeStyle | undefined, pal: Palette) {
  switch (s) {
    case "human":
      return { color: pal.tones.amber, dash: "", width: 2.4 }
    case "feedback":
      return { color: pal.tones.blue, dash: "8 6", width: 2.1 }
    case "repair":
      return { color: pal.tones.red, dash: "8 6", width: 2.1 }
    case "async":
      return { color: pal.muted, dash: "2.5 6", width: 2.1 }
    case "data":
      return { color: pal.tones.green, dash: "", width: 2.4 }
    case "muted":
      return { color: pal.muted, dash: "", width: 1.5 }
    default:
      return { color: pal.tones.blue, dash: "", width: 2.4 }
  }
}

/** A lane's room around its nodes, and above them for its title and subtitle. */
export const LANE = { pad: 34, top: 66, topSub: 92 }

/** Lanes: each group's panel around its nodes. */
export function laneBoxes(dia: Diagram) {
  return dia.groups
    .map((g) => {
      const members = dia.nodes.filter((n) => n.group === g.id).map(boxOf)
      if (!members.length) return null
      const top = g.subtitle ? LANE.topSub : LANE.top
      const x = Math.min(...members.map((b) => b.x)) - LANE.pad
      const y = Math.min(...members.map((b) => b.y)) - top
      return {
        g,
        x,
        y,
        w: Math.max(...members.map((b) => b.x + b.w)) + LANE.pad - x,
        h: Math.max(...members.map((b) => b.y + b.h)) + LANE.pad - y,
      }
    })
    .filter((l): l is { g: DGroup; x: number; y: number; w: number; h: number } => l !== null)
}

/** The title area of each lane, for the router to keep clear. */
export function laneHeads(dia: Diagram) {
  return laneBoxes(dia).map((l) => ({
    x: l.x + 8,
    y: l.y + 14,
    w: l.w - 16,
    h: (l.g.subtitle ? LANE.topSub : LANE.top) - 24,
  }))
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
export const LEGEND_H = 80

/** Markers and filters a drawing uses; ids are prefixed so two windows do not clash. */
export function Defs({ uid, pal, grid = "lines" }: { uid: string; pal: Palette; grid?: Grid }) {
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
          markerWidth="11"
          markerHeight="11"
          markerUnits="userSpaceOnUse"
          orient="auto-start-reverse"
        >
          <path d="M0 0.5L10 5L0 9.5L2.2 5z" fill={c} />
        </marker>
      ))}
      {/* The canvas grid, on the drawing's plane: fine lines every 48, a stronger one every 240. */}
      <pattern id={`${uid}-grid`} width="240" height="240" patternUnits="userSpaceOnUse">
        {grid === "dots"
          ? [0, 48, 96, 144, 192].flatMap((x) =>
              [0, 48, 96, 144, 192].map((y) => (
                <circle
                  key={`${x}:${y}`}
                  cx={x + 1.2}
                  cy={y + 1.2}
                  r={1.2}
                  fill={x === 0 && y === 0 ? pal.gridMajor : pal.grid}
                />
              )),
            )
          : grid === "lines" && (
              <>
                <path
                  d="M48 0V240M96 0V240M144 0V240M192 0V240M0 48H240M0 96H240M0 144H240M0 192H240"
                  stroke={pal.grid}
                  strokeWidth={1}
                  fill="none"
                />
                <path d="M0 0V240M0 0H240" stroke={pal.gridMajor} strokeWidth={1.2} fill="none" />
              </>
            )}
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
  /** In a presentation: the nodes in view; the rest are dimmed. */
  focus?: Set<string>
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
  focus,
}: SceneProps) {
  const dim = (on: boolean) => (focus && !on ? 0.12 : 1)
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
              rx={20}
              fill={tone ? mix(tone, pal.bg, pal.glow ? 0.08 : 0.06) : pal.laneFill}
              stroke={on ? pal.select : tone ? mix(tone, pal.bg, 0.38) : pal.laneStroke}
              strokeWidth={on ? 2 : 1.3}
            />
            <text
              x={l.x + LANE.pad}
              y={l.y + 40}
              fontFamily={MONO}
              fontSize={12.5}
              fontWeight={600}
              letterSpacing="0.24em"
              fill={tone ?? pal.laneTitle}
            >
              {l.g.label.toUpperCase()}
            </text>
            {l.g.subtitle && (
              <text
                x={l.x + LANE.pad}
                y={l.y + 64}
                fontFamily={SANS}
                fontSize={13.5}
                fill={pal.muted}
              >
                {fit(l.g.subtitle, l.w - LANE.pad * 2, `13.5px ${SANS}`)}
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
        const chipFont = `600 10.5px ${MONO}`
        const tw = label ? measure(label.toUpperCase(), chipFont) + label.length * 1.05 : 0
        const chipFill = pal.glow ? pal.bg : pal.card
        return (
          <g
            key={`${change}:${e.id}`}
            data-edge={e.id}
            className="dia-edge"
            opacity={
              (change === "removed" ? 0.75 : 1) * dim(!!focus?.has(e.from) && !!focus?.has(e.to))
            }
            style={focus ? { transition: "opacity 0.35s" } : undefined}
          >
            <path d={d} fill="none" stroke="transparent" strokeWidth={16} className="dia-hit" />
            <path
              d={d}
              fill="none"
              stroke={color}
              strokeWidth={on ? look.width + 1 : look.width}
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
                r={4.6}
                fill={pal.bg}
                stroke={color}
                strokeWidth={2}
              />
            )}
            {label && (
              <g transform={`translate(${r.mid.x} ${r.mid.y})`}>
                <rect
                  x={-tw / 2 - 10}
                  y={-11}
                  width={tw + 20}
                  height={22}
                  rx={6}
                  fill={chipFill}
                  stroke={mix(color, pal.bg, pal.glow ? 0.55 : 0.22)}
                  strokeWidth={1}
                />
                <text
                  textAnchor="middle"
                  y={3.8}
                  fontFamily={MONO}
                  fontSize={10.5}
                  fontWeight={600}
                  letterSpacing="0.1em"
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
          dim={dim(!!focus?.has(n.id))}
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

      {legendItems.length > 0 &&
        (() => {
          let x = 110
          return (
            <g transform={`translate(${ext.x} ${ext.y + ext.h + 44})`} className="dia-legend-art">
              <line
                x1={0}
                x2={Math.max(ext.w, 560)}
                y1={0}
                y2={0}
                stroke={pal.laneStroke}
                strokeWidth={1.2}
              />
              <text
                x={0}
                y={42}
                fontFamily={MONO}
                fontSize={12.5}
                fontWeight={600}
                letterSpacing="0.24em"
                fill={pal.laneTitle}
              >
                FLOW
              </text>
              {legendItems.map((it) => {
                const look = edgeLook(it.style, pal)
                const at = x
                x += 76 + measure(it.label, `14px ${SANS}`) + 56
                return (
                  <g key={it.style} transform={`translate(${at} 38)`}>
                    <line
                      x1={0}
                      x2={60}
                      y1={0}
                      y2={0}
                      stroke={look.color}
                      strokeWidth={look.width}
                      strokeDasharray={look.dash || undefined}
                      strokeLinecap="round"
                      markerEnd={arrow(look.color)}
                      filter={glow}
                    />
                    <text x={76} y={5} fontFamily={SANS} fontSize={14} fill={pal.text}>
                      {it.label}
                    </text>
                  </g>
                )
              })}
            </g>
          )
        })()}
    </>
  )
}

function NodeArt({
  dim = 1,
  n,
  pal,
  uid,
  change,
  on,
  marks,
  active,
  handle,
}: {
  dim?: number
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
  const toned = !!n.tone || pal.glow || !!n.highlight
  const stroke = changed ?? (n.highlight ? T : toned ? mix(T, pal.card, 0.55) : pal.cardStroke)
  const strokeW = changed || n.highlight ? 2.2 : toned ? 1.5 : 1.2
  const fill = n.highlight
    ? mix(T, pal.card, 0.09)
    : changed
      ? mix(changed, pal.card, 0.1)
      : pal.card
  const dash =
    change === "removed"
      ? "6 4"
      : kind === "external"
        ? "6 4"
        : change === "moved"
          ? "2 3"
          : undefined
  const Icon = ICONS[n.icon ?? KIND_ICON[kind] ?? ""] as LucideIcon | undefined
  const P = CARD.pad
  let body: ReactNode

  if (kind === "decision") {
    const font = `600 13.5px ${SANS}`
    const lines = wrap(n.label, b.w * 0.6, font, 3)
    body = (
      <>
        {!pal.glow && (
          <polygon
            points={`${b.w / 2},4 ${b.w},${b.h / 2 + 4} ${b.w / 2},${b.h + 4} 0,${b.h / 2 + 4}`}
            fill={SHADOW}
            opacity={pal.shadow * 0.45}
          />
        )}
        <polygon
          points={`${b.w / 2},0 ${b.w},${b.h / 2} ${b.w / 2},${b.h} 0,${b.h / 2}`}
          fill={mix(T, pal.card, 0.12)}
          stroke={changed ?? T}
          strokeWidth={2}
          strokeLinejoin="round"
          strokeDasharray={dash}
          filter={glow}
        />
        {lines.map((l, i) => (
          <text
            key={l}
            x={b.w / 2}
            y={b.h / 2 + 5 + (i - (lines.length - 1) / 2) * 17}
            textAnchor="middle"
            fontFamily={SANS}
            fontSize={13.5}
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
        {!pal.glow && <Shadow w={b.w} h={b.h} rx={b.h / 2} pal={pal} />}
        <rect
          width={b.w}
          height={b.h}
          rx={b.h / 2}
          fill={kind === "start" ? mix(T, pal.card, 0.18) : pal.card}
          stroke={changed ?? T}
          strokeWidth={1.8}
          strokeDasharray={dash}
          filter={glow}
        />
        {Icon && (
          <Icon x={20} y={b.h / 2 - 9} width={18} height={18} stroke={T} strokeWidth={2.2} />
        )}
        <text
          x={Icon ? 48 : b.w / 2}
          y={b.h / 2 + 5}
          textAnchor={Icon ? "start" : "middle"}
          fontFamily={SANS}
          fontSize={14.5}
          fontWeight={600}
          fill={pal.text}
        >
          {fit(n.label, b.w - (Icon ? 64 : 28), `600 14.5px ${SANS}`)}
        </text>
      </>
    )
  } else {
    const isNote = kind === "note"
    const titleFont = `600 15px ${SANS}`
    const tb = titleBox(n, b.w)
    const lines = wrap(n.label, tb.max, titleFont, 2)
    const extra = (lines.length - 1) * CARD.line
    // A tag is a pill on its own line above the title; the rest moves down under it.
    const top = n.tag ? CARD.tag : 0
    const headH = (n.subtitle ? CARD.headSub : CARD.head) + extra + top
    // Baselines: the title's first line, the subtitle; the lead (badge or icon) sits centred on them.
    const t0 = n.subtitle ? 34 + top : (headH + top) / 2 - extra / 2 + 5.5
    const subY = t0 + extra + 19
    const cy = n.subtitle ? (t0 - 11 + subY + 3) / 2 : (headH + top) / 2
    const filled = !!n.highlight || !!n.tone
    const lead = n.step ? (
      <g transform={`translate(${P + 17} ${cy})`}>
        <circle r={16} fill={filled ? T : pal.card} stroke={T} strokeWidth={2} />
        <text
          textAnchor="middle"
          y={5}
          fontFamily={SANS}
          fontSize={14}
          fontWeight={700}
          fill={filled ? pal.onTone : T}
        >
          {n.step}
        </text>
      </g>
    ) : Icon && !isNote ? (
      <g transform={`translate(${P} ${cy - 17})`}>
        <rect
          width={34}
          height={34}
          rx={9}
          fill={mix(T, pal.card, pal.glow ? 0.22 : 0.14)}
          stroke={mix(T, pal.card, 0.3)}
          strokeWidth={1}
        />
        <Icon x={8} y={8} width={18} height={18} stroke={T} strokeWidth={2} />
      </g>
    ) : null
    const tx = tb.x
    const tagFont = `600 10px ${MONO}`
    const tagW = n.tag ? measure(n.tag.toUpperCase(), tagFont) + n.tag.length * 1 + 16 : 0
    body = (
      <>
        {n.highlight && !pal.glow && (
          <rect
            x={-5}
            y={-5}
            width={b.w + 10}
            height={b.h + 10}
            rx={19}
            fill="none"
            stroke={T}
            strokeOpacity={0.18}
            strokeWidth={6}
          />
        )}
        {!pal.glow && <Shadow w={b.w} h={b.h} rx={isNote ? 6 : 14} pal={pal} />}
        <rect
          width={b.w}
          height={b.h}
          rx={isNote ? 6 : 14}
          fill={isNote ? pal.note : fill}
          stroke={isNote && !changed ? "none" : stroke}
          strokeWidth={strokeW}
          strokeDasharray={dash}
          filter={pal.glow && toned ? glow : undefined}
        />
        {!isNote && !pal.glow && (
          <path
            d={`M14 1.2 H${b.w - 14}`}
            stroke={pal.sheen}
            strokeWidth={1}
            strokeLinecap="round"
          />
        )}
        {lead}
        {lines.map((l, i) => (
          <text
            // biome-ignore lint/suspicious/noArrayIndexKey: a title's lines, in order
            key={`${i}:${l}`}
            x={tx}
            y={t0 + i * CARD.line}
            fontFamily={SANS}
            fontSize={15}
            fontWeight={600}
            fill={pal.text}
          >
            {l}
          </text>
        ))}
        {n.subtitle && (
          <text x={tx} y={subY} fontFamily={MONO} fontSize={11.5} fill={pal.muted}>
            {fit(n.subtitle, b.w - tx - P, `11.5px ${MONO}`)}
          </text>
        )}
        {n.tag && (
          <g transform={`translate(${tx} ${12})`}>
            <rect width={tagW} height={20} rx={6} fill={mix(T, pal.card, 0.16)} />
            <text
              x={tagW / 2}
              y={13.8}
              textAnchor="middle"
              fontFamily={MONO}
              fontSize={10}
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
          <g key={`${i}:${it}`} transform={`translate(${P} ${headH + i * CARD.row})`}>
            <rect
              width={b.w - P * 2}
              height={CARD.rowH}
              rx={6}
              fill={pal.row}
              stroke={pal.cardStroke}
              strokeWidth={1}
            />
            <rect x={10} y={CARD.rowH / 2 - 4.5} width={9} height={9} rx={2} fill={T} />
            <text
              x={28}
              y={CARD.rowH / 2 + 4}
              fontFamily={MONO}
              fontSize={10.5}
              fontWeight={500}
              letterSpacing="0.08em"
              fill={pal.text}
            >
              {fit(it.toUpperCase(), b.w - P * 2 - 38, `500 10.5px ${MONO}`)}
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
      opacity={(change === "removed" ? 0.6 : 1) * dim}
      style={{ transition: "opacity 0.35s" }}
    >
      {(on || active) && (
        <rect
          x={-7}
          y={-7}
          width={b.w + 14}
          height={b.h + 14}
          rx={kind === "start" || kind === "end" ? (b.h + 14) / 2 : 20}
          fill="none"
          stroke={pal.select}
          strokeWidth={on ? 2 : 1.5}
          strokeDasharray={active && !on ? "4 3" : undefined}
        />
      )}
      {body}
      {marks > 0 && (
        <g transform={`translate(${b.w - 2} 2)`}>
          <circle r={10} fill={pal.select} />
          <text
            textAnchor="middle"
            y={4}
            fontFamily={SANS}
            fontSize={11}
            fontWeight={700}
            fill={pal.onTone}
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
          r={8}
          fill={pal.select}
          stroke={pal.bg}
          strokeWidth={2.5}
          className="dia-handle"
        />
      )}
    </g>
  )
}

/** A card's shadow as plain shapes (a filter would be drawn blurry when scaled in WebKit). */
function Shadow({ w, h, rx, pal }: { w: number; h: number; rx: number; pal: Palette }) {
  return (
    <>
      <rect
        x={-3}
        y={7}
        width={w + 6}
        height={h}
        rx={rx + 3}
        fill={SHADOW}
        opacity={pal.shadow * 0.18}
      />
      <rect
        x={-1}
        y={4}
        width={w + 2}
        height={h}
        rx={rx + 1}
        fill={SHADOW}
        opacity={pal.shadow * 0.28}
      />
      <rect y={1.5} width={w} height={h} rx={rx} fill={SHADOW} opacity={pal.shadow * 0.45} />
    </>
  )
}
