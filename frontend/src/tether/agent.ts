/**
 * The tools the server runs itself through the Tether gateway (internal/agent/tether.go):
 * Torque and Tesseract. They are not in the page's tool registry, so what the panel says of
 * them lives here: a line for each call that changes something (for the permission question
 * and the call's row), and the cards for the records they return.
 */
import {
  type CardExtractor,
  field,
  type ListCard,
  type ListRow,
  type ResultCard,
  registerResultCards,
  text,
  toneOf,
} from "../cite/cards"

const s = (v: unknown) => (typeof v === "string" ? v.trim().replace(/\s+/g, " ") : "")
const clip = (t: string, n = 60) => (t.length > n ? `${t.slice(0, n - 1)}…` : t)

/** One plain line per Torque write: what the call will do. */
const DESCRIBE: Record<string, (input: Record<string, unknown>) => string> = {
  torque_task_create: (i) => {
    const t = s(i.title)
    return t ? `Create the Torque task “${clip(t)}”` : "Create a Torque task"
  },
  torque_task_update: (i) => {
    const id = s(i.id)
    const what = Object.keys(i)
      .filter((k) => k !== "id")
      .map((k) => k.replaceAll("_", " "))
    if (s(i.status)) return `Set ${id || "a task"} to ${s(i.status)}`
    if (!what.length) return `Update ${id || "a Torque task"}`
    const list =
      what.length > 3
        ? `${what.slice(0, 3).join(", ")} and more`
        : what.length > 1
          ? `${what.slice(0, -1).join(", ")} and ${what[what.length - 1]}`
          : what[0]
    return `Change the ${list} of ${id || "a Torque task"}`
  },
  torque_task_transition: (i) => {
    const id = s(i.id) || "a Torque task"
    const to = s(i.status)
    const note = s(i.comment)
    return `${to ? `Move ${id} to ${to}` : `Change the status of ${id}`}${note ? `, noting “${clip(note, 48)}”` : ""}`
  },
  torque_comment_add: (i) => {
    const on = s(i.entity_id) || `a ${s(i.entity_type) || "task"}`
    const c = s(i.content)
    return c ? `Comment on ${on}: “${clip(c)}”` : `Comment on ${on}`
  },
  torque_task_subtodo_add: (i) => {
    const t = s(i.text)
    return `Add ${t ? `“${clip(t)}”` : "an item"} to the checklist of ${s(i.task_id) || "a task"}${i.required === true ? " (required)" : ""}`
  },
  torque_task_subtodo_done: (i) =>
    `Tick off “${s(i.id) || "an item"}” on ${s(i.task_id) || "a task"}${s(i.evidence) ? `, with “${clip(s(i.evidence), 40)}”` : ""}`,
}

/** The line for a server-run call, or "". */
export function describeServerCall(name: string, input: unknown): string {
  const fn = DESCRIBE[name]
  if (!fn) return ""
  try {
    return fn((input && typeof input === "object" ? input : {}) as Record<string, unknown>)
  } catch {
    return ""
  }
}

// ---- Cards ----

const TASK_ID = /^CW-\d{8}-\d{4}$/

/** A Torque task record (data = TaskRecord, either case), as a card. */
function taskCard(rec: unknown, fallbackId = ""): ResultCard | null {
  const id = text(rec, "id") || fallbackId
  if (!TASK_ID.test(id)) return null
  const title = text(rec, "title")
  const status = text(rec, "status")
  const priority = field(rec, "priority")
  const facts: ResultCard["facts"] = []
  if (typeof priority === "number") facts.push({ label: "Priority", value: `P${priority}` })
  const project = text(rec, "project_id")
  if (project) facts.push({ label: "Project", value: project, mono: true })
  facts.push({ label: "Id", value: id, mono: true })
  return {
    kind: "torque.task",
    title,
    ...(status ? { status: { label: status, tone: toneOf(status) } } : {}),
    // Only the id came back (a comment, a checklist): the cite resolver fills the rest.
    facts: title ? facts : [],
    ref: { kind: "torque.task", canonical: id },
  }
}

const dataOf = (r: unknown) => field(r, "data")

/** A task returned whole: get, create, update, transition. */
const taskResult: CardExtractor = (_in, r) => {
  const d = dataOf(r)
  return d && !Array.isArray(d) ? taskCard(d) : null
}

/** A call that names a task and returns something else (a comment, the checklist). */
const taskNamed =
  (key: string): CardExtractor =>
  (input) => {
    if (key === "entity_id" && s(input.entity_type) && s(input.entity_type) !== "task") return null
    return taskCard(null, s(input[key]))
  }

const firstLine = (t: string) =>
  (t.split("\n").find((l) => l.trim()) ?? "").trim().replace(/^#+\s*/, "")

/** A Tesseract record (tesseract_get), as a card. */
const tesseractResult: CardExtractor = (_in, r) => {
  const rec = field(r, "item") ?? field(r, "revision") ?? r
  if (!rec || typeof rec !== "object") return null
  const domain = text(rec, "domain")
  const namespace = text(rec, "namespace")
  const key = text(rec, "key") || text(rec, "memory_key")
  const itemId = text(rec, "item_id")
  const payload = field(rec, "payload")
  const body = text(payload, "body")
  const summary = text(payload, "summary")
  let title = firstLine(body)
  if (!title || title.length > 140) title = firstLine(summary)
  if (!title) title = key || itemId
  if (!title) return null
  const ref =
    namespace && key
      ? {
          kind: "tesseract.key",
          canonical: `${domain ? `${domain}@` : ""}${namespace}#${key}`,
        }
      : /^[0-7][0-9A-HJKMNP-TV-Z]{25}$/.test(itemId)
        ? { kind: "tesseract.item", canonical: itemId }
        : undefined
  const status = text(rec, "status")
  const facts: ResultCard["facts"] = []
  if (domain) facts.push({ label: "Domain", value: domain })
  if (namespace) facts.push({ label: "Namespace", value: namespace, mono: true })
  if (key) facts.push({ label: "Key", value: key, mono: true })
  return {
    kind: "tesseract.item",
    title: title.length > 140 ? `${title.slice(0, 139)}…` : title,
    ...(status ? { status: { label: status, tone: toneOf(status) } } : {}),
    facts,
    ...(ref ? { ref } : {}),
  }
}

/** torque_task_list: data.items (brief or full records, either case) and data.meta. */
const taskList: CardExtractor = (_in, r) => {
  const d = dataOf(r)
  const items = field(d, "items")
  if (!Array.isArray(items)) return null
  const rows: ListRow[] = []
  for (const it of items) {
    const id = text(it, "id")
    if (!TASK_ID.test(id)) continue
    const status = text(it, "status")
    const priority = field(it, "priority")
    rows.push({
      title: text(it, "title") || id,
      ...(status ? { status: { label: status, tone: toneOf(status) } } : {}),
      fact: typeof priority === "number" ? `P${priority}` : id,
      ref: { kind: "torque.task", canonical: id },
    })
  }
  const meta = field(d, "meta")
  const total = field(meta, "total")
  const card: ListCard = {
    list: true,
    kind: "torque.task",
    heading: "Torque tasks",
    total: typeof total === "number" ? total : rows.length,
    rows,
    hasMore: typeof total !== "number" && field(meta, "has_more") === true,
  }
  return card
}

/** The kind of note, from where it lives: ".../memory/decisions" is a Decision. */
const typeOf = (namespace: string) => {
  const seg = (namespace.split("/").pop() ?? "").replace(/s$/, "")
  return seg ? seg.charAt(0).toUpperCase() + seg.slice(1) : ""
}

/** tesseract_recall: results[] of {revision} or {item}, and manifest.results_total. */
const recallList: CardExtractor = (_in, r) => {
  const results = field(r, "results")
  if (!Array.isArray(results)) return null
  const rows: ListRow[] = []
  for (const hit of results) {
    const rec = field(hit, "revision") ?? field(hit, "item")
    if (!rec || typeof rec !== "object") continue
    const domain = text(rec, "domain")
    const namespace = text(rec, "namespace")
    const key = text(rec, "key") || text(rec, "memory_key")
    const itemId = text(rec, "item_id")
    const payload = field(rec, "payload")
    const summary = firstLine(text(payload, "summary")) || firstLine(text(payload, "body"))
    const status = text(rec, "status")
    const ref =
      namespace && key
        ? { kind: "tesseract.key", canonical: `${domain ? `${domain}@` : ""}${namespace}#${key}` }
        : /^[0-7][0-9A-HJKMNP-TV-Z]{25}$/.test(itemId)
          ? { kind: "tesseract.item", canonical: itemId }
          : undefined
    rows.push({
      title: summary || key || itemId,
      mono: !summary,
      ...(status ? { status: { label: status, tone: toneOf(status) } } : {}),
      fact: typeOf(namespace) || domain,
      ...(ref ? { ref } : {}),
    })
  }
  const total = field(field(r, "manifest"), "results_total")
  const card: ListCard = {
    list: true,
    kind: "tesseract.item",
    heading: "Tesseract",
    total: typeof total === "number" ? Math.max(total, rows.length) : rows.length,
    rows,
  }
  return card
}

registerResultCards({
  torque_task_list: taskList,
  tesseract_recall: recallList,
  torque_task_get: taskResult,
  torque_task_create: taskResult,
  torque_task_update: taskResult,
  torque_task_transition: taskResult,
  torque_comment_add: taskNamed("entity_id"),
  torque_task_subtodo_add: taskNamed("task_id"),
  torque_task_subtodo_done: taskNamed("task_id"),
  tesseract_get: tesseractResult,
})
