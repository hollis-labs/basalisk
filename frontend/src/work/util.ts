/** Small helpers the Work views share. */
import {
  Book,
  BookOpen,
  Circle,
  Clapperboard,
  FileText,
  Flag,
  Gamepad2,
  Gem,
  Layers,
  Lightbulb,
  type LucideIcon,
  MapPin,
  MessageSquare,
  Milestone,
  Radio,
  Scroll,
  Shield,
  Swords,
  Target,
  User,
} from "lucide-react"

/** Kind icons by lucide name; the presets use these, anything else is a dot. */
const ICONS: Record<string, LucideIcon> = {
  lightbulb: Lightbulb,
  "file-text": FileText,
  clapperboard: Clapperboard,
  "message-square": MessageSquare,
  "gamepad-2": Gamepad2,
  layers: Layers,
  radio: Radio,
  book: Book,
  "book-open": BookOpen,
  user: User,
  "map-pin": MapPin,
  gem: Gem,
  shield: Shield,
  scroll: Scroll,
  swords: Swords,
  target: Target,
  flag: Flag,
  milestone: Milestone,
}

export const kindIcon = (name?: string): LucideIcon => (name && ICONS[name]) || Circle

export const message = (e: unknown) => (e instanceof Error ? e.message : String(e))

export function ago(iso: string): string {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return ""
  const m = Math.round((Date.now() - t) / 60000)
  if (m < 1) return "just now"
  if (m < 60) return `${m}m ago`
  const h = Math.round(m / 60)
  return h < 48 ? `${h}h ago` : `${Math.round(h / 24)}d ago`
}

/** A stage or value as a column heading. */
export const label = (s: string) => (s ? s[0].toUpperCase() + s.slice(1).replace(/-/g, " ") : "")

export const slug = (s: string) =>
  s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")

export type DiffPart = { op: "same" | "del" | "ins"; text: string }

/** A word-level diff (longest common subsequence over words and the spaces between). */
export function diffWords(a: string, b: string): DiffPart[] {
  const x = a.split(/(\s+)/)
  const y = b.split(/(\s+)/)
  if (x.length * y.length > 400_000)
    return [
      { op: "del", text: a },
      { op: "ins", text: b },
    ]
  const n = x.length
  const m = y.length
  const t: number[][] = Array.from({ length: n + 1 }, () => new Array(m + 1).fill(0))
  for (let i = n - 1; i >= 0; i--)
    for (let j = m - 1; j >= 0; j--)
      t[i][j] = x[i] === y[j] ? t[i + 1][j + 1] + 1 : Math.max(t[i + 1][j], t[i][j + 1])
  const out: DiffPart[] = []
  const push = (op: DiffPart["op"], text: string) => {
    const last = out[out.length - 1]
    if (last && last.op === op) last.text += text
    else out.push({ op, text })
  }
  let i = 0
  let j = 0
  while (i < n && j < m) {
    if (x[i] === y[j]) {
      push("same", x[i])
      i++
      j++
    } else if (t[i + 1][j] >= t[i][j + 1]) push("del", x[i++])
    else push("ins", y[j++])
  }
  while (i < n) push("del", x[i++])
  while (j < m) push("ins", y[j++])
  return out
}
