import { type KeyboardEvent, useEffect, useRef, useState } from "react"
import { ApiError } from "../../api/account"
import { type FragState, frag } from "../../api/frag"
import { CiteText } from "../../cite/ui"
import { useDesk } from "../../desktop/context"
import type { ClipPage } from "./clip"
import { type Moment, moment } from "./context"
import { parseTags } from "./draft"

/** The note, tags and title for one page, kept while the box is away (per address). */
interface ClipDraft {
  url: string
  title: string
  note: string
  tags: string
  draftAt: string
  captureId: string
  idFor: string
  context?: Moment
}
const KEY = "basalisk.frag.clip"

function readClipDraft(page: ClipPage): ClipDraft {
  try {
    const d = JSON.parse(window.localStorage.getItem(KEY) ?? "null") as ClipDraft | null
    if (d && d.url === page.submittedUrl) return d
  } catch {
    // Start fresh.
  }
  return {
    url: page.submittedUrl,
    title: page.title,
    note: "",
    tags: "link",
    draftAt: new Date().toISOString(),
    captureId: "",
    idFor: "",
  }
}
function writeClipDraft(d: ClipDraft | null) {
  try {
    if (d) window.localStorage.setItem(KEY, JSON.stringify(d))
    else window.localStorage.removeItem(KEY)
  } catch {
    // Not kept.
  }
}

/** The id belongs to one clip: the page, the note, the selection and the tags. */
function idFor(d: ClipDraft, page: ClipPage): ClipDraft {
  const key = [
    page.submittedUrl,
    d.note,
    page.selection ?? "",
    parseTags(d.tags).join(","),
    d.title,
  ].join("\u0000")
  if (d.captureId && d.idFor === key) return d
  const rand = Array.from(crypto.getRandomValues(new Uint8Array(12)), (b) =>
    b.toString(16).padStart(2, "0"),
  ).join("")
  return { ...d, captureId: `clip-${rand}`, idFor: key }
}

/** The capture box in clip mode: a page from a Browser window, with a note about it. */
export function ClipBox({ page, close }: { page: ClipPage; close: () => void }) {
  const desk = useDesk()
  const [d, setD] = useState<ClipDraft>(() => readClipDraft(page))
  const [status, setStatus] = useState<{ tone: string; text: string }>({ tone: "idle", text: "" })
  const [state, setState] = useState<FragState | null>(null)
  const noteRef = useRef<HTMLTextAreaElement>(null)
  const saving = useRef(false)

  // biome-ignore lint/correctness/useExhaustiveDependencies: once, when the box opens
  useEffect(() => {
    noteRef.current?.focus()
    void frag
      .state()
      .then(setState)
      .catch(() => undefined)
    if (!d.context) void moment(desk).then((m) => setD((x) => ({ ...x, context: m })))
  }, [])

  const edit = (patch: Partial<ClipDraft>) =>
    setD((x) => {
      const next = { ...x, ...patch }
      writeClipDraft(next)
      return next
    })

  const save = async () => {
    if (saving.current) return
    saving.current = true
    let dd = idFor(d, page)
    setStatus({ tone: "busy", text: "queueing in Basalisk…" })
    const send = (x: ClipDraft) =>
      frag.capture({
        captureId: x.captureId,
        content: "",
        tags: parseTags(x.tags),
        draftAt: x.draftAt,
        submittedAt: new Date().toISOString(),
        context: { draft: x.context, save: {} },
        page: { ...page, title: x.title.trim() || page.title },
        note: x.note,
      })
    try {
      try {
        await send(dd)
      } catch (e) {
        if (!(e instanceof ApiError && e.status === 409 && e.message.includes("different text")))
          throw e
        dd = idFor({ ...dd, captureId: "" }, page)
        await send(dd)
      }
      writeClipDraft(null)
      setStatus({
        tone: "ok",
        text:
          state?.mode !== "exchange" && !state?.integration
            ? "queued in Basalisk · add a Fragments Engine integration in Settings to deliver"
            : "queued in Basalisk · it goes to Fragments Engine from the outbox",
      })
      window.setTimeout(close, 1000)
    } catch (e) {
      writeClipDraft(dd)
      setD(dd)
      setStatus({
        tone: "error",
        text: `${e instanceof Error ? e.message : "Could not queue it."} Your clip is kept.`,
      })
    } finally {
      saving.current = false
    }
  }

  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === "Escape") {
      e.preventDefault()
      e.stopPropagation()
      close()
    } else if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault()
      e.stopPropagation()
      void save()
    }
  }

  const selection = page.selection?.trim() ?? ""
  const sent = selection || page.markdown.trim()
  const words = sent ? sent.split(/\s+/).length : 0
  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: the overlay's own keys (Esc, ⌘↩)
    <div className="frag-layer" onKeyDown={onKey}>
      <button
        type="button"
        className="frag-scrim"
        aria-label="Hide clip (it is kept)"
        tabIndex={-1}
        onClick={close}
      />
      <section
        className="frag frag-clip"
        role="dialog"
        aria-label="Clip this page"
        aria-modal="true"
      >
        <div className="frag-grid" aria-hidden="true" />
        <header className="frag-head">
          <span className="frag-dot" aria-hidden="true" />
          <span className="frag-brand">FRAG</span>
          <span className="frag-sep">{"//"}</span>
          <span className="frag-what">BASALISK</span>
          <span className="frag-sep">·</span>
          <span className="frag-what">CLIP</span>
          <span className="frag-time">{words} words</span>
        </header>
        <label className="frag-field frag-tags frag-title">
          <span className="frag-tags-label">▸ title</span>
          <input
            value={d.title}
            aria-label="Title"
            onChange={(e) => edit({ title: e.target.value })}
          />
        </label>
        <div className="frag-clip-url">
          <CiteText text={page.submittedUrl} />
        </div>
        {/* Exactly what is sent: the selection when there is one, else the whole page. */}
        <div className="frag-clip-what" data-selection={!!selection}>
          sending: {selection ? "selection" : "whole page"}
        </div>
        {selection ? (
          <blockquote className="frag-clip-selection" title="What will be saved">
            {selection}
          </blockquote>
        ) : (
          <pre className="frag-clip-body" title="What will be saved">
            {page.markdown.slice(0, 1800) || "(no article body found; the title and note are kept)"}
          </pre>
        )}
        <div className="frag-field frag-text frag-note">
          <textarea
            ref={noteRef}
            value={d.note}
            aria-label="Note"
            placeholder="A note about this page (optional)…"
            onChange={(e) => edit({ note: e.target.value })}
          />
        </div>
        <label className="frag-field frag-tags">
          <span className="frag-tags-label">▸ tags</span>
          <input
            value={d.tags}
            spellCheck={false}
            aria-label="Tags, separated by commas"
            onChange={(e) => edit({ tags: e.target.value })}
          />
        </label>
        <footer className="frag-foot">
          <span className="frag-status" data-tone={status.tone} role="status">
            {status.text}
          </span>
          <span className="frag-hint">esc hide · ⌘↵ clip</span>
        </footer>
      </section>
    </div>
  )
}
