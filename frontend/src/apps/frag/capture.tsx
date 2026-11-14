import { type KeyboardEvent, useCallback, useEffect, useRef, useState } from "react"
import { ApiError } from "../../api/account"
import { type FragCapture, type FragState, frag } from "../../api/frag"
import { SCRATCH_CHANGED, scratch } from "../../api/scratch"
import { fragmentRef } from "../../cite/fragment"
import { CiteText } from "../../cite/ui"
import { useDesk } from "../../desktop/context"
import type { DeskAPI } from "../../desktop/registry"
import { type Moment, moment } from "./context"
import { captureIdFor, type Draft, newDraft, parseTags, readDraft, writeDraft } from "./draft"
import { NoteField, PreviewToggle, tagsFieldProps, usePreview } from "./notefield"

type Status = {
  tone: "idle" | "busy" | "ok" | "error"
  text: string
  /** A saved fragment, shown as its reference chip. */
  fragment?: string
}

const pad = (n: number) => String(n).padStart(2, "0")
const stamp = (d: Date) =>
  `${d.getFullYear()}.${pad(d.getMonth() + 1)}.${pad(d.getDate())} · ${pad(d.getHours())}:${pad(d.getMinutes())}`

/** What the status line says about where a queued capture is. */
function described(c: FragCapture, state: FragState | null): Status {
  if (c.state === "saved")
    return { tone: "ok", text: "saved to Fragments Engine →", fragment: c.fragmentID }
  if (c.state === "needs_attention")
    return {
      tone: "error",
      text: `needs attention · ${c.code.replaceAll("_", " ")} · see the outbox`,
    }
  if (c.delivering)
    return { tone: "busy", text: "queued in Basalisk · sending to Fragments Engine…" }
  if (state?.mode !== "exchange" && !state?.integration)
    return {
      tone: "ok",
      text: "queued in Basalisk · add a Fragments Engine integration in Settings to deliver",
    }
  return { tone: "ok", text: "queued in Basalisk" }
}

/** "session linked · <project>" while the draft belongs to a work session. */
function linked(d: Draft): string {
  const s = (d.context as Moment | undefined)?.session
  return s ? `session linked · ${s.project || s.title}` : ""
}

/** Where the box sends what is written: Fragments Engine, or the owner's scratch. */
export type Dest = "frag" | "scratch"
const DEST_KEY = "basalisk.frag.dest"
let pendingDest: Dest | null = null

function readDest(): Dest {
  if (pendingDest) {
    const d = pendingDest
    pendingDest = null
    return d
  }
  try {
    return window.localStorage.getItem(DEST_KEY) === "scratch" ? "scratch" : "frag"
  } catch {
    return "frag"
  }
}

/** Open the capture box set to one destination (the Scratch shortcut). */
export function openCaptureFor(desk: DeskAPI, dest: Dest) {
  pendingDest = dest
  if (desk.overlayOpen("frag")) desk.toggleOverlay("frag", false)
  desk.toggleOverlay("frag", true)
}

/**
 * The capture box: text and tags, ⌘↩ queues the note on this Basalisk (delivery happens
 * behind it, so nothing is lost offline), Esc hides it and keeps the draft. It writes to
 * Fragments Engine or, switched over (⌘⇧D), to the owner's scratch.
 */
export function Capture({ close }: { close: () => void }) {
  const desk = useDesk()
  const [dest, setDestState] = useState<Dest>(readDest)
  const setDest = (d: Dest) => {
    setDestState(d)
    try {
      window.localStorage.setItem(DEST_KEY, d)
    } catch {
      // Not kept.
    }
  }
  const [draft, setDraft] = useState<Draft>(readDraft)
  const [status, setStatus] = useState<Status>({ tone: "idle", text: "" })
  const [state, setState] = useState<FragState | null>(null)
  const [now, setNow] = useState(() => new Date())
  const [pulse, setPulse] = useState(0)
  const textRef = useRef<HTMLTextAreaElement>(null)
  const saving = useRef(false)
  const [preview, togglePreview] = usePreview()

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
    if (dest === "scratch") {
      // Scratch: kept as small items at once; tags tie every item written here.
      try {
        const tags = parseTags(draft.tags)
        const text = tags.length ? `::config tag=${tags.join(" ")}\n${content}` : content
        const { items } = await scratch.add(text, draft.context)
        window.dispatchEvent(new Event(SCRATCH_CHANGED))
        setStatus({
          tone: "ok",
          text: `kept in scratch · ${items.length} ${items.length === 1 ? "item" : "items"}`,
        })
        writeDraft(null)
        setDraft(newDraft())
        window.setTimeout(() => {
          const current = readDraft()
          if (!current.content && !current.tags) close()
        }, 700)
      } catch (e) {
        setStatus({
          tone: "error",
          text: `${e instanceof Error ? e.message : "It could not be kept."} Your draft is kept.`,
        })
      } finally {
        saving.current = false
      }
      return
    }
    let d = captureIdFor(draft)
    setStatus({ tone: "busy", text: "queueing in Basalisk…" })
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
    } else if (e.metaKey && e.shiftKey && e.code === "KeyP") {
      e.preventDefault()
      e.stopPropagation()
      togglePreview()
    } else if (e.metaKey && e.shiftKey && e.code === "KeyD") {
      e.preventDefault()
      e.stopPropagation()
      setDest(dest === "scratch" ? "frag" : "scratch")
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
          <span className="frag-what">BASALISK</span>
          <span className="frag-sep">·</span>
          <span className="frag-dest">
            <button
              type="button"
              className="frag-dest-opt"
              data-on={dest === "frag"}
              title="Send to Fragments Engine (⌘⇧D switches)"
              onClick={() => setDest("frag")}
            >
              NEW_FRAGMENT
            </button>
            <button
              type="button"
              className="frag-dest-opt"
              data-on={dest === "scratch"}
              title="Keep in scratch (⌘⇧D switches)"
              onClick={() => setDest("scratch")}
            >
              SCRATCH
            </button>
          </span>
          <span className="frag-time">{stamp(now)}</span>
        </header>
        <NoteField
          value={draft.content}
          onChange={(v) => edit({ content: v })}
          textRef={textRef}
          label={dest === "scratch" ? "Scratch" : "Fragment"}
          placeholder={
            dest === "scratch"
              ? "Jot it down…  ::idea  ::follow-up  $ command  [] todo  +project @agent #tag"
              : "Write it down…"
          }
          preview={preview}
          className=""
          pulse={pulse % 2}
        />
        <label className="frag-field frag-tags">
          <span className="frag-tags-label">▸ tags</span>
          <input
            value={draft.tags}
            spellCheck={false}
            aria-label="Tags, separated by commas"
            placeholder="idea, later"
            onChange={(e) => edit({ tags: e.target.value })}
            {...tagsFieldProps(draft.tags, (tags) => edit({ tags }))}
          />
        </label>
        <footer className="frag-foot">
          <span className="frag-status" data-tone={status.tone} role="status">
            {status.text || linked(draft)}
            {status.fragment && (
              <>
                {" "}
                <CiteText text={fragmentRef(status.fragment)} />
              </>
            )}
          </span>
          <PreviewToggle on={preview} toggle={togglePreview} />
          <span className="frag-hint">
            esc hide · ⌘⇧D {dest === "scratch" ? "fragment" : "scratch"} · ⌘↵{" "}
            {dest === "scratch" ? "keep" : "commit"}
          </span>
        </footer>
      </section>
    </div>
  )
}
