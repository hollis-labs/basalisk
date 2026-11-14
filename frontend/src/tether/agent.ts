/**
 * The tools the server runs itself through the Tether gateway (internal/agent/tether.go):
 * Torque and Tesseract. They are not in the page's tool registry, so what the panel says of
 * them lives here: a line for each call that changes something (for the permission question
 * and the call's row), and the cards for the records they return.
 */
import {
  type CardExtractor,
  field,
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

registerResultCards({
  torque_task_get: taskResult,
  torque_task_create: taskResult,
  torque_task_update: taskResult,
  torque_task_transition: taskResult,
  torque_comment_add: taskNamed("entity_id"),
  torque_task_subtodo_add: taskNamed("task_id"),
  torque_task_subtodo_done: taskNamed("task_id"),
  tesseract_get: tesseractResult,
})
