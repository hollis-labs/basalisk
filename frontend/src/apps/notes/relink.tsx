/**
 * After a note in the notes folder is renamed: the notes that linked to it by its old name
 * ([[wiki]] links and relative links) are found, and the owner is shown each line that would
 * change before anything is written. On confirm, each file is rewritten through the file API
 * with If-Match; a file with unsaved edits in an open window is skipped and named.
 */
import { useState } from "react"
import type { FileRef } from "../../desktop/registry"
import { changed } from "../files/ops"
import { inFolder, notesFolder } from "./notefiles"
import { noteWindows } from "./open"
import {
  type Backlink,
  backlinks,
  candidates,
  dirOf,
  joinPath,
  linkName,
  loadIndex,
  type NoteIndex,
  WIKI,
} from "./wiki"

export interface FileChange {
  path: string
  etag?: string
  before: string
  after: string
  lines: { line: number; was: string; now: string }[]
}

export interface RelinkPlan {
  root: string
  from: string
  to: string
  files: FileChange[]
}

/** Before the rename: the notes linking to the old name (it must still resolve), or null. */
export async function linkersBefore(
  old: FileRef,
): Promise<{ index: NoteIndex; links: Backlink[] } | null> {
  const folder = await notesFolder()
  if (!inFolder(old, folder)) return null
  const index = await loadIndex(folder)
  const links = await backlinks(old, index)
  return links.length ? { index, links } : null
}

const contentURL = (root: string, path: string) =>
  `/api/files/content?root=${encodeURIComponent(root)}&path=${encodeURIComponent(path)}`

/** The path from folder `dir` to `target`, as a relative link. */
function relative(dir: string, target: string): string {
  const from = dir ? dir.split("/") : []
  const to = target.split("/")
  let i = 0
  while (i < from.length && i < to.length - 1 && from[i] === to[i]) i++
  return [...from.slice(i).map(() => ".."), ...to.slice(i)].join("/")
}

/** One line of prose with the links to `from` pointed at `to` (code spans left alone). */
function relinkLine(
  line: string,
  file: string,
  from: string,
  to: string,
  before: NoteIndex,
  after: NoteIndex,
): string {
  return line
    .split(/(`[^`\n]*`)/)
    .map((part, i) =>
      i % 2
        ? part
        : part
            .replace(WIKI, (all, name: string, hash?: string, label?: string) =>
              candidates(name, file, before)[0] === from
                ? `[[${linkName(to, after)}${hash ?? ""}${label !== undefined ? `|${label}` : ""}]]`
                : all,
            )
            .replace(/(\]\(\s*)(<[^>\n]*>|[^)\s]+)/g, (all, head: string, dest: string) => {
              const bare = dest.startsWith("<") ? dest.slice(1, -1) : dest
              if (
                !bare ||
                /^[a-z][a-z0-9+.-]*:/i.test(bare) ||
                bare.startsWith("/") ||
                bare.startsWith("#")
              )
                return all
              const hashAt = bare.indexOf("#")
              let p = hashAt >= 0 ? bare.slice(0, hashAt) : bare
              const hash = hashAt >= 0 ? bare.slice(hashAt) : ""
              try {
                p = decodeURI(p)
              } catch {
                // As written.
              }
              if (joinPath(dirOf(file), p) !== from) return all
              const rel = relative(dirOf(file), to) + hash
              return `${head}${/\s/.test(rel) || dest.startsWith("<") ? `<${rel}>` : rel}`
            }),
    )
    .join("")
}

/** After the rename: each linking file read in full, with the lines that would change. */
export async function planRelink(
  root: string,
  from: string,
  to: string,
  found: { index: NoteIndex; links: Backlink[] },
): Promise<RelinkPlan> {
  const before = found.index
  const after: NoteIndex = { ...before, notes: before.notes.map((p) => (p === from ? to : p)) }
  const files: FileChange[] = []
  for (const b of found.links) {
    const r = await fetch(contentURL(root, b.path), { credentials: "same-origin" })
    if (!r.ok) continue
    const text = await r.text()
    const lines = text.split("\n")
    const out: string[] = []
    const changes: FileChange["lines"] = []
    let fence: string | null = null
    lines.forEach((line, i) => {
      const m = /^ {0,3}(`{3,}|~{3,})/.exec(line)
      if (fence === null && m) fence = m[1]
      else if (fence !== null) {
        if (m && m[1][0] === fence[0] && m[1].length >= fence.length) fence = null
      } else {
        const now = relinkLine(line, b.path, from, to, before, after)
        if (now !== line) changes.push({ line: i + 1, was: line, now })
        out.push(now)
        return
      }
      out.push(line)
    })
    if (changes.length)
      files.push({
        path: b.path,
        etag: r.headers.get("ETag") ?? undefined,
        before: text,
        after: out.join("\n"),
        lines: changes,
      })
  }
  return { root, from, to, files }
}

export interface RelinkResult {
  changed: string[]
  skipped: string[]
  failed: { path: string; why: string }[]
}

/** Writes the planned changes, file by file. */
export async function applyRelink(plan: RelinkPlan): Promise<RelinkResult> {
  const res: RelinkResult = { changed: [], skipped: [], failed: [] }
  for (const f of plan.files) {
    const ref = { root: plan.root, path: f.path }
    const showing = noteWindows.showing(ref)
    if (showing.some(([, n]) => n.dirty())) {
      res.skipped.push(f.path)
      continue
    }
    const headers: Record<string, string> = {
      "Content-Type": "text/plain; charset=utf-8",
      "X-Requested-With": "XMLHttpRequest",
    }
    if (f.etag) headers["If-Match"] = f.etag
    const r = await fetch(contentURL(plan.root, f.path), {
      method: "PUT",
      credentials: "same-origin",
      headers,
      body: f.after,
    })
    if (!r.ok) {
      res.failed.push({
        path: f.path,
        why: r.status === 412 ? "it changed since it was read" : `error ${r.status}`,
      })
      continue
    }
    const etag = r.headers.get("ETag") ?? undefined
    for (const [, n] of showing) n.replaced(f.after, etag)
    res.changed.push(f.path)
  }
  if (res.changed.length) changed(plan.root, [...new Set(res.changed.map(dirOf))])
  return res
}

const base = (p: string) => p.split("/").pop() ?? p

/** The list to confirm, then what happened. */
export function RelinkPanel({ plan, onClose }: { plan: RelinkPlan; onClose: () => void }) {
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<RelinkResult | null>(null)
  const count = plan.files.reduce((n, f) => n + f.lines.length, 0)
  if (result)
    return (
      <div className="notes-relink" role="status">
        <p>
          {result.changed.length
            ? `Updated the links in ${result.changed.length} note${result.changed.length === 1 ? "" : "s"}.`
            : "No note was changed."}
          {result.skipped.length > 0 &&
            ` Skipped, because they have unsaved edits: ${result.skipped.map(base).join(", ")}.`}
          {result.failed.length > 0 &&
            ` Not changed: ${result.failed.map((f) => `${base(f.path)} (${f.why})`).join(", ")}.`}
        </p>
        <div className="notes-relink-actions">
          <button type="button" className="desk-button" onClick={onClose}>
            Close
          </button>
        </div>
      </div>
    )
  return (
    <div className="notes-relink" role="dialog" aria-label="Update links to the renamed note">
      <p>
        {plan.files.length} note{plan.files.length === 1 ? "" : "s"} link to{" "}
        <strong>{base(plan.from)}</strong> by its old name ({count} line{count === 1 ? "" : "s"}).
        Update them to <strong>{base(plan.to)}</strong>?
      </p>
      <div className="notes-relink-list">
        {plan.files.map((f) => (
          <div key={f.path} className="notes-relink-file">
            <div className="notes-relink-path">{f.path}</div>
            {f.lines.map((l) => (
              <div key={l.line} className="notes-relink-line">
                <span className="notes-relink-no">{l.line}</span>
                <div>
                  <div data-was="true">− {l.was}</div>
                  <div data-now="true">+ {l.now}</div>
                </div>
              </div>
            ))}
          </div>
        ))}
      </div>
      <div className="notes-relink-actions">
        <button type="button" className="desk-button" disabled={busy} onClick={onClose}>
          Leave them
        </button>
        <button
          type="button"
          className="desk-button"
          data-primary="true"
          disabled={busy}
          onClick={async () => {
            setBusy(true)
            try {
              setResult(await applyRelink(plan))
            } finally {
              setBusy(false)
            }
          }}
        >
          Update {plan.files.length} note{plan.files.length === 1 ? "" : "s"}
        </button>
      </div>
    </div>
  )
}
