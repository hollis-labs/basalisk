/**
 * Notes and files: a new note in the notes folder, rename, save as. Through the file API,
 * which never overwrites; a taken name is reported, not replaced.
 */
import { useEffect, useRef, useState } from "react"
import { ApiError } from "../../api/account"
import { type FileRoot, files } from "../../api/files"
import type { FileRef } from "../../desktop/registry"
import { getSettings, updateSettings } from "../../desktop/settings"
import { loadRoots } from "../files/browser"
import { changed } from "../files/ops"

/** The folder new notes go in: the setting, else Notes in Documents (or Home). */
export async function notesFolder(): Promise<FileRef> {
  const set = getSettings().notesFolder
  if (set) return set
  const roots = await loadRoots().catch(() => [] as FileRoot[])
  const pick =
    roots.find((r) => r.id === "documents") ??
    roots.find((r) => r.id === "home") ??
    roots.find((r) => r.kind === "local") ??
    roots[0]
  return { root: pick?.id ?? "home", path: "Notes" }
}

/** "My idea" becomes "My idea.md"; a name with an extension keeps it. */
export const noteName = (name: string) => {
  const n = name.trim().replace(/[/\\]/g, "-")
  return /\.[A-Za-z0-9]{1,8}$/.test(n) ? n : `${n}.md`
}

const join = (folder: string, name: string) => (folder ? `${folder}/${name}` : name)
const parent = (p: string) => (p.includes("/") ? p.slice(0, p.lastIndexOf("/")) : "")

async function ensureFolder(root: string, path: string) {
  if (!path) return
  try {
    await files.mkdir(root, path)
  } catch (e) {
    if (!(e instanceof ApiError && e.status === 409)) throw e
  }
}

/** Makes an empty note; refuses a taken name. */
export async function createNote(folder: FileRef, name: string): Promise<FileRef> {
  await ensureFolder(folder.root, folder.path)
  const path = join(folder.path, noteName(name))
  try {
    await files.create(folder.root, path)
  } catch (e) {
    if (e instanceof ApiError && e.status === 409)
      throw new Error(`"${noteName(name)}" already exists there.`)
    throw e
  }
  changed(folder.root, [folder.path])
  return { root: folder.root, path }
}

export async function renameNote(file: FileRef, name: string): Promise<FileRef> {
  const to = join(parent(file.path), noteName(name))
  try {
    await files.move(file, { root: file.root, path: to })
  } catch (e) {
    if (e instanceof ApiError && e.status === 409)
      throw new Error(`"${noteName(name)}" already exists there.`)
    throw e
  }
  changed(file.root, [parent(file.path)])
  return { root: file.root, path: to }
}

/** Writes text to a new file beside the original (or in folder); refuses a taken name. */
export async function saveAs(
  text: string,
  folder: FileRef,
  name: string,
): Promise<{ ref: FileRef; etag?: string }> {
  const ref = await createNote(folder, name)
  const response = await fetch(
    `/api/files/content?root=${encodeURIComponent(ref.root)}&path=${encodeURIComponent(ref.path)}`,
    {
      method: "PUT",
      credentials: "same-origin",
      headers: {
        "Content-Type": "text/plain; charset=utf-8",
        "X-Requested-With": "XMLHttpRequest",
      },
      body: text,
    },
  )
  if (!response.ok) throw new Error(`The note could not be written (${response.status}).`)
  return { ref, etag: response.headers.get("ETag") ?? undefined }
}

/**
 * The name (and, for a new note, the folder) being asked for. The folder can be remembered as
 * where new notes go.
 */
export function NameForm({
  mode,
  initial,
  folder,
  onDone,
  onCancel,
}: {
  mode: "new" | "rename" | "save-as"
  initial: string
  folder?: FileRef
  onDone: (name: string, folder?: FileRef) => Promise<void>
  onCancel?: () => void
}) {
  const [name, setName] = useState(initial)
  const [roots, setRoots] = useState<FileRoot[]>([])
  const [where, setWhere] = useState<FileRef | undefined>(folder)
  const [remember, setRemember] = useState(false)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const ref = useRef<HTMLInputElement>(null)
  useEffect(() => {
    const el = ref.current
    el?.focus()
    const dot = initial.lastIndexOf(".")
    el?.setSelectionRange(0, dot > 0 ? dot : initial.length)
    if (mode !== "rename")
      void loadRoots()
        .then(setRoots)
        .catch(() => undefined)
  }, [initial, mode])
  useEffect(() => setWhere(folder), [folder])
  const submit = async () => {
    if (!name.trim() || busy) return
    setBusy(true)
    setError("")
    try {
      if (remember && where) updateSettings({ notesFolder: where })
      await onDone(name, where)
    } catch (e) {
      setError(e instanceof Error ? e.message : "That did not work.")
    } finally {
      setBusy(false)
    }
  }
  const label = mode === "new" ? "Create note" : mode === "rename" ? "Rename" : "Save as"
  return (
    <form
      className="notes-form"
      onSubmit={(e) => {
        e.preventDefault()
        void submit()
      }}
      onKeyDown={(e) => {
        if (e.key === "Escape" && onCancel) {
          e.preventDefault()
          e.stopPropagation()
          onCancel()
        }
      }}
    >
      <label>
        <span>Name</span>
        <input
          ref={ref}
          className="desk-input"
          value={name}
          spellCheck={false}
          onChange={(e) => setName(e.target.value)}
        />
      </label>
      {mode !== "rename" && where && (
        <>
          <label>
            <span>In</span>
            <select
              className="desk-input"
              value={where.root}
              onChange={(e) => setWhere({ root: e.target.value, path: where.path })}
            >
              {roots.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.label}
                </option>
              ))}
            </select>
            <input
              className="desk-input"
              value={where.path}
              aria-label="Folder"
              placeholder="folder (blank for the top)"
              spellCheck={false}
              onChange={(e) =>
                setWhere({ root: where.root, path: e.target.value.replace(/^\/+|\/+$/g, "") })
              }
            />
          </label>
          <label className="notes-form-check">
            <input
              type="checkbox"
              checked={remember}
              onChange={(e) => setRemember(e.target.checked)}
            />
            <span>Use this folder for new notes</span>
          </label>
        </>
      )}
      {error && <p className="notes-form-error">{error}</p>}
      <div className="notes-form-actions">
        {onCancel && (
          <button type="button" className="desk-button" onClick={onCancel}>
            Cancel
          </button>
        )}
        <button type="submit" className="desk-button" disabled={busy || !name.trim()}>
          {label}
        </button>
      </div>
    </form>
  )
}
