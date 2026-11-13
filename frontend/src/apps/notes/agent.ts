/**
 * The agent's tools for notes: read one, list them, write one, edit a passage. Files go
 * through the file API. A note open in a window is read as the window shows it, and a change
 * to it is shown there at once; a window with unsaved edits is never written under: the tool
 * refuses and says so, and the owner's edit stays as it is.
 */
import { registerAgentTools } from "../../agent/tools"
import { ApiError } from "../../api/account"
import { files } from "../../api/files"
import type { FileRef } from "../../desktop/registry"
import { changed } from "../files/ops"
import { noteName, notesFolder } from "./notefiles"
import { noteWindows } from "./open"

const TEXT = /\.(md|markdown|mdx|txt|text|log|rst|org)$/i
/** How much of a note one read returns. */
const READ_LIMIT = 60_000

const object = (properties: Record<string, unknown>, required: string[] = []) => ({
  type: "object",
  properties,
  required,
  additionalProperties: false,
})
const str = (description: string) => ({ type: "string", description })
const WHICH = {
  window_id: str("A Notes window id (from desktop_windows): the note it shows."),
  root: str(
    "The place the note is in, as notes_list or window_context report it. Leave out for the notes folder.",
  ),
  path: str("The note's path inside root; without root, relative to the notes folder."),
}

const contentURL = (f: FileRef) =>
  `/api/files/content?root=${encodeURIComponent(f.root)}&path=${encodeURIComponent(f.path)}`

async function readFile(f: FileRef): Promise<{ text: string; etag?: string } | null> {
  const r = await fetch(contentURL(f), { credentials: "same-origin" })
  if (r.status === 404) return null
  if (!r.ok) throw new Error(`${f.root}:${f.path} could not be read (${await message(r)}).`)
  return { text: await r.text(), etag: r.headers.get("ETag") ?? undefined }
}

async function writeFile(f: FileRef, text: string, etag?: string): Promise<string | undefined> {
  const headers: Record<string, string> = {
    "Content-Type": "text/plain; charset=utf-8",
    "X-Requested-With": "XMLHttpRequest",
  }
  if (etag) headers["If-Match"] = etag
  const r = await fetch(contentURL(f), {
    method: "PUT",
    credentials: "same-origin",
    headers,
    body: text,
  })
  if (r.status === 412)
    throw new Error(`${f.path} changed on disk while this was being done. Read it again and retry.`)
  if (!r.ok) throw new Error(`${f.path} could not be written (${await message(r)}).`)
  return r.headers.get("ETag") ?? undefined
}

async function message(r: Response): Promise<string> {
  try {
    return ((await r.json()) as { message?: string }).message ?? String(r.status)
  } catch {
    return String(r.status)
  }
}

/** The note a call means: a window's, or a root and path (the notes folder by default). */
async function which(input: Record<string, unknown>): Promise<{ file: FileRef; winId?: string }> {
  const winId = typeof input.window_id === "string" ? input.window_id : ""
  if (winId) {
    const open = noteWindows.get(winId)
    if (!open)
      throw new Error(
        `Window ${winId} is not a Notes window showing a file. desktop_windows lists the windows.`,
      )
    return { file: open.file, winId }
  }
  const path = typeof input.path === "string" ? input.path.trim().replace(/^\/+/, "") : ""
  if (!path)
    throw new Error("Give window_id, or path (and root, unless it is in the notes folder).")
  if (path.split("/").includes("..")) throw new Error("path may not contain '..'.")
  if (typeof input.root === "string" && input.root) return { file: { root: input.root, path } }
  const folder = await notesFolder()
  return { file: { root: folder.root, path: folder.path ? `${folder.path}/${path}` : path } }
}

/** Refuses when a window showing the file has unsaved edits; returns the windows showing it. */
function guard(file: FileRef) {
  const showing = noteWindows.showing(file)
  const busy = showing.find(([, n]) => n.dirty())
  if (busy)
    throw new Error(
      `The owner has unsaved edits to ${file.path} in window ${busy[0]}. Nothing was changed; ask them to save or discard their edits first.`,
    )
  return showing
}

/** Writes and shows the new text in every window showing the file. */
async function put(file: FileRef, text: string, etag?: string) {
  const showing = guard(file)
  const next = await writeFile(file, text, etag)
  for (const [, n] of showing) n.replaced(text, next)
  return showing.map(([id]) => id)
}

/** The note a call means, in a word (sync and best effort: never throws). */
function noteLabel(input: Record<string, unknown>): string {
  try {
    const id = typeof input.window_id === "string" ? input.window_id : ""
    const path = id ? (noteWindows.get(id)?.file.path ?? "") : String(input.path ?? "")
    return path.split("/").pop() || "the note"
  } catch {
    return "the note"
  }
}

registerAgentTools([
  {
    name: "notes_read",
    level: "read",
    description: `Read a note: from a Notes window (as it shows it, including unsaved edits) or a file by root and path. Returns the text, where it is, and whether a window has unsaved edits. Long notes are cut at ${READ_LIMIT} characters; use offset to read on.`,
    input_schema: object({
      ...WHICH,
      offset: {
        type: "integer",
        description: "Start this many characters in (to read past a cut).",
      },
    }),
    run: async (input) => {
      const { file, winId } = await which(input)
      const open = winId ? noteWindows.get(winId) : noteWindows.showing(file)[0]?.[1]
      let text: string
      if (open) text = open.shown()
      else {
        const got = await readFile(file)
        if (!got)
          throw new Error(`There is no note at ${file.root}:${file.path}. notes_list lists them.`)
        text = got.text
      }
      const offset =
        typeof input.offset === "number" && input.offset > 0 ? Math.floor(input.offset) : 0
      const part = text.slice(offset, offset + READ_LIMIT)
      return {
        root: file.root,
        path: file.path,
        ...(open
          ? { open_in: winId ?? noteWindows.showing(file)[0][0], unsaved_edits: open.dirty() }
          : {}),
        length: text.length,
        ...(offset ? { offset } : {}),
        text: part,
        ...(offset + READ_LIMIT < text.length
          ? { note: `cut at ${READ_LIMIT} characters; read on with offset ${offset + READ_LIMIT}` }
          : {}),
      }
    },
  },
  {
    name: "notes_list",
    level: "read",
    description:
      "List the notes (markdown and text files) in the notes folder and below, or the ones whose names contain every word of a query. Returns root and path for notes_read.",
    input_schema: object({
      query: str("Words the file name must contain (case-insensitive)."),
      max: { type: "integer", description: "At most this many (default 100, up to 300)." },
    }),
    run: async (input) => {
      const folder = await notesFolder()
      const q = typeof input.query === "string" ? input.query.trim() : ""
      const max =
        typeof input.max === "number" ? Math.max(1, Math.min(300, Math.floor(input.max))) : 100
      let found: Awaited<ReturnType<typeof files.search>>
      try {
        found = await files.search(folder.root, folder.path, q || ".")
      } catch (e) {
        if (e instanceof ApiError && e.status === 404)
          return {
            root: folder.root,
            folder: folder.path,
            total: 0,
            notes: [],
            note: "the notes folder does not exist yet",
          }
        throw e
      }
      const notes = found.entries
        .filter((e) => !e.dir && TEXT.test(e.name))
        .sort((a, b) => b.mtime.localeCompare(a.mtime))
      return {
        root: folder.root,
        folder: folder.path,
        total: notes.length,
        notes: notes.slice(0, max).map((e) => ({ path: e.path, modified: e.mtime, size: e.size })),
        ...(notes.length > max || found.truncated
          ? {
              note: `${found.truncated ? "the search stopped early; " : ""}showing ${Math.min(max, notes.length)}; narrow with query`,
            }
          : {}),
      }
    },
  },
  {
    name: "notes_write",
    level: "write",
    description:
      "Replace a note's whole text, or create the note if it does not exist (folders too; .md is added to a name without an extension). A window showing the note shows the new text. Refused if a window has unsaved edits to it. To change part of a note, use notes_edit.",
    input_schema: object({ ...WHICH, text: str("The note's new text (markdown).") }, ["text"]),
    describe: (input) => `Replace the text of ${noteLabel(input)} (or create it)`,
    run: async (input) => {
      if (typeof input.text !== "string") throw new Error("text must be a string.")
      const { file: asked } = await which(input)
      const dir = asked.path.includes("/") ? asked.path.slice(0, asked.path.lastIndexOf("/")) : ""
      const base = noteName(asked.path.slice(dir.length ? dir.length + 1 : 0))
      const file = { root: asked.root, path: dir ? `${dir}/${base}` : base }
      if (!TEXT.test(file.path))
        throw new Error("Notes are markdown or text files (.md, .txt, …); that name is not one.")
      guard(file)
      const old = await readFile(file)
      if (!old) {
        if (dir)
          await files.mkdir(file.root, dir).catch((e) => {
            if (!(e instanceof ApiError && e.status === 409)) throw e
          })
        await files.create(file.root, file.path)
        changed(file.root, [dir])
      }
      const shown = await put(file, input.text, old?.etag)
      return {
        root: file.root,
        path: file.path,
        created: !old,
        length: input.text.length,
        ...(shown.length ? { shown_in: shown } : {}),
      }
    },
  },
  {
    name: "notes_edit",
    level: "write",
    description:
      "Change one passage of a note: old_text must occur exactly once in it (include enough around it to make it unique); it is replaced by new_text. A window showing the note shows the change. Refused if a window has unsaved edits to it.",
    input_schema: object(
      {
        ...WHICH,
        old_text: str("The exact text to replace, as it is in the note."),
        new_text: str("What to put in its place (empty to delete it)."),
      },
      ["old_text", "new_text"],
    ),
    describe: (input) => `Edit ${noteLabel(input)}: 1 passage`,
    run: async (input) => {
      const oldText = typeof input.old_text === "string" ? input.old_text : ""
      const newText = typeof input.new_text === "string" ? input.new_text : null
      if (!oldText) throw new Error("old_text is empty: give the exact passage to replace.")
      if (newText === null) throw new Error("new_text must be a string (empty to delete).")
      const { file } = await which(input)
      guard(file)
      const got = await readFile(file)
      if (!got)
        throw new Error(`There is no note at ${file.root}:${file.path}. notes_list lists them.`)
      const at = got.text.indexOf(oldText)
      if (at < 0)
        throw new Error(
          "old_text is not in the note. Read it with notes_read and copy the passage exactly.",
        )
      const count = got.text.split(oldText).length - 1
      if (count > 1)
        throw new Error(
          `old_text occurs ${count} times. Include more of the text around it so it occurs once.`,
        )
      const text = got.text.slice(0, at) + newText + got.text.slice(at + oldText.length)
      const shown = await put(file, text, got.etag)
      const line = got.text.slice(0, at).split("\n").length
      return {
        root: file.root,
        path: file.path,
        changed_at_line: line,
        length: text.length,
        ...(shown.length ? { shown_in: shown } : {}),
      }
    },
  },
])
