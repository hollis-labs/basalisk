/**
 * The Notes windows that show a file, so something outside a window (the agent's tools) can
 * read what it shows and hand it new text without going around it.
 */
import type { FileRef } from "../../desktop/registry"

export interface OpenNote {
  file: FileRef
  /** The text as saved (the version the window read or last wrote). */
  saved(): string
  /** What the window shows: the edit in progress, else the saved text. */
  shown(): string
  /** True while the window has an edit that is not saved. */
  dirty(): boolean
  /** The file was written from outside: show this text (and keep editing, if it was). */
  replaced(text: string, etag?: string): void
}

const open = new Map<string, OpenNote>()

export const noteWindows = {
  set(winId: string, note: OpenNote | null) {
    if (note) open.set(winId, note)
    else open.delete(winId)
  },
  get: (winId: string) => open.get(winId),
  /** The windows showing this file. */
  showing: (file: FileRef) =>
    [...open.entries()].filter(([, n]) => n.file.root === file.root && n.file.path === file.path),
}
