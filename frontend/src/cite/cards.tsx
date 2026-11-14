/**
 * Result cards in the agent panel: when a finished tool call returned one of a small fixed
 * set of things (a Torque task, a Tesseract item, a file, a web page, a Frag capture), the
 * call's row shows it as a card: a title, two or three facts, and the open action that thing
 * already has on the desktop. The raw result stays behind the row's caret.
 *
 * The page chooses, by tool name: each app registers an extractor beside its tools
 * (registerResultCards). The model never lays a card out; an extractor that does not find
 * what it expects returns null and the row stays a plain row.
 *
 * A card can name a cite reference (`ref`): the cite resolver (the hover cards' cache) then
 * fills the title, status and facts the result did not carry, and Open is the cite opener.
 */
import {
  Bot,
  Brain,
  FileText,
  Globe,
  ListChecks,
  type LucideIcon,
  Maximize2,
  Zap,
} from "lucide-react"
import type { DeskAPI } from "../desktop/registry"
import { openFileRef } from "./files"
import { canResolve, openCite, type RefIdentity, useRefState } from "./index"
import "./cards.css"

export type CardKind = "torque.task" | "tesseract.item" | "file" | "page" | "frag" | "agent.task"
type Tone = "neutral" | "info" | "success" | "warning" | "danger"

export interface ResultCard {
  kind: CardKind
  /** Empty when the cite reference is to supply it. */
  title: string
  status?: { label: string; tone: Tone }
  /** At most three are shown. */
  facts: { label: string; value: string; mono?: boolean }[]
  /** A record the cite resolver can fill in and open. */
  ref?: RefIdentity
  /** What Open does, when it is not opening `ref`. */
  open?: (from: HTMLElement) => void
  /** Overrides the kind's label in the card's corner ("Torque task", "Note"). */
  label?: string
  /** A passage that changed (notes_edit): shown before and after, a few lines each. */
  /** A changed passage. before is null when only the new text is known (a whole-file write). */
  diff?: { before: string | null; after: string }
}

/** One row of a list card: it opens the way its single card would. */
export interface ListRow {
  title: string
  status?: { label: string; tone: Tone }
  /** One short fact beside the title. */
  fact?: string
  mono?: boolean
  ref?: RefIdentity
  open?: (from: HTMLElement) => void
}

/** A list result: a heading with the count, the first rows, and how many more there were. */
export interface ListCard {
  list: true
  kind: CardKind
  heading: string
  /** How many there are in all (more than rows.length when the result was cut). */
  total: number
  rows: ListRow[]
  /** There are more past `total` that the result did not count (a paged list). */
  hasMore?: boolean
}

export type AnyCard = ResultCard | ListCard

/** Turns a successful call (its input, and its result parsed as JSON when it is JSON) into a card, or null. */
export type CardExtractor = (input: Record<string, unknown>, result: unknown) => AnyCard | null

/** Rows a list card shows before "N more". */
export const LIST_ROWS = 8

const extractors = new Map<string, CardExtractor>()

/** Give some tools a card. Call at import time, beside registerAgentTools. */
export function registerResultCards(map: Record<string, CardExtractor>) {
  for (const [name, fn] of Object.entries(map)) extractors.set(name, fn)
}

const parse = (s: string): unknown => {
  try {
    return JSON.parse(s)
  } catch {
    return s
  }
}

/** The card for a finished call, or null. */
export function cardFor(
  name: string,
  input: unknown,
  result: string | undefined,
  failed: boolean,
): AnyCard | null {
  const fn = extractors.get(name)
  if (!fn || failed || result === undefined || result === "") return null
  try {
    const obj =
      input && typeof input === "object" && !Array.isArray(input)
        ? (input as Record<string, unknown>)
        : {}
    return fn(obj, parse(result))
  } catch {
    return null
  }
}

// ---- Helpers for extractors ----

/** A field of an object, whatever case the tool used (id, ID, Id; project_id, ProjectID). */
export function field(o: unknown, ...names: string[]): unknown {
  if (!o || typeof o !== "object") return undefined
  const rec = o as Record<string, unknown>
  const norm = (k: string) => k.replaceAll("_", "").toLowerCase()
  for (const n of names) {
    if (n in rec) return rec[n]
    const want = norm(n)
    for (const k of Object.keys(rec)) if (norm(k) === want) return rec[k]
  }
  return undefined
}

/** A string field (a sql.Null {String, Valid} wrapper is unwrapped), or "". */
export function text(o: unknown, ...names: string[]): string {
  const v = field(o, ...names)
  if (typeof v === "string") return v
  if (typeof v === "number") return String(v)
  const inner = field(v, "String")
  return typeof inner === "string" ? inner : ""
}

export function toneOf(status: string): Tone {
  switch (status.toLowerCase()) {
    case "done":
    case "saved":
    case "sent":
    case "canonical":
    case "active":
      return "success"
    case "doing":
    case "review":
    case "queued":
    case "sending":
      return "info"
    case "blocked":
    case "failed":
    case "cancelled":
    case "canceled":
      return "danger"
    case "paused":
    case "draft":
      return "warning"
  }
  return "neutral"
}

export const desk = () => (globalThis as { basalisk?: { desk?: DeskAPI } }).basalisk?.desk ?? null

/** A file in a place, as a card that opens it the way a file reference does. */
export function fileCard(
  root: string,
  path: string,
  facts: ResultCard["facts"] = [],
  label?: string,
): ResultCard | null {
  if (!root || !path) return null
  const name = path.split("/").pop() || path
  return {
    kind: "file",
    title: name,
    facts: [{ label: "In", value: `${root}:${path}`, mono: true }, ...facts],
    open: (from) => void openFileRef({ root, path }, from),
    ...(label ? { label } : {}),
  }
}

/** A file or folder in a place, as a list row that opens it. */
export function fileRow(root: string, path: string, folder: boolean, fact?: string): ListRow {
  const name = path.split("/").pop() || path || root
  return {
    title: folder ? `${name}/` : name,
    ...(fact ? { fact } : {}),
    open: (from) => void openFileRef({ root, path }, from),
  }
}

/** A date as the rows show it: the day, or the time today. */
export function shortWhen(iso: unknown): string {
  if (typeof iso !== "string") return ""
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ""
  return d.toDateString() === new Date().toDateString()
    ? d.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })
    : d.toLocaleDateString(undefined, { month: "short", day: "numeric" })
}

/** A size in bytes, short. */
export const bytes = (n: unknown) =>
  typeof n !== "number"
    ? ""
    : n < 1024
      ? `${n} B`
      : n < 1024 * 1024
        ? `${Math.round(n / 1024)} KB`
        : `${(n / (1024 * 1024)).toFixed(n < 10 * 1024 * 1024 ? 1 : 0)} MB`

/** A size in characters, short. */
export const chars = (n: unknown) =>
  typeof n === "number"
    ? n >= 10_000
      ? `${Math.round(n / 1000)}k characters`
      : `${n.toLocaleString()} characters`
    : ""

// ---- The card ----

const ICONS: Record<CardKind, LucideIcon> = {
  "torque.task": ListChecks,
  "tesseract.item": Brain,
  file: FileText,
  page: Globe,
  frag: Zap,
  "agent.task": Bot,
}
const LABELS: Record<CardKind, string> = {
  "torque.task": "Torque task",
  "tesseract.item": "Tesseract",
  file: "File",
  page: "Web page",
  frag: "Frag",
  "agent.task": "Task agent",
}

/** The card for one tool call row, or nothing. */
export function ResultCardView({
  item,
}: {
  item: { name: string; input?: unknown; result?: string; isError?: boolean; running: boolean }
}) {
  if (item.running) return null
  const card = cardFor(item.name, item.input, item.result, !!item.isError)
  if (!card) return null
  return "list" in card ? <ListBox card={card} /> : <CardBox card={card} />
}

const plural = (n: number, one: string, many = `${one}s`) =>
  `${n.toLocaleString()} ${n === 1 ? one : many}`

function ListBox({ card }: { card: ListCard }) {
  const Icon = ICONS[card.kind]
  const rows = card.rows.slice(0, LIST_ROWS)
  const more = Math.max(0, card.total - rows.length)
  return (
    <div className="cite-result" data-kind={card.kind} data-list="true">
      <div className="cite-result-head">
        <Icon aria-hidden="true" size={12} strokeWidth={1.7} />
        <span className="cite-result-kind">{card.heading}</span>
        <span className="cite-result-count">
          {card.total.toLocaleString()}
          {card.hasMore ? "+" : ""}
        </span>
      </div>
      {rows.length === 0 ? (
        <div className="cite-result-empty">None.</div>
      ) : (
        <ul className="cite-result-rows">
          {rows.map((r, i) => {
            const open =
              r.open ?? (r.ref && canResolve(r.ref.kind) ? () => r.ref && openCite(r.ref) : null)
            const body = (
              <>
                <span className="cite-result-row-title" data-mono={!!r.mono}>
                  {r.title}
                </span>
                {r.status && (
                  <span className="cite-status" data-tone={r.status.tone}>
                    {r.status.label}
                  </span>
                )}
                {r.fact && <span className="cite-result-row-fact">{r.fact}</span>}
              </>
            )
            return (
              // biome-ignore lint/suspicious/noArrayIndexKey: rows are a fixed snapshot of one result
              <li key={i}>
                {open ? (
                  <button
                    type="button"
                    className="cite-result-row"
                    title={r.title}
                    onClick={(e) => {
                      e.preventDefault()
                      e.stopPropagation()
                      open(e.currentTarget)
                    }}
                  >
                    {body}
                  </button>
                ) : (
                  <div className="cite-result-row" title={r.title}>
                    {body}
                  </div>
                )}
              </li>
            )
          })}
        </ul>
      )}
      {(more > 0 || card.hasMore) && (
        <div className="cite-result-more">
          {more > 0 ? `${more.toLocaleString()} more` : ""}
          {card.hasMore ? `${more > 0 ? ", and " : ""}more past these` : ""}
        </div>
      )}
    </div>
  )
}

/** The passage without the lines both sides share at the start and the end. */
function changedPart(before: string, after: string) {
  const a = before.split("\n")
  const b = after.split("\n")
  let head = 0
  while (head < a.length && head < b.length && a[head] === b[head]) head++
  let tail = 0
  while (
    tail < a.length - head &&
    tail < b.length - head &&
    a[a.length - 1 - tail] === b[b.length - 1 - tail]
  )
    tail++
  const was = a.slice(head, a.length - tail).join("\n")
  const now = b.slice(head, b.length - tail).join("\n")
  // Nothing differs line by line (whitespace at the end, say): show it as given.
  return was || now ? { before: was, after: now } : { before, after }
}

/** Lines shown on each side of a changed passage. */
const DIFF_LINES = 6

function Passage({ text, side }: { text: string; side: "was" | "now" }) {
  const lines = text.split("\n")
  const shown = lines.slice(0, DIFF_LINES)
  const cut = lines.length - shown.length
  return (
    <div className="cite-result-passage" data-side={side}>
      {text === "" ? (
        <span className="cite-result-passage-empty">
          {side === "now" ? "(removed)" : "(nothing)"}
        </span>
      ) : (
        shown.map((l, i) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: lines of one fixed passage
          <div key={i} className="cite-result-passage-line">
            <span aria-hidden="true">{side === "was" ? "−" : "+"}</span>
            {l.length > 200 ? `${l.slice(0, 199)}…` : l || " "}
          </div>
        ))
      )}
      {cut > 0 && <div className="cite-result-passage-cut">{plural(cut, "more line")}</div>}
    </div>
  )
}

function CardBox({ card }: { card: ResultCard }) {
  const resolvable = !!card.ref && canResolve(card.ref.kind)
  // Asked only when the result left something out: a result is fresher than the cache.
  const needs = !card.title || !card.status || card.facts.length === 0
  const state = useRefState(card.ref?.kind ?? "", card.ref?.canonical ?? "", resolvable && needs)
  const entity =
    state.status === "found"
      ? state.entity
      : state.status === "loading" || state.status === "error"
        ? state.stale
        : undefined
  const title = card.title || entity?.title || card.ref?.canonical || ""
  const status = card.status ?? entity?.status
  const facts = (card.facts.length ? card.facts : (entity?.fields ?? [])).slice(0, 3)
  const Icon = ICONS[card.kind]
  const open =
    card.open ?? (resolvable && card.ref ? () => card.ref && openCite(card.ref) : undefined)
  return (
    <div className="cite-result" data-kind={card.kind}>
      <div className="cite-result-head">
        <Icon aria-hidden="true" size={12} strokeWidth={1.7} />
        <span className="cite-result-kind">{card.label ?? LABELS[card.kind]}</span>
        {status && (
          <span className="cite-status" data-tone={status.tone}>
            {status.label}
          </span>
        )}
        <span className="cite-result-spacer" />
        {open && (
          <button
            type="button"
            className="cite-result-open"
            onClick={(e) => {
              e.preventDefault()
              e.stopPropagation()
              open(e.currentTarget)
            }}
          >
            <Maximize2 aria-hidden="true" size={11} strokeWidth={1.7} />
            Open
          </button>
        )}
      </div>
      <div className="cite-result-title" title={title}>
        {title || (state.status === "loading" ? "Looking it up…" : "")}
      </div>
      {facts.length > 0 && (
        <dl className="cite-result-facts">
          {facts.map((f) => (
            <div key={f.label}>
              <dt>{f.label}</dt>
              <dd data-mono={!!f.mono} title={f.value}>
                {f.value}
              </dd>
            </div>
          ))}
        </dl>
      )}
      {card.diff && (
        <div className="cite-result-diff" title="What changed">
          {(() => {
            if (card.diff.before === null) return <Passage text={card.diff.after} side="now" />
            const d = changedPart(card.diff.before, card.diff.after)
            return (
              <>
                <Passage text={d.before} side="was" />
                <Passage text={d.after} side="now" />
              </>
            )
          })()}
        </div>
      )}
    </div>
  )
}
