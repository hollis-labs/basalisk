import { ApiError, request } from "./account"

export type CollabKind = "draft" | "artifact" | "diagram"

/** A document the owner and the agent work on together (internal/collab Doc). */
export interface CollabDoc {
  name: string
  kind: CollabKind
  title: string
  lang?: string
  project?: string
  ref?: string
  created?: string
  rev: number
  updated?: string
  body: string
}

/** What a mark points at; which fields are set depends on the document. */
export interface CollabAnchor {
  quote?: string
  prefix?: string
  suffix?: string
  line_start?: number
  line_end?: number
  x?: number
  y?: number
  w?: number
  h?: number
  page?: number
  time?: number
  node?: string
  edge?: string
  selector?: string
}

export interface CollabMark {
  id: string
  rev: number
  anchor: CollabAnchor
  kind: "comment" | "suggestion" | "question"
  text?: string
  replace?: string
  by: "user" | "agent"
  state: "open" | "done" | "declined"
  at: string
  reply?: string
}

export interface CollabRevision {
  rev: number
  at: string
  title: string
}

/** Dispatched on window when a document or its marks changed (detail: its name). */
export const COLLAB_CHANGED = "basalisk:collab"
const changed = (name: string) =>
  window.dispatchEvent(new CustomEvent<string>(COLLAB_CHANGED, { detail: name }))

const at = (name: string) => `collab/${encodeURIComponent(name)}`

export const collab = {
  list: (kind?: CollabKind) =>
    request<{ docs: CollabDoc[] }>(`collab${kind ? `?kind=${kind}` : ""}`).then((r) => r.docs),
  get: (name: string, rev = 0) =>
    request<{ doc: CollabDoc; marks: CollabMark[] }>(`${at(name)}${rev ? `?rev=${rev}` : ""}`),
  create: (
    d: Partial<CollabDoc> & { kind: CollabKind; title: string },
    by: "user" | "agent" = "user",
  ) =>
    request<{ doc: CollabDoc; marks: CollabMark[] }>("collab", "POST", { ...d, by }).then((r) => {
      changed(r.doc.name)
      return r.doc
    }),
  /** Save a new revision. Throws an ApiError with status 409 when it changed meanwhile. */
  save: (
    name: string,
    body: string,
    baseRev: number,
    title?: string,
    by: "user" | "agent" = "user",
  ) =>
    request<{ doc: CollabDoc }>(at(name), "PUT", { body, base_rev: baseRev, title, by }).then(
      (r) => {
        changed(name)
        return r.doc
      },
    ),
  log: (name: string) =>
    request<{ revisions: CollabRevision[] }>(`${at(name)}/log`).then((r) => r.revisions ?? []),
  mark: (name: string, op: "add" | "update" | "remove", mark: Partial<CollabMark>) =>
    request<{ marks: CollabMark[] }>(`${at(name)}/marks`, "POST", { op, mark }).then((r) => {
      changed(name)
      return r.marks
    }),
}

export const isConflict = (e: unknown) => e instanceof ApiError && e.status === 409
