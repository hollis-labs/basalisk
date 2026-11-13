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

const q = (root: string, path: string) =>
  `root=${encodeURIComponent(root)}&path=${encodeURIComponent(path)}`

export const files = {
  state: () => request<{ enabled: boolean; roots?: FileRoot[]; terminal?: boolean }>("files"),
  roots: () => request<{ roots: FileRoot[] }>("files/roots"),
  list: (root: string, path: string) => request<FileListing>(`files/list?${q(root, path)}`),
  stat: (root: string, path: string) => request<FileEntry>(`files/stat?${q(root, path)}`),
  /** The file's bytes; usable as an <img> src. */
  contentURL: (root: string, path: string, download = false) =>
    `/api/files/content?${q(root, path)}${download ? "&download=1" : ""}`,
  terminal: (root: string, path: string) =>
    request<TerminalSession>("files/terminal", "POST", { root, path }),
}
