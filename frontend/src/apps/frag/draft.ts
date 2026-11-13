/**
 * The capture being written, kept in this browser's local storage (like Notes' unsaved
 * edits) so it survives the overlay closing and a reload. The capture id belongs to one
 * text: it stays while content and tags stay the same, so saving twice queues once.
 */
export interface Draft {
  content: string
  tags: string
  draftAt: string
  captureId: string
  /** content + tags the id was made for. */
  idFor: string
  /** The moment the draft began (session, focused window), read once. */
  context?: unknown
}

const KEY = "basalisk.frag.draft"

export function newDraft(): Draft {
  return { content: "", tags: "", draftAt: new Date().toISOString(), captureId: "", idFor: "" }
}

export function readDraft(): Draft {
  try {
    const raw = window.localStorage.getItem(KEY)
    return raw ? { ...newDraft(), ...(JSON.parse(raw) as Draft) } : newDraft()
  } catch {
    return newDraft()
  }
}

export function writeDraft(d: Draft | null) {
  try {
    if (d === null) window.localStorage.removeItem(KEY)
    else window.localStorage.setItem(KEY, JSON.stringify(d))
  } catch {
    // Not kept.
  }
}

export const parseTags = (s: string) =>
  s
    .split(",")
    .map((t) => t.trim())
    .filter(Boolean)

/** The id for the draft's current text: the same one, or a new one if the text changed. */
export function captureIdFor(d: Draft): Draft {
  const key = `${d.content}\u0000${parseTags(d.tags).join(",")}`
  if (d.captureId && d.idFor === key) return d
  const rand = Array.from(crypto.getRandomValues(new Uint8Array(12)), (b) =>
    b.toString(16).padStart(2, "0"),
  ).join("")
  return { ...d, captureId: `frag-${rand}`, idFor: key }
}
