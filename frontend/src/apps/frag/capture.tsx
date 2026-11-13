import { type KeyboardEvent, useCallback, useEffect, useRef, useState } from "react"
import { ApiError } from "../../api/account"
import { type FragCapture, type FragState, frag } from "../../api/frag"
import { useDesk } from "../../desktop/context"
import { type Moment, moment } from "./context"
import { captureIdFor, type Draft, newDraft, parseTags, readDraft, writeDraft } from "./draft"

type Status =
  | { tone: "idle"; text: string }
  | { tone: "busy"; text: string }
  | { tone: "ok"; text: string }
  | { tone: "error"; text: string }

const pad = (n: number) => String(n).padStart(2, "0")
const stamp = (d: Date) =>
  `${d.getFullYear()}.${pad(d.getMonth() + 1)}.${pad(d.getDate())} · ${pad(d.getHours())}:${pad(d.getMinutes())}`

/** What the status line says about where a queued capture is. */
function described(c: FragCapture, state: FragState | null): Status {
  if (c.state === "saved")
    return { tone: "ok", text: `saved → ${c.fragmentID?.slice(0, 8) ?? ""}…` }
  if (c.state === "needs_attention")
    return {
      tone: "error",
      text: `needs attention · ${c.code.replaceAll("_", " ")} · see the outbox`,
    }
  if (c.delivering) return { tone: "busy", text: "transmitting…" }
  if (state?.mode !== "exchange" && !state?.integration)
    return {
      tone: "ok",
      text: "queued · add a Fragments Engine integration in Settings to deliver",
    }
  return { tone: "ok", text: "queued" }
}

/** "session linked · <project>" while the draft belongs to a work session. */
function linked(d: Draft): string {
  const s = (d.context as Moment | undefined)?.session
  return s ? `session linked · ${s.project || s.title}` : ""
}

/**
 * The capture box: text and tags, ⌘↩ queues the note on this Basalisk (delivery happens
 * behind it, so nothing is lost offline), Esc hides it and keeps the draft.
 */
export function Capture({ close }: { close: () => void }) {
  const desk = useDesk()
  const [draft, setDraft] = useState<Draft>(readDraft)
  const [status, setStatus] = useState<Status>({ tone: "idle", text: "" })
  const [state, setState] = useState<FragState | null>(null)
  const [now, setNow] = useState(() => new Date())
  const [pulse, setPulse] = useState(0)
  const textRef = useRef<HTMLTextAreaElement>(null)
  const saving = useRef(false)

  // A new draft remembers the moment it began (once; a kept draft keeps its own).
  // biome-ignore lint/correctness/useExhaustiveDependencies: read once, when the box opens
  useEffect(() => {
    if (draft.context) return
    void moment(desk).then((m) =>
      setDraft((d) => {
        if (d.context) return d
        const next = { ...d, context: m }
        if (next.content || next.tags) writeDraft(next)
        return next
      }),
    )
  }, [])

  useEffect(() => {
    textRef.current?.focus()
    const tick = window.setInterval(() => setNow(new Date()), 15_000)
    void frag
      .state()
      .then(setState)
      .catch(() => setState({ mode: "off", integration: null }))
    return () => window.clearInterval(tick)
  }, [])

  // Every keystroke is kept.
  const edit = (patch: Partial<Draft>) => {
    setDraft((d) => {
      const next = {
        ...d,
        ...patch,
        draftAt: d.content || d.tags ? d.draftAt : new Date().toISOString(),
      }
      writeDraft(next)
      return next
    })
    setPulse((p) => p + 1)
    if (status.tone === "error") setStatus({ tone: "idle", text: "" })
  }

  // After a save, follow the capture until it is delivered or the box closes.
  const follow = useCallback(
    (seq: number) => {
      let n = 0
      const timer = window.setInterval(() => {
        if (++n > 20) return window.clearInterval(timer)
        void frag
          .list()
          .then((items) => {
            const c = items.find((i) => i.sequence === seq)
            if (!c) return
            setStatus(described(c, state))
            if (c.state === "saved" || c.state === "needs_attention") window.clearInterval(timer)
          })
          .catch(() => undefined)
      }, 1000)
      return () => window.clearInterval(timer)
    },
    [state],
  )

  const save = async () => {
    if (saving.current) return
    const content = draft.content
    if (!content.trim()) {
      setStatus({ tone: "error", text: "content cannot be empty" })
      return
    }
    saving.current = true
    let d = captureIdFor(draft)
    setStatus({ tone: "busy", text: "queueing…" })
    const atSave = await moment(desk).catch(() => ({}) as Moment)
    const send = (dd: Draft) =>
      frag.capture({
        captureId: dd.captureId,
        content: dd.content,
        tags: parseTags(dd.tags),
        draftAt: dd.draftAt,
        submittedAt: new Date().toISOString(),
        context: { draft: dd.context, save: { window: atSave.window } },
      })
    try {
      let c: FragCapture
      try {
        c = await send(d)
      } catch (e) {
        // The id was used for other text (a save whose answer was lost, then an edit): new id.
        if (!(e instanceof ApiError && e.status === 409 && e.message.includes("different text")))
          throw e
        d = captureIdFor({ ...d, captureId: "" })
        c = await send(d)
      }
      setStatus(described(c, state))
      // Queued is safe: the draft is done. Close a moment later unless the user typed on.
      writeDraft(null)
      setDraft(newDraft())
      follow(c.sequence)
      window.setTimeout(() => {
        const current = readDraft()
        if (!current.content && !current.tags) close()
      }, 1200)
    } catch (e) {
      writeDraft(d)
      setDraft(d)
      setStatus({
        tone: "error",
        text: `${e instanceof Error ? e.message : "Could not queue it."} Your draft is kept.`,
      })
    } finally {
      saving.current = false
    }
  }

  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === "Escape") {
      e.preventDefault()
      e.stopPropagation()
      close() // the draft stays in storage
    } else if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault()
      e.stopPropagation()
      void save()
    }
  }

  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: the overlay's own keys (Esc, ⌘↩)
    <div className="frag-layer" onKeyDown={onKey}>
      <button
        type="button"
        className="frag-scrim"
        aria-label="Hide capture (the draft is kept)"
        tabIndex={-1}
        onClick={close}
      />
      <section className="frag" role="dialog" aria-label="Capture a fragment" aria-modal="true">
        <div className="frag-grid" aria-hidden="true" />
        <header className="frag-head">
          <span className="frag-dot" aria-hidden="true" />
          <span className="frag-brand">FRAG</span>
          <span className="frag-sep">{"//"}</span>
          <span className="frag-what">NEW_FRAGMENT</span>
          <span className="frag-time">{stamp(now)}</span>
        </header>
        <div className="frag-field frag-text" data-pulse={pulse % 2}>
          <textarea
            ref={textRef}
            value={draft.content}
            spellCheck
            aria-label="Fragment"
            placeholder="Write it down…"
            onChange={(e) => edit({ content: e.target.value })}
          />
        </div>
        <label className="frag-field frag-tags">
          <span className="frag-tags-label">▸ tags</span>
          <input
            value={draft.tags}
            spellCheck={false}
            aria-label="Tags, separated by commas"
            placeholder="idea, later"
            onChange={(e) => edit({ tags: e.target.value })}
          />
        </label>
        <footer className="frag-foot">
          <span className="frag-status" data-tone={status.tone} role="status">
            {status.text || linked(draft)}
          </span>
          <span className="frag-hint">esc hide · ⌘↵ commit</span>
        </footer>
      </section>
    </div>
  )
}
