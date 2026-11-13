/** Shared helpers for the Files app: formatting, file kinds, recents, the listing cache. */
import {
  File,
  FileArchive,
  FileAudio,
  FileCode,
  FileImage,
  FileText,
  FileVideo,
  Folder,
  FolderSymlink,
  type LucideIcon,
} from "lucide-react"
import type { FileEntry, FileListing } from "../../api/files"

export interface Loc {
  root: string
  path: string
}

export const extOf = (name: string) => {
  const i = name.lastIndexOf(".")
  return i > 0 ? name.slice(i + 1).toLowerCase() : ""
}

const IMAGE = new Set(["png", "jpg", "jpeg", "gif", "webp", "svg", "avif", "bmp", "ico", "heic"])
const VIDEO = new Set(["mp4", "mov", "webm", "m4v"])
const AUDIO = new Set(["mp3", "wav", "m4a", "ogg", "flac", "aac"])
const ARCHIVE = new Set(["zip", "tar", "gz", "tgz", "bz2", "xz", "7z", "rar", "dmg", "zst"])
const CODE = new Set([
  "go",
  "ts",
  "tsx",
  "js",
  "jsx",
  "mjs",
  "cjs",
  "py",
  "rb",
  "rs",
  "c",
  "h",
  "cc",
  "cpp",
  "hpp",
  "java",
  "kt",
  "swift",
  "m",
  "mm",
  "php",
  "sh",
  "bash",
  "zsh",
  "fish",
  "lua",
  "sql",
  "css",
  "scss",
  "html",
  "htm",
  "xml",
  "json",
  "jsonc",
  "yaml",
  "yml",
  "toml",
  "ini",
  "conf",
  "mod",
  "sum",
  "lock",
  "proto",
  "graphql",
  "vue",
  "svelte",
  "zig",
  "ex",
  "exs",
  "erl",
  "hs",
  "ml",
  "tf",
  "nix",
  "dockerfile",
  "make",
  "mk",
  "env",
  "gradle",
])
const TEXT = new Set(["md", "markdown", "txt", "text", "log", "csv", "tsv", "rst", "adoc", "org"])
const TEXT_NAMES = new Set([
  "makefile",
  "dockerfile",
  "license",
  "readme",
  "changelog",
  "authors",
  "notice",
  "gemfile",
  "procfile",
  "vagrantfile",
  ".gitignore",
  ".gitattributes",
  ".editorconfig",
  ".env",
  ".npmrc",
  ".zshrc",
  ".bashrc",
  ".profile",
  ".vimrc",
  "agents",
  "claude",
])

export type Kind =
  | "dir"
  | "image"
  | "video"
  | "audio"
  | "archive"
  | "code"
  | "text"
  | "pdf"
  | "other"

export function kindOf(e: Pick<FileEntry, "name" | "dir">): Kind {
  if (e.dir) return "dir"
  const ext = extOf(e.name)
  if (IMAGE.has(ext)) return "image"
  if (VIDEO.has(ext)) return "video"
  if (AUDIO.has(ext)) return "audio"
  if (ARCHIVE.has(ext)) return "archive"
  if (ext === "pdf") return "pdf"
  if (CODE.has(ext)) return "code"
  if (TEXT.has(ext)) return "text"
  if (TEXT_NAMES.has(e.name.toLowerCase())) return "text"
  return "other"
}

/** Kinds worth reading as text in the preview (others are sniffed first). */
export const textual = (k: Kind) => k === "code" || k === "text" || k === "other"

export function iconOf(e: FileEntry): LucideIcon {
  if (e.dir) return e.symlink ? FolderSymlink : Folder
  switch (kindOf(e)) {
    case "image":
      return FileImage
    case "video":
      return FileVideo
    case "audio":
      return FileAudio
    case "archive":
      return FileArchive
    case "code":
      return FileCode
    case "text":
    case "pdf":
      return FileText
    default:
      return File
  }
}

export function formatSize(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ["KB", "MB", "GB", "TB"]
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`
}

const pad = (n: number) => String(n).padStart(2, "0")
const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]

export function formatTime(iso: string, now = new Date()): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ""
  const time = `${pad(d.getHours())}:${pad(d.getMinutes())}`
  if (d.toDateString() === now.toDateString()) return `Today ${time}`
  if (d.getFullYear() === now.getFullYear()) return `${MONTHS[d.getMonth()]} ${d.getDate()} ${time}`
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

export const parentOf = (path: string) => {
  const i = path.lastIndexOf("/")
  return i < 0 ? "" : path.slice(0, i)
}
export const baseOf = (path: string) => path.slice(path.lastIndexOf("/") + 1)

/** Where each Files window is, for others to ask (Frag's capture context). */
export const filesLocations = new Map<string, Loc>()

/** Listings already seen, so going back is instant; a fresh copy replaces them. */
export const listingCache = new Map<string, FileListing>()
export const cacheKey = (l: Loc) => `${l.root}\u0000${l.path}`

// ---- Recents: files opened from here, newest first. ----
const RECENT_KEY = "basalisk.files.recent"
const LAST_KEY = "basalisk.files.last"
export interface Recent extends Loc {
  dir: boolean
  at: number
}

function read<T>(key: string, fallback: T): T {
  try {
    const raw = window.localStorage.getItem(key)
    return raw ? (JSON.parse(raw) as T) : fallback
  } catch {
    return fallback
  }
}
function write(key: string, value: unknown) {
  try {
    window.localStorage.setItem(key, JSON.stringify(value))
  } catch {
    /* storage unavailable: recents are a convenience */
  }
}

export const recents = (): Recent[] => read<Recent[]>(RECENT_KEY, [])
export function remember(r: Omit<Recent, "at">) {
  const list = recents().filter((x) => x.root !== r.root || x.path !== r.path)
  write(RECENT_KEY, [{ ...r, at: Date.now() }, ...list].slice(0, 40))
}
export const lastLoc = (): Loc | null => read<Loc | null>(LAST_KEY, null)
export const setLastLoc = (l: Loc) => write(LAST_KEY, l)

export const prefs = {
  get hidden() {
    return read("basalisk.files.hidden", false)
  },
  set hidden(v: boolean) {
    write("basalisk.files.hidden", v)
  },
  get preview() {
    return read("basalisk.files.preview", true)
  },
  set preview(v: boolean) {
    write("basalisk.files.preview", v)
  },
}
