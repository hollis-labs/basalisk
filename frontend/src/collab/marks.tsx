import { Check, MessageSquare, PencilLine, Trash2, Undo2, X } from "lucide-react"
import { useState } from "react"
import type { CollabMark } from "../api/collab"

const ICON = { size: 13, strokeWidth: 1.6, "aria-hidden": true } as const

const when = (iso: string) => {
  const d = new Date(iso)
  return Number.isNaN(d.getTime())
    ? ""
    : d.toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })
}

/** What a mark points at, in a few words. */
export function anchorLabel(m: CollabMark): string {
  const a = m.anchor
  if (a.node) return `node ${a.node}`
  if (a.edge) return `edge ${a.edge}`
  if (a.time !== undefined) {
    const s = Math.floor(a.time)
    return `at ${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`
  }
  if (a.x !== undefined) return a.page ? `page ${a.page}, a region` : "a region"
  if (a.selector) return a.selector
  if (a.line_start)
    return a.line_end && a.line_end !== a.line_start
      ? `lines ${a.line_start}–${a.line_end}`
      : `line ${a.line_start}`
  return "the whole thing"
}

/**
 * The marks on a document: who said what about which part, with the ways to settle each.
 * `found` says whether a mark's passage is still in the text (a text mark whose passage was
 * rewritten is shown as detached). `accept` applies a suggestion; absent when it cannot be.
 */
export function MarksPanel({
  marks,
  active,
  detached,
  onPick,
  onState,
  onRemove,
  onAccept,
}: {
  marks: CollabMark[]
  active?: string
  detached?: (m: CollabMark) => boolean
  onPick: (m: CollabMark) => void
  onState: (m: CollabMark, state: CollabMark["state"]) => void
  onRemove: (m: CollabMark) => void
  onAccept?: (m: CollabMark) => void
}) {
  const [settled, setSettled] = useState(false)
  const open = marks.filter((m) => m.state === "open")
  const rest = marks.filter((m) => m.state !== "open")
  const shown = settled ? [...open, ...rest] : open
  return (
    <aside className="collab-marks">
      <header>
        <strong>
          {open.length} open {open.length === 1 ? "mark" : "marks"}
        </strong>
        {rest.length > 0 && (
          <button type="button" onClick={() => setSettled((s) => !s)}>
            {settled ? "Hide settled" : `${rest.length} settled`}
          </button>
        )}
      </header>
      {!shown.length && (
        <p className="collab-none">
          Nothing marked yet. Select what you mean, then say what about it. The agent sees every
          mark, and can add its own.
        </p>
      )}
      <ul>
        {shown.map((m) => {
          const gone = detached?.(m) ?? false
          return (
            <li key={m.id} data-active={m.id === active} data-state={m.state} data-by={m.by}>
              <button type="button" className="collab-mark-head" onClick={() => onPick(m)}>
                {m.kind === "suggestion" ? <PencilLine {...ICON} /> : <MessageSquare {...ICON} />}
                <span>
                  {m.by === "agent" ? "Agent" : "You"} · {anchorLabel(m)}
                  {gone ? " · passage changed" : ""}
                </span>
                <time>{when(m.at)}</time>
              </button>
              {m.anchor.quote && <p className="collab-quote">{m.anchor.quote.slice(0, 240)}</p>}
              {m.kind === "suggestion" && m.replace !== undefined && (
                <p className="collab-replace">{m.replace.slice(0, 400) || "(remove it)"}</p>
              )}
              {m.text && <p className="collab-note">{m.text}</p>}
              {m.reply && <p className="collab-reply">{m.reply}</p>}
              <div className="collab-mark-tools">
                {m.state === "open" ? (
                  <>
                    {m.kind === "suggestion" && onAccept && !gone && (
                      <button
                        type="button"
                        title="Put the suggestion into the text"
                        onClick={() => onAccept(m)}
                      >
                        <Check {...ICON} /> Accept
                      </button>
                    )}
                    <button type="button" title="Settled" onClick={() => onState(m, "done")}>
                      <Check {...ICON} /> Done
                    </button>
                    <button
                      type="button"
                      title="Not doing this"
                      onClick={() => onState(m, "declined")}
                    >
                      <X {...ICON} /> Decline
                    </button>
                  </>
                ) : (
                  <button type="button" title="Open it again" onClick={() => onState(m, "open")}>
                    <Undo2 {...ICON} /> Reopen ({m.state})
                  </button>
                )}
                <button
                  type="button"
                  title="Remove the mark"
                  aria-label="Remove the mark"
                  onClick={() => onRemove(m)}
                >
                  <Trash2 {...ICON} />
                </button>
              </div>
            </li>
          )
        })}
      </ul>
    </aside>
  )
}

/** The small form that turns a selection into a mark. */
export function MarkComposer({
  what,
  quote,
  canSuggest,
  onAdd,
  onCancel,
}: {
  what: string
  quote?: string
  canSuggest: boolean
  onAdd: (m: { kind: CollabMark["kind"]; text: string; replace?: string }) => void
  onCancel: () => void
}) {
  const [kind, setKind] = useState<CollabMark["kind"]>("comment")
  const [text, setText] = useState("")
  const [replace, setReplace] = useState(quote ?? "")
  const ok = kind === "suggestion" ? replace !== (quote ?? "") : text.trim() !== ""
  const add = () =>
    ok && onAdd({ kind, text: text.trim(), replace: kind === "suggestion" ? replace : undefined })
  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: the form's own keys (Esc, ⌘↩)
    <div
      className="collab-composer"
      onKeyDown={(e) => {
        if (e.key === "Escape") onCancel()
        if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) add()
      }}
    >
      <header>
        <span>{what}</span>
        <div className="collab-kinds">
          {(
            ["comment", "question", ...(canSuggest ? ["suggestion"] : [])] as CollabMark["kind"][]
          ).map((k) => (
            <button key={k} type="button" data-on={kind === k} onClick={() => setKind(k)}>
              {k}
            </button>
          ))}
        </div>
      </header>
      {kind === "suggestion" && (
        <textarea
          className="collab-mono"
          rows={Math.min(8, Math.max(2, replace.split("\n").length))}
          value={replace}
          aria-label="What to put instead"
          onChange={(e) => setReplace(e.target.value)}
        />
      )}
      <textarea
        // biome-ignore lint/a11y/noAutofocus: the form was just asked for
        autoFocus
        rows={2}
        value={text}
        placeholder={
          kind === "suggestion"
            ? "Why (optional)"
            : kind === "question"
              ? "What do you want to know?"
              : "What about it?"
        }
        onChange={(e) => setText(e.target.value)}
      />
      <footer>
        <button type="button" onClick={onCancel}>
          Cancel
        </button>
        <button type="button" className="collab-primary" disabled={!ok} onClick={add}>
          Mark it (⌘↵)
        </button>
      </footer>
    </div>
  )
}
