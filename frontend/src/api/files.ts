import { request } from "./account"
import type { TerminalSession } from "./shell"

/** A served folder (GET /api/files/roots). */
export interface FileRoot {
  id: string
  label: string
  kind: "local" | "sftp"
  host?: string
  path: string
}

/** A file or folder. `path` is relative to its root; "" is the root itself. */
export interface FileEntry {
  name: string
  path: string
  dir: boolean
  size: number
  mtime: string
  mode: string
  symlink: boolean
  /** A link that dangles or leads outside its root. */
  broken?: boolean
}

export interface FileListing {
  root: string
  path: string
  entries: FileEntry[]
}

export interface FileRefLike {
  root: string
  path: string
}

const q = (root: string, path: string) =>
  `root=${encodeURIComponent(root)}&path=${encodeURIComponent(path)}`

const q_ = (root: string, path: string) => q(root, path)

export const files = {
  state: () => request<{ enabled: boolean; roots?: FileRoot[]; terminal?: boolean }>("files"),
  roots: () => request<{ roots: FileRoot[] }>("files/roots"),
  list: (root: string, path: string) => request<FileListing>(`files/list?${q(root, path)}`),
  stat: (root: string, path: string) => request<FileEntry>(`files/stat?${q(root, path)}`),
  /** The file's bytes; usable as an <img> src. */
  contentURL: (root: string, path: string, download = false) =>
    `/api/files/content?${q(root, path)}${download ? "&download=1" : ""}`,
  mkdir: (root: string, path: string) => request<FileEntry>("files/mkdir", "POST", { root, path }),
  create: (root: string, path: string) =>
    request<FileEntry>("files/create", "POST", { root, path }),
  move: (from: FileRefLike, to: FileRefLike) =>
    request<FileEntry>("files/move", "POST", { from, to }),
  copy: (from: FileRefLike, to: FileRefLike) =>
    request<FileEntry>("files/copy", "POST", { from, to }),
  /** Local places go to the Trash (trashed true); remote ones are gone for good. */
  remove: (root: string, path: string) =>
    request<{ trashed: boolean }>("files/delete", "POST", { root, path }),
  search: (root: string, path: string, q: string) =>
    request<{ entries: FileEntry[]; truncated: boolean }>(
      `files/search?${q_(root, path)}&q=${encodeURIComponent(q)}`,
    ),
  hosts: () => request<{ hosts: string[] }>("files/hosts").then((r) => r.hosts),
  addRoot: (host: string, path: string, label = "") =>
    request<FileRoot>("files/roots", "POST", { host, path, label }),
  removeRoot: (id: string) => request<void>(`files/roots/${encodeURIComponent(id)}`, "DELETE"),
  terminal: (root: string, path: string) =>
    request<TerminalSession>("files/terminal", "POST", { root, path }),
}
