/**
 * What Notes adds to the rendered markdown of a file, without changing the shared renderer:
 * relative links and images resolved through the file API, ids on headings (so #anchors and the
 * outline work), and syntax colour in fenced code. Colour is drawn with the CSS Custom
 * Highlight API, so the DOM React rendered is never rewritten.
 */
import hljs from "highlight.js/lib/core"
import bash from "highlight.js/lib/languages/bash"
import css from "highlight.js/lib/languages/css"
import diff from "highlight.js/lib/languages/diff"
import go from "highlight.js/lib/languages/go"
import ini from "highlight.js/lib/languages/ini"
import javascript from "highlight.js/lib/languages/javascript"
import json from "highlight.js/lib/languages/json"
import markdown from "highlight.js/lib/languages/markdown"
import php from "highlight.js/lib/languages/php"
import python from "highlight.js/lib/languages/python"
import rust from "highlight.js/lib/languages/rust"
import sql from "highlight.js/lib/languages/sql"
import typescript from "highlight.js/lib/languages/typescript"
import xml from "highlight.js/lib/languages/xml"
import yaml from "highlight.js/lib/languages/yaml"
import type { FileRef } from "../../desktop/registry"

for (const [name, lang] of Object.entries({
  bash,
  css,
  diff,
  go,
  ini,
  javascript,
  json,
  markdown,
  php,
  python,
  rust,
  sql,
  typescript,
  xml,
  yaml,
}))
  hljs.registerLanguage(name, lang)
hljs.registerAliases(["sh", "shell", "zsh", "console"], { languageName: "bash" })
hljs.registerAliases(["toml"], { languageName: "ini" })
hljs.registerAliases(["html", "svg"], { languageName: "xml" })
hljs.registerAliases(["tsx", "jsx"], { languageName: "typescript" })

// ---- Relative links and images -------------------------------------------------------------

const IMAGE = /\.(png|jpe?g|gif|webp|svg|avif|bmp|ico)$/i
/** The link prefix Notes reads back: #notes-file:<root>:<path>[#anchor]. */
export const FILE_LINK = "#notes-file:"

const contentURL = (root: string, path: string) =>
  `/api/files/content?root=${encodeURIComponent(root)}&path=${encodeURIComponent(path)}`

/** a/b + ../c.md is a/c.md; null if it climbs above the root. */
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

/** A destination rewritten for the file it is in, or null to leave it alone. */
function resolveDest(dest: string, file: FileRef, image: boolean): string | null {
  const bare = dest.startsWith("<") && dest.endsWith(">") ? dest.slice(1, -1) : dest
  if (!bare || /^[a-z][a-z0-9+.-]*:/i.test(bare) || bare.startsWith("#") || bare.startsWith("/"))
    return null
  const hashAt = bare.indexOf("#")
  const rawPath = hashAt >= 0 ? bare.slice(0, hashAt) : bare
  const hash = hashAt >= 0 ? bare.slice(hashAt + 1) : ""
  let decoded = rawPath
  try {
    decoded = decodeURI(rawPath)
  } catch {
    // Keep it as written.
  }
  const dir = file.path.includes("/") ? file.path.slice(0, file.path.lastIndexOf("/")) : ""
  const path = joinPath(dir, decoded)
  if (path === null) return null
  if (image || (IMAGE.test(path) && !hash)) return `<${contentURL(file.root, path)}>`
  return `<${FILE_LINK}${encodeURIComponent(file.root)}:${encodeURIComponent(path)}${hash ? `#${encodeURIComponent(hash)}` : ""}>`
}

const INLINE = /(!?)\[((?:[^[\]\n]|\[[^\]\n]*\])*)\]\(\s*(<[^>\n]*>|[^)\s]+)/g
const DEFINITION = /^( {0,3}\[[^\]\n]+\]:[ \t]*)(<[^>\n]*>|\S+)/gm

function rewriteProse(text: string, file: FileRef): string {
  // Inline code spans are left as written.
  return text
    .split(/(`[^`\n]*`)/)
    .map((part, i) =>
      i % 2
        ? part
        : part
            .replace(INLINE, (all, bang: string, label: string, dest: string) => {
              const to = resolveDest(dest, file, bang === "!")
              return to ? `${bang}[${label}](${to}` : all
            })
            .replace(DEFINITION, (all, head: string, dest: string) => {
              const to = resolveDest(dest, file, false)
              return to ? `${head}${to}` : all
            }),
    )
    .join("")
}

/** The markdown with relative links and images pointed at the file API; fenced code untouched. */
export function resolveRelative(source: string, file: FileRef | undefined): string {
  if (!file) return source
  const lines = source.split("\n")
  const out: string[] = []
  let prose: string[] = []
  let fence: string | null = null
  const flush = () => {
    if (prose.length) out.push(rewriteProse(prose.join("\n"), file))
    prose = []
  }
  for (const line of lines) {
    const m = /^ {0,3}(`{3,}|~{3,})/.exec(line)
    if (fence === null && m) {
      flush()
      fence = m[1]
      out.push(line)
    } else if (fence !== null) {
      out.push(line)
      if (
        m &&
        m[1][0] === fence[0] &&
        m[1].length >= fence.length &&
        !line.trim().slice(m[1].length).trim()
      )
        fence = null
    } else prose.push(line)
  }
  flush()
  return out.join("\n")
}

/** #notes-file:<root>:<path>#anchor back to a file and an anchor. */
export function parseFileLink(href: string): { file: FileRef; anchor: string } | null {
  if (!href.startsWith(FILE_LINK)) return null
  const rest = href.slice(FILE_LINK.length)
  const colon = rest.indexOf(":")
  if (colon < 0) return null
  const hashAt = rest.indexOf("#", colon)
  const path = hashAt >= 0 ? rest.slice(colon + 1, hashAt) : rest.slice(colon + 1)
  try {
    return {
      file: { root: decodeURIComponent(rest.slice(0, colon)), path: decodeURIComponent(path) },
      anchor: hashAt >= 0 ? decodeURIComponent(rest.slice(hashAt + 1)) : "",
    }
  } catch {
    return null
  }
}

// ---- Headings -------------------------------------------------------------------------------

export interface Heading {
  id: string
  level: number
  text: string
}

/** GitHub's heading slugs: lower case, punctuation dropped, spaces to dashes, repeats numbered. */
export function slug(text: string, seen: Map<string, number>): string {
  const base =
    text
      .trim()
      .toLowerCase()
      .replace(/[^\p{L}\p{N}\s_-]/gu, "")
      .replace(/\s/g, "-") || "section"
  const n = seen.get(base) ?? 0
  seen.set(base, n + 1)
  return n ? `${base}-${n}` : base
}

/** Gives each heading in the page an id (React does not own the attribute) and lists them. */
export function markHeadings(page: HTMLElement): Heading[] {
  const seen = new Map<string, number>()
  return [...page.querySelectorAll<HTMLElement>("h1, h2, h3, h4, h5, h6")].map((h) => {
    const text = h.textContent ?? ""
    const id = slug(text, seen)
    if (h.id !== id) h.id = id
    return { id, level: Number(h.tagName[1]), text }
  })
}

/** Scrolls the page to the heading with this anchor; false if there is none. */
export function scrollToAnchor(page: HTMLElement, anchor: string): boolean {
  let id = anchor
  try {
    id = decodeURIComponent(anchor)
  } catch {
    // As written.
  }
  const el =
    page.querySelector<HTMLElement>(`[id="${CSS.escape(id)}"]`) ??
    page.querySelector<HTMLElement>(`[id="${CSS.escape(slug(id, new Map()))}"]`)
  el?.scrollIntoView({ block: "start" })
  return !!el
}

// ---- Code colour ----------------------------------------------------------------------------

type Registry = { set(name: string, h: unknown): void; delete(name: string): void }
const registry = (): Registry | null =>
  (globalThis as { CSS?: { highlights?: Registry } }).CSS?.highlights ?? null
const HighlightCtor = (globalThis as { Highlight?: new (...r: Range[]) => unknown }).Highlight

/** The scopes drawn; others are left plain. */
const SCOPES = [
  "keyword",
  "built_in",
  "type",
  "literal",
  "number",
  "string",
  "regexp",
  "comment",
  "title",
  "attr",
  "attribute",
  "variable",
  "meta",
  "symbol",
  "section",
  "tag",
  "name",
  "addition",
  "deletion",
] as const
const KNOWN = new Set<string>(SCOPES)

// Every Notes page's ranges, merged into one highlight per scope (the registry is global).
const pages = new Map<HTMLElement, Map<string, Range[]>>()
function publish() {
  const reg = registry()
  if (!reg || !HighlightCtor) return
  for (const scope of SCOPES) {
    const all: Range[] = []
    for (const m of pages.values()) all.push(...(m.get(scope) ?? []))
    if (all.length) reg.set(`hl-${scope}`, new HighlightCtor(...all))
    else reg.delete(`hl-${scope}`)
  }
}

/** The text node and offset at a character offset in el. */
function locate(el: Element, offset: number): [Node, number] | null {
  const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT)
  let at = 0
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const len = n.textContent?.length ?? 0
    if (offset <= at + len) return [n, offset - at]
    at += len
  }
  return null
}

/** Colours the fenced code blocks in page that name a known language. */
export function colourCode(page: HTMLElement) {
  if (!registry() || !HighlightCtor) return
  const byScope = new Map<string, Range[]>()
  for (const code of page.querySelectorAll<HTMLElement>("pre > code[class*='language-']")) {
    const lang = /language-([\w+#-]+)/.exec(code.className)?.[1]?.toLowerCase()
    if (!lang || !hljs.getLanguage(lang)) continue
    const text = code.textContent ?? ""
    if (text.length > 200_000) continue
    let html: string
    try {
      html = hljs.highlight(text, { language: lang, ignoreIllegals: true }).value
    } catch {
      continue
    }
    const tpl = document.createElement("template")
    tpl.innerHTML = html
    let off = 0
    const walk = (node: Node, scope: string | null) => {
      for (const child of node.childNodes) {
        if (child.nodeType === Node.TEXT_NODE) {
          const len = child.textContent?.length ?? 0
          if (scope && len) {
            const a = locate(code, off)
            const b = locate(code, off + len)
            if (a && b) {
              const r = document.createRange()
              r.setStart(a[0], a[1])
              r.setEnd(b[0], b[1])
              const list = byScope.get(scope) ?? []
              list.push(r)
              byScope.set(scope, list)
            }
          }
          off += len
        } else if (child instanceof Element) {
          const cls = /hljs-([\w]+)/.exec(child.className)?.[1]
          walk(child, cls && KNOWN.has(cls) ? cls : scope)
        }
      }
    }
    walk(tpl.content, null)
  }
  pages.set(page, byScope)
  publish()
}

/** Drops a page's colour (when it goes away). */
export function forgetPage(page: HTMLElement) {
  if (pages.delete(page)) publish()
}
