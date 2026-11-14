/**
 * Clip: extract the page in a Browser window with fe-clipper's extraction
 * (/clip/extract.js, built by scripts/build-clip-bundle.sh), run inside the page through the
 * view's own eval. This is the window acting for the person, not an agent: it goes over the
 * page channel, not /api/browser, and is not written to the shell log.
 */
import { nativeBrowser } from "../../api/browser"
import { asScript, cleanupPage, keepSelection, preparePage } from "./prepare"

/** What the server takes for a clipped page (internal/api/frag_clip.go). */
export interface ClipPage {
  title: string
  description?: string
  language?: string
  submittedUrl: string
  canonicalUrl?: string
  provider?: string
  providerItemId?: string
  segmentKey?: string
  adapter?: string
  adapterVersion?: string
  markdown: string
  selection?: string
  capabilities?: string[]
  warnings?: { code: string; message: string }[]
  /** fe-clipper's media candidates and provider extensions, as found (internal/api/frag_media.go). */
  media?: ClipMedia[]
  extensions?: ClipExtension[]
}

/** fe-clipper's AdapterMediaCandidate. */
export interface ClipMedia {
  candidateId: string
  providerMediaId?: string
  kind: string
  role: string
  position: number
  altText?: string
  caption?: string
  sourceContext?: string
  variants: {
    variantId: string
    kind: string
    sourceUrl: string
    acquisitionPreference?: string
    mimeType?: string
    width?: number
    height?: number
    durationSeconds?: number
    byteSize?: number
    expiresAt?: string
  }[]
}

/** fe-clipper's AdapterProviderMetadataExtension. */
export interface ClipExtension {
  namespace: string
  schemaVersion: string
  placement?: string
  data: Record<string, unknown>
}

/** fe-clipper's PageCaptureResult, as much of it as a clip uses. */
interface Captured {
  adapter?: { id?: string; version?: string }
  identity?: {
    submittedUrl?: string
    canonicalUrl?: string
    provider?: string
    providerItemId?: string
    segmentKey?: string
  }
  document?: {
    title?: string
    description?: string
    contentMarkdown?: string
    selection?: string
    language?: string
  }
  capabilities?: { observed?: string[] }
  warnings?: { code?: string; message?: string }[]
  media?: ClipMedia[]
  extensions?: ClipExtension[]
}

let bundle: Promise<string> | null = null
const loadBundle = () => {
  bundle ??= fetch("/clip/extract.js", { credentials: "same-origin" }).then((r) => {
    if (!r.ok) throw new Error("The clipper is missing from this build.")
    return r.text()
  })
  bundle.catch(() => {
    bundle = null
  })
  return bundle
}

const READ = `(()=>{const c=window.__FE_CLIPPER_CAPTURE__;return c?JSON.stringify(c):null})()`

const PREP_DONE = `(()=>{const p=window.__BASALISK_CLIP_PREP__;return !p||p.done===true})()`
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

/** Extracts the page shown in a Browser window. */
export async function clipPage(winId: string): Promise<ClipPage> {
  const send = (msg: Record<string, unknown>) => nativeBrowser.send({ id: winId, ...msg })
  const script = await loadBundle()
  try {
    // Get the page ready (prepare.ts); whatever it does or fails to do, the clip goes on.
    try {
      await send({ op: "eval", js: asScript(preparePage) })
      // Up to 12 s: a YouTube page may refresh its state (4 s) and load its transcript (5 s).
      for (let i = 0; i < 60 && (await send({ op: "eval", js: PREP_DONE })) !== true; i++)
        await sleep(200)
    } catch {
      // Clip the page as it is.
    }
    await send({ op: "eval", js: `${script}\n;0` })
    // The adapters may walk the page first (X loads replies near the viewport), so poll.
    for (let i = 0; i < 50; i++) {
      const raw = await send({ op: "eval", js: READ })
      if (typeof raw === "string") return toPage(JSON.parse(raw) as Captured)
      await sleep(200)
    }
    throw new Error("The page took too long to extract.")
  } finally {
    await send({ op: "eval", js: asScript(cleanupPage) }).catch(() => undefined)
  }
}

function toPage(c: Captured): ClipPage {
  const url = c.identity?.submittedUrl ?? ""
  if (!/^https?:/i.test(url)) throw new Error("Only web pages can be clipped.")
  return {
    title: c.document?.title ?? "",
    description: c.document?.description,
    language: c.document?.language,
    submittedUrl: url,
    canonicalUrl: c.identity?.canonicalUrl,
    provider: c.identity?.provider,
    providerItemId: c.identity?.providerItemId,
    segmentKey: c.identity?.segmentKey,
    adapter: c.adapter?.id,
    adapterVersion: c.adapter?.version,
    markdown: c.document?.contentMarkdown ?? "",
    selection: c.document?.selection,
    capabilities: c.capabilities?.observed ?? [],
    warnings: (c.warnings ?? [])
      .filter((w) => w.message)
      .map((w) => ({ code: w.code ?? "", message: w.message ?? "" })),
    // Passed on as found; the server checks and maps them as fe-clipper does. YouTube's
    // caption files are left out: they answer empty to any fetch without the player's own
    // token, so each would only be a file not sent. The transcript text comes from the page
    // (prepare.ts) in the provider.youtube extension.
    media: (c.media ?? [])
      .map((m) => ({ ...m, variants: m.variants.filter((v) => !youtubeCaptionFile(v)) }))
      .filter((m) => m.variants.length > 0)
      .slice(0, 64),
    extensions: (c.extensions ?? []).slice(0, 16),
  }
}

function youtubeCaptionFile(v: ClipMedia["variants"][number]): boolean {
  if (v.kind !== "subtitles") return false
  try {
    const u = new URL(v.sourceUrl)
    return /(^|\.)youtube\.com$/i.test(u.hostname) && u.pathname === "/api/timedtext"
  } catch {
    return false
  }
}

/** What goes with the text, in words ("3 images, 1 video"), or "". */
export function mediaSummary(media: ClipMedia[] | undefined): string {
  const n = new Map<string, number>()
  for (const m of media ?? []) n.set(m.kind, (n.get(m.kind) ?? 0) + 1)
  const word: Record<string, [string, string]> = {
    image: ["image", "images"],
    video: ["video", "videos"],
    audio: ["audio file", "audio files"],
    document: ["document", "documents"],
    timed_text: ["transcript", "transcripts"],
  }
  return [...n.entries()]
    .map(([k, c]) => {
      const [one, many] = word[k] ?? ["file", "files"]
      return `${c} ${c === 1 ? one : many}`
    })
    .join(", ")
}

// ---- Keeping a page's selection for the clip button (prepare.ts keepSelection) ----

// Installed in each Browser page when it finishes loading (once per document: a page that
// starts loading again gets it again).
const keeping = new Map<string, string>()
if (typeof window !== "undefined")
  window.addEventListener("basalisk:browser", (e) => {
    const ev = (e as CustomEvent<{ kind?: string; id?: string; url?: string; loading?: boolean }>)
      .detail
    if (ev?.kind !== "state" || !ev.id) return
    if (ev.loading) {
      keeping.delete(ev.id)
      return
    }
    if (!/^https?:/i.test(ev.url ?? "") || keeping.get(ev.id) === ev.url) return
    keeping.set(ev.id, ev.url ?? "")
    void nativeBrowser
      .send({ id: ev.id, op: "eval", js: asScript(keepSelection) })
      .catch(() => undefined)
  })

// ---- Handing a clip to the capture box ----

let pending: ClipPage | null = null
/** The clip the box opens with next (set just before the overlay opens). */
export const setPendingClip = (p: ClipPage) => {
  pending = p
}
export const takePendingClip = () => {
  const p = pending
  pending = null
  return p
}

/** Asks the focused Browser window to clip its page (the frag.clip command). */
export const CLIP_EVENT = "basalisk:frag-clip"
