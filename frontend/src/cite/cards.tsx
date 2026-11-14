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
import { Brain, FileText, Globe, ListChecks, type LucideIcon, Maximize2, Zap } from "lucide-react"
import type { DeskAPI } from "../desktop/registry"
import { openFileRef } from "./files"
import { canResolve, openCite, type RefIdentity, useRefState } from "./index"
import "./cards.css"

export type CardKind = "torque.task" | "tesseract.item" | "file" | "page" | "frag"
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
}

/** Turns a successful call (its input, and its result parsed as JSON when it is JSON) into a card, or null. */
export type CardExtractor = (input: Record<string, unknown>, result: unknown) => ResultCard | null

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
): ResultCard | null {
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
}
const LABELS: Record<CardKind, string> = {
  "torque.task": "Torque task",
  "tesseract.item": "Tesseract",
  file: "File",
  page: "Web page",
  frag: "Frag",
}

/** The card for one tool call row, or nothing. */
export function ResultCardView({
  item,
}: {
  item: { name: string; input?: unknown; result?: string; isError?: boolean; running: boolean }
}) {
  if (item.running) return null
  const card = cardFor(item.name, item.input, item.result, !!item.isError)
  return card ? <CardBox card={card} /> : null
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
    </div>
  )
}
