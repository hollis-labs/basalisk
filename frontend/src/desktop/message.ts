/**
 * Sending a message, from anywhere on the desktop. composeMessage opens the compose box
 * (apps/inbox/compose.tsx) with what is given filled in: a recipient (a clicked address),
 * a subject and a body (a forwarded message, a scratch item, what a window is showing).
 * Nothing is sent until the owner sends it.
 */
import type { DeskAPI } from "./registry"

export interface MessageDraft {
  /** The recipient's address (msg://…). */
  to?: string
  subject?: string
  body?: string
  /** The provider's kind of message; the compose box offers notice and request. */
  kind?: string
}

/** The compose overlay's id (registerOverlay). */
export const MESSAGE_OVERLAY = "message"
/** Dispatched on window to open the compose box; detail is a MessageDraft. */
export const MESSAGE_EVENT = "basalisk:message"

/** A Tether address in running text. */
export const URN = /msg:\/\/[A-Za-z0-9][A-Za-z0-9._~/-]*[A-Za-z0-9_]/g

/** Open the compose box with this filled in. */
export function composeMessage(draft: MessageDraft = {}) {
  window.dispatchEvent(new CustomEvent<MessageDraft>(MESSAGE_EVENT, { detail: draft }))
}

/** What the focused window is showing, as a message to send on: its selection, page or file. */
export async function draftFromWindow(desk: DeskAPI): Promise<MessageDraft> {
  const c = await desk.context()
  if (!c) return {}
  const lines: string[] = []
  if (c.selection) lines.push(c.selection.replace(/^/gm, "> "), "")
  if (c.url) lines.push(c.url)
  if (c.file) lines.push(`\`${c.file.root}:${c.file.path}\``)
  return { subject: c.title, body: lines.join("\n").trim() }
}
