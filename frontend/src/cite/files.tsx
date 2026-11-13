/**
 * File references in text open with a click: `root:path` where root is a known place
 * (home:Notes/plan.md), and an absolute or ~/ path under a local place's folder. A link is
 * made only when the place exists; whether the file does is asked on click (a folder opens
 * in Files, a file in the app that handles it, and a missing one says so in a small notice).
 */
import { FileText, Folder } from "lucide-react"
import type { MouseEvent } from "react"
import { useSyncExternalStore } from "react"
import { request } from "../api/account"
import { type FileRoot, files } from "../api/files"
import type { DeskAPI, FileRef } from "../desktop/registry"
import "./files.css"

// ---- The places, loaded once and again when they change ----

let roots: FileRoot[] = []
let loading: Promise<void> | null = null
const listeners = new Set<() => void>()
const ROOTS_EVENT = "basalisk:files-roots" // apps/files/places.tsx

function load() {
  loading ??= request<{ enabled: boolean; roots?: FileRoot[] }>("files")
    .then((s) => {
      roots = s.enabled ? (s.roots ?? []) : []
      for (const l of listeners) l()
    })
    .catch(() => undefined)
  return loading
}
if (typeof window !== "undefined")
  window.addEventListener(ROOTS_EVENT, () => {
    loading = null
    void load()
  })

/** The known places (empty until loaded; components re-render when they arrive). */
export function usePlaces(): FileRoot[] {
  return useSyncExternalStore(
    (fn) => {
      listeners.add(fn)
      void load()
      return () => listeners.delete(fn)
    },
    () => roots,
  )
}

// ---- Matching ----

export interface FileMatch {
  start: number
  end: number
  raw: string
  ref: FileRef
}

/** root:path, the root a known place id, the path without spaces. */
const PLACE_REF =
  /(^|[\s([{"'`])([a-z0-9][a-z0-9._-]{0,63}):((?:[^\s:`'"()<>[\]{}]+\/)*[^\s:`'"()<>[\]{}]*[^\s:`'"()<>[\]{}.,;!?])/gi

/** The place references in text (only for places that exist). */
export function placeRefs(text: string, places: FileRoot[]): FileMatch[] {
  if (!places.length || !text.includes(":")) return []
  const ids = new Set(places.map((p) => p.id))
  const out: FileMatch[] = []
  for (const m of text.matchAll(PLACE_REF)) {
    const [, lead, root, path] = m
    if (!ids.has(root) || /^\/\//.test(path)) continue
    const start = (m.index ?? 0) + lead.length
    const raw = `${root}:${path}`
    out.push({
      start,
      end: start + raw.length,
      raw,
      ref: { root, path: path.replace(/^\/+|\/+$/g, "") },
    })
  }
  return out
}

/** An absolute (or ~/) path as a place and a path in it: the local place whose folder holds it. */
export function placeOf(path: string, places: FileRoot[]): FileRef | null {
  const home = places.find((p) => p.id === "home" && p.kind === "local")?.path
  const abs = path.startsWith("~/") && home ? `${home.replace(/\/+$/, "")}/${path.slice(2)}` : path
  if (!abs.startsWith("/")) return null
  let best: FileRoot | null = null
  for (const p of places) {
    if (p.kind !== "local" || !p.path) continue
    const base = p.path.replace(/\/+$/, "")
    if ((abs === base || abs.startsWith(`${base}/`)) && (!best || base.length > best.path.length))
      best = p
  }
  if (!best) return null
  const base = best.path.replace(/\/+$/, "")
  return { root: best.id, path: abs.slice(base.length).replace(/^\/+|\/+$/g, "") }
}

/** The whole string as one file reference (for inline code), or null. */
export function wholeRef(text: string, places: FileRoot[]): FileRef | null {
  const t = text.trim()
  if (!t || /\s/.test(t)) return null
  const m = placeRefs(t, places)
  if (m.length === 1 && m[0].start === 0 && m[0].end === t.length) return m[0].ref
  const plain = t.replace(/:\d{1,6}(?::\d{1,4})?$/, "")
  return /^(~\/|\/)/.test(plain) ? placeOf(plain, places) : null
}

// ---- Opening ----

const desk = () => (globalThis as { basalisk?: { desk?: DeskAPI } }).basalisk?.desk ?? null

function notice(at: HTMLElement, text: string) {
  const r = at.getBoundingClientRect()
  const el = document.createElement("div")
  el.className = "cite-file-notice"
  el.textContent = text
  el.style.left = `${Math.max(8, Math.min(r.left, window.innerWidth - 320))}px`
  el.style.top = `${r.bottom + 6}px`
  document.body.appendChild(el)
  window.setTimeout(() => el.remove(), 2600)
}

/** Opens a file reference: a folder in Files, a file in its app; says so when it is not there. */
export async function openFileRef(ref: FileRef, from: HTMLElement) {
  const d = desk()
  if (!d) return
  try {
    const st = ref.path ? await files.stat(ref.root, ref.path) : null
    if (!st || st.dir) {
      d.open("files", { args: { root: ref.root, path: ref.path } })
      return
    }
    if (!d.openFile(ref)) notice(from, `No app here opens ${st.name}.`)
  } catch {
    notice(from, `${ref.root}:${ref.path} is not there.`)
  }
}

/** A file reference as a link. */
export function FileLink({ fileRef, children }: { fileRef: FileRef; children: React.ReactNode }) {
  const name = fileRef.path.split("/").pop() ?? ""
  const Icon = name && /\.[A-Za-z0-9]{1,8}$/.test(name) ? FileText : Folder
  return (
    <button
      type="button"
      className="cite-file"
      title={`Open ${fileRef.root}:${fileRef.path || "/"}`}
      onClick={(e: MouseEvent<HTMLButtonElement>) => {
        e.preventDefault()
        e.stopPropagation()
        void openFileRef(fileRef, e.currentTarget)
      }}
    >
      <Icon aria-hidden="true" size={12} strokeWidth={1.7} />
      <span>{children}</span>
    </button>
  )
}

/** Text split into plain runs and place references. */
export function splitFiles(text: string, places: FileRoot[]): (string | FileMatch)[] {
  const hits = placeRefs(text, places)
  if (!hits.length) return [text]
  const out: (string | FileMatch)[] = []
  let at = 0
  for (const h of hits) {
    if (h.start > at) out.push(text.slice(at, h.start))
    out.push(h)
    at = h.end
  }
  if (at < text.length) out.push(text.slice(at))
  return out
}

/** Inline code that is one file reference becomes a link; anything else, and fenced code, is left alone. */
export function CodeRef({
  className,
  children,
  ...rest
}: { className?: string; children?: React.ReactNode } & Record<string, unknown>) {
  const places = usePlaces()
  const text = typeof children === "string" ? children : null
  const ref = !className && text && !text.includes("\n") ? wholeRef(text, places) : null
  return (
    <code className={className} {...rest}>
      {ref && text ? <FileLink fileRef={ref}>{text}</FileLink> : children}
    </code>
  )
}
