/**
 * Files' operations, as the window runs them: each one goes to the server (which never
 * overwrites), asks before keeping both on a name clash, and announces what changed.
 */
import { ApiError } from "../../api/account"
import { type FileEntry, type FileRefLike, type FileRoot, files } from "../../api/files"
import type { DeskAPI } from "../../desktop/registry"
import { baseOf, parentOf } from "./model"
import { changed, copyName, freeName, getClip, joinPath, setClip, touched } from "./ops"

const clash = (e: unknown) => e instanceof ApiError && e.status === 409
const message = (e: unknown) => (e instanceof Error ? e.message : "That did not work.")

async function namesIn(root: string, folder: string): Promise<Set<string>> {
  try {
    return new Set((await files.list(root, folder)).entries.map((e) => e.name))
  } catch {
    return new Set()
  }
}

export interface Ctx {
  desk: DeskAPI
  root: FileRoot
  /** The folder the window shows. */
  folder: string
  flash: (msg: string) => void
  /** Select this path once the folder has reloaded. */
  select: (path: string) => void
  /** Give the keyboard back to the list (after a question). */
  refocus?: () => void
}

/** Asks on the desktop, then hands the keyboard back to the list. */
async function ask(c: Pick<Ctx, "desk" | "refocus">, q: Parameters<DeskAPI["confirm"]>[0]) {
  const yes = await c.desk.confirm(q)
  window.setTimeout(() => c.refocus?.(), 0)
  return yes
}

export async function newItem(c: Ctx, kind: "folder" | "file", name: string) {
  const path = joinPath(c.folder, name.trim())
  try {
    const e =
      kind === "folder" ? await files.mkdir(c.root.id, path) : await files.create(c.root.id, path)
    changed(c.root.id, [c.folder])
    c.select(e.path)
  } catch (e) {
    c.flash(clash(e) ? `"${name}" already exists here.` : message(e))
  }
}

export async function rename(c: Ctx, item: FileEntry, name: string) {
  const to = joinPath(parentOf(item.path), name.trim())
  if (to === item.path) return
  try {
    const e = await files.move({ root: c.root.id, path: item.path }, { root: c.root.id, path: to })
    changed(c.root.id, touched(item))
    c.select(e.path)
  } catch (e) {
    c.flash(clash(e) ? `"${name}" already exists here.` : message(e))
  }
}

export async function duplicate(c: Ctx, items: FileEntry[]) {
  const taken = await namesIn(c.root.id, c.folder)
  let last = ""
  for (const item of items) {
    const name = copyName(item.name, taken)
    taken.add(name)
    try {
      last = (
        await files.copy(
          { root: c.root.id, path: item.path },
          { root: c.root.id, path: joinPath(parentOf(item.path), name) },
        )
      ).path
    } catch (e) {
      c.flash(message(e))
      break
    }
  }
  changed(c.root.id, [c.folder])
  if (last) c.select(last)
}

export async function remove(c: Ctx, items: FileEntry[]) {
  if (!items.length) return
  const what = items.length === 1 ? `"${items[0].name}"` : `${items.length} items`
  const local = c.root.kind === "local"
  const ok = await ask(
    c,
    local
      ? {
          title: `Move ${what} to the Trash?`,
          message: "You can put it back from the Trash.",
          confirmLabel: "Move to Trash",
        }
      : {
          title: `Delete ${what} on ${c.root.host} for good?`,
          message: "A remote place has no Trash. This cannot be undone.",
          confirmLabel: "Delete",
          danger: true,
        },
  )
  if (!ok) return
  for (const item of items) {
    try {
      await files.remove(c.root.id, item.path)
    } catch (e) {
      c.flash(message(e))
      break
    }
  }
  changed(c.root.id, [c.folder])
  c.flash(local ? `Moved ${what} to the Trash.` : `Deleted ${what}.`)
}

export function copyItems(root: string, items: FileEntry[], mode: "copy" | "cut") {
  if (!items.length) return
  setClip({ mode, items: items.map((e) => ({ root, path: e.path, name: e.name, dir: e.dir })) })
}

/** Moves or copies refs into a folder of a place, asking before keeping both on a clash. */
export async function transfer(
  c: Pick<Ctx, "desk" | "flash" | "refocus">,
  refs: (FileRefLike & { name: string; dir?: boolean })[],
  dest: FileRefLike,
  mode: "copy" | "move",
): Promise<boolean> {
  let taken: Set<string> | null = null
  let done = 0
  for (const r of refs) {
    if (mode === "move" && r.root === dest.root && parentOf(r.path) === dest.path) continue // already there
    let to = joinPath(dest.path, r.name)
    const from = { root: r.root, path: r.path } // the API takes exactly {root, path}
    const run = () =>
      mode === "copy"
        ? files.copy(from, { root: dest.root, path: to })
        : files.move(from, { root: dest.root, path: to })
    try {
      await run()
    } catch (e) {
      if (!clash(e)) {
        c.flash(message(e))
        return false
      }
      const keep = await ask(c, {
        title: `"${r.name}" already exists there.`,
        message: `Keep both? The ${mode === "copy" ? "copy" : "moved item"} gets a new name; nothing is replaced.`,
        confirmLabel: "Keep both",
      })
      if (!keep) continue
      taken ??= await namesIn(dest.root, dest.path)
      to = joinPath(dest.path, freeName(r.name, taken))
      taken.add(baseOf(to))
      try {
        await run()
      } catch (e2) {
        c.flash(message(e2))
        return false
      }
    }
    done++
    if (mode === "move") changed(r.root, touched(r))
  }
  changed(dest.root, [dest.path])
  if (done) c.flash(`${mode === "copy" ? "Copied" : "Moved"} ${done} item${done === 1 ? "" : "s"}.`)
  return true
}

/** Pastes the file clipboard into the folder (a cut is moved, once). */
export async function paste(c: Ctx) {
  const clip = getClip()
  if (!clip) return
  const ok = await transfer(
    c,
    clip.items,
    { root: c.root.id, path: c.folder },
    clip.mode === "cut" ? "move" : "copy",
  )
  if (ok && clip.mode === "cut") setClip(null)
}
