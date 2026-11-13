/**
 * [[Wiki links]] between notes in the notes folder: [[Name]], [[Name#Heading]],
 * [[Name|label]]. A name is a note's file name without .md (case does not matter), or a path
 * ending in it (sub/Name). The nearest note wins: the same folder, then below it, then
 * anywhere in the notes folder. Backlinks are found on demand with the content search.
 */
import { useEffect, useState } from "react"
import { request } from "../../api/account"
import type { FileRef } from "../../desktop/registry"
import { CHANGED_EVENT } from "../files/ops"
import { inFolder, notesFolder } from "./notefiles"

export interface NoteIndex {
  root: string
  /** The notes folder's path in its root. */
  folder: string
  /** Every note's path in the root. */
  notes: string[]
}

const EXT = /\.(md|markdown|mdx|txt)$/i
const dirOf = (p: string) => (p.includes("/") ? p.slice(0, p.lastIndexOf("/")) : "")
const stem = (p: string) => (p.split("/").pop() ?? p).replace(EXT, "")

// One index per notes folder, until a file under it changes.
const cache = new Map<string, Promise<NoteIndex>>()
const keyOf = (f: FileRef) => `${f.root}:${f.path}`
if (typeof window !== "undefined")
  window.addEventListener(CHANGED_EVENT, (e) => {
    const root = (e as CustomEvent<{ root: string }>).detail?.root
    for (const k of [...cache.keys()]) if (!root || k.startsWith(`${root}:`)) cache.delete(k)
  })

export function loadIndex(folder: FileRef): Promise<NoteIndex> {
  const k = keyOf(folder)
  let p = cache.get(k)
  if (!p) {
    p = request<{ notes: { path: string }[] }>(
      `files/notes-index?root=${encodeURIComponent(folder.root)}&path=${encodeURIComponent(folder.path)}`,
    ).then((r) => ({ root: folder.root, folder: folder.path, notes: r.notes.map((n) => n.path) }))
    p.catch(() => cache.delete(k))
    cache.set(k, p)
  }
  return p
}

/** The notes index, when the file is in the notes folder (else null: no wiki links there). */
export function useNoteIndex(file: FileRef | undefined): NoteIndex | null {
  const [index, setIndex] = useState<NoteIndex | null>(null)
  const [nonce, setNonce] = useState(0)
  useEffect(() => {
    const bump = () => setNonce((n) => n + 1)
    window.addEventListener(CHANGED_EVENT, bump)
    return () => window.removeEventListener(CHANGED_EVENT, bump)
  }, [])
  const key = file ? keyOf(file) : ""
  // biome-ignore lint/correctness/useExhaustiveDependencies: key is the file, serialized; nonce reloads
  useEffect(() => {
    if (!file) {
      setIndex(null)
      return
    }
    let live = true
    void notesFolder()
      .then(async (folder) => (inFolder(file, folder) ? loadIndex(folder) : null))
      .then((ix) => live && setIndex(ix))
      .catch(() => live && setIndex(null))
    return () => {
      live = false
    }
  }, [key, nonce])
  return index
}

/** The notes a name could mean, nearest to `from` (a note's path) first. */
export function candidates(name: string, from: string, index: NoteIndex): string[] {
  const target = name.trim().replace(EXT, "").toLowerCase()
  if (!target) return []
  const matches = index.notes.filter((p) => {
    const bare = p.replace(EXT, "").toLowerCase()
    return target.includes("/")
      ? bare === target || bare.endsWith(`/${target}`)
      : stem(p).toLowerCase() === target
  })
  const here = dirOf(from)
  const rank = (p: string) => {
    const d = dirOf(p)
    const depth = p.split("/").length
    if (d === here) return 0
    if (here ? d.startsWith(`${here}/`) : true) return 100 + depth
    return 1000 + depth
  }
  return matches.sort((a, b) => rank(a) - rank(b) || a.length - b.length || a.localeCompare(b))
}

export const WIKI = /\[\[([^[\]\n|#]+)(#[^[\]\n|]*)?(?:\|([^[\]\n]*))?\]\]/g

/** Prose with each [[link]] made a markdown link: to the note, or to create it. */
export function rewriteWiki(
  text: string,
  file: FileRef,
  index: NoteIndex,
  fileLink: (root: string, path: string, anchor: string) => string,
): string {
  return text.replace(WIKI, (_all, name: string, hash: string | undefined, label?: string) => {
    const shown = (label ?? `${name}${hash ?? ""}`).trim().replace(/[[\]]/g, "") || name
    const hit = candidates(name, file.path, index)[0]
    const to = hit
      ? fileLink(index.root, hit, (hash ?? "").slice(1))
      : `<#notes-new:${encodeURIComponent(name.trim())}>`
    return `[${shown}](${to})`
  })
}

/** The name to insert for a note: its file name, or a path when that is not unique. */
export function linkName(path: string, index: NoteIndex): string {
  const s = stem(path)
  const same = index.notes.filter((p) => stem(p).toLowerCase() === s.toLowerCase())
  if (same.length < 2) return s
  const base = index.folder ? `${index.folder}/` : ""
  return (path.startsWith(base) ? path.slice(base.length) : path).replace(EXT, "")
}

/** Notes whose names contain the typed text, for [[ completion; nearest first. */
export function complete(query: string, from: string, index: NoteIndex, max = 8): string[] {
  const q = query.trim().toLowerCase()
  const here = dirOf(from)
  const base = index.folder ? `${index.folder}/` : ""
  const inside = (p: string) => (p.startsWith(base) ? p.slice(base.length) : p)
  return index.notes
    .filter((p) => p !== from && (!q || inside(p).replace(EXT, "").toLowerCase().includes(q)))
    .map((p) => {
      const s = stem(p).toLowerCase()
      const score =
        (s === q ? 0 : s.startsWith(q) ? 1 : s.includes(q) ? 2 : 3) * 10 +
        (dirOf(p) === here ? 0 : 1)
      return { p, score }
    })
    .sort((a, b) => a.score - b.score || a.p.length - b.p.length)
    .slice(0, max)
    .map((x) => x.p)
}

// ---- Backlinks ----

export interface Backlink {
  path: string
  lines: { line: number; text: string }[]
}

interface GrepMatch {
  path: string
  line: number
  text: string
}

function joinPath(dir: string, rel: string): string | null {
  const out = dir ? dir.split("/") : []
  for (const part of rel.split("/")) {
    if (part === "" || part === ".") continue
    if (part === "..") {
      if (!out.length) return null
      out.pop()
    } else out.push(part)
  }
  return out.join("/")
}

/** Does this line (in the note at `from`) link to `target`? */
function linksTo(line: string, from: string, target: string, index: NoteIndex): boolean {
  for (const m of line.matchAll(WIKI)) if (candidates(m[1], from, index)[0] === target) return true
  for (const m of line.matchAll(/\]\(\s*<?([^)\s>]+)/g)) {
    let dest = m[1].split("#")[0]
    if (!dest || /^[a-z][a-z0-9+.-]*:/i.test(dest) || dest.startsWith("/")) continue
    try {
      dest = decodeURI(dest)
    } catch {
      // As written.
    }
    if (joinPath(dirOf(from), dest) === target) return true
  }
  return false
}

/** The notes that link to file (by [[name]] or a relative link), found with the content search. */
export async function backlinks(file: FileRef, index: NoteIndex): Promise<Backlink[]> {
  const grep = (q: string) =>
    request<{ matches: GrepMatch[] }>(
      `files/grep?root=${encodeURIComponent(index.root)}&path=${encodeURIComponent(index.folder)}&q=${encodeURIComponent(q)}`,
    ).then((r) => r.matches)
  const name = file.path.split("/").pop() ?? file.path
  const found = (await Promise.all([grep(`[[${stem(file.path)}`), grep(name)])).flat()
  const by = new Map<string, Backlink>()
  const seen = new Set<string>()
  for (const m of found) {
    const key = `${m.path}:${m.line}`
    if (m.path === file.path || seen.has(key) || !linksTo(m.text, m.path, file.path, index))
      continue
    seen.add(key)
    const b = by.get(m.path) ?? { path: m.path, lines: [] }
    b.lines.push({ line: m.line, text: m.text.trim().slice(0, 160) })
    by.set(m.path, b)
  }
  return [...by.values()].sort((a, b) => a.path.localeCompare(b.path))
}

/** Backlinks for the note while `on` (the outline is showing); null while looking. */
export function useBacklinks(file: FileRef | undefined, index: NoteIndex | null, on: boolean) {
  const [links, setLinks] = useState<Backlink[] | null>(null)
  const key = file ? keyOf(file) : ""
  // biome-ignore lint/correctness/useExhaustiveDependencies: key is the file, serialized
  useEffect(() => {
    if (!on || !file || !index) return
    let live = true
    setLinks(null)
    backlinks(file, index)
      .then((b) => live && setLinks(b))
      .catch(() => live && setLinks([]))
    return () => {
      live = false
    }
  }, [key, index, on])
  return index && on ? links : undefined
}
