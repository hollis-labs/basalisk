/**
 * What Files' operations share across windows: the file clipboard (copy or cut, then paste in
 * any Files window), the notice that a folder changed (every window showing it reloads), and
 * naming helpers.
 */
import type { FileEntry, FileRefLike } from "../../api/files"
import { baseOf, cacheKey, listingCache, parentOf } from "./model"

export interface Clip {
  mode: "copy" | "cut"
  items: (FileRefLike & { name: string; dir: boolean })[]
}

let clipboard: Clip | null = null
export const getClip = () => clipboard
export const setClip = (c: Clip | null) => {
  clipboard = c
  window.dispatchEvent(new Event(CLIP_EVENT))
}
export const CLIP_EVENT = "basalisk:files-clip"

/** A folder's contents changed: windows showing it reload, and its cached listing goes. */
export const CHANGED_EVENT = "basalisk:files-changed"
export function changed(root: string, folders: string[]) {
  for (const f of folders) listingCache.delete(cacheKey({ root, path: f }))
  window.dispatchEvent(new CustomEvent(CHANGED_EVENT, { detail: { root, folders } }))
}

/** The folders an item's change touches: its own folder (and itself, for a folder). */
export const touched = (e: { path: string; dir?: boolean }) =>
  e.dir ? [parentOf(e.path), e.path] : [parentOf(e.path)]

/** "name 2.ext", "name 3.ext"... the first not in taken. */
export function freeName(name: string, taken: Set<string>): string {
  if (!taken.has(name)) return name
  const dot = name.lastIndexOf(".")
  const stem = dot > 0 ? name.slice(0, dot) : name
  const ext = dot > 0 ? name.slice(dot) : ""
  for (let i = 2; i < 1000; i++) {
    const n = `${stem} ${i}${ext}`
    if (!taken.has(n)) return n
  }
  return `${stem} ${Date.now()}${ext}`
}

/** "name copy.ext", then "name copy 2.ext"... */
export function copyName(name: string, taken: Set<string>): string {
  const dot = name.lastIndexOf(".")
  const stem = dot > 0 ? name.slice(0, dot) : name
  const ext = dot > 0 ? name.slice(dot) : ""
  return freeName(`${stem} copy${ext}`, taken)
}

export const joinPath = (folder: string, name: string) => (folder ? `${folder}/${name}` : name)

/** Drag and drop between Files windows and places carries the items as this type. */
export const DRAG_TYPE = "application/x-basalisk-files"
export const dragItems = (items: FileEntry[], root: string) =>
  JSON.stringify(items.map((e) => ({ root, path: e.path, name: baseOf(e.path), dir: e.dir })))
