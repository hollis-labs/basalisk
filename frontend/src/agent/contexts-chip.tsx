import { useCallback, useEffect, useRef, useState } from "react"
import { request } from "../api/account"

interface Slot {
  name: string
  about: string
  note_written?: string
  current: boolean
}

/**
 * The context the assistant is in, in the panel's header: which kind of work it takes the
 * owner to be doing. It follows the window in front; the menu moves it by hand and shows
 * the assistant's own note for each context.
 */
export function ContextChip({ busy }: { busy: boolean }) {
  const [slots, setSlots] = useState<Slot[]>([])
  const [open, setOpen] = useState(false)
  const [note, setNote] = useState<{ name: string; text: string; written: string } | null>(null)
  const root = useRef<HTMLSpanElement>(null)

  const load = useCallback(
    () =>
      request<{ contexts: Slot[] }>("agent/contexts")
        .then((r) => setSlots(r.contexts ?? []))
        .catch(() => undefined),
    [],
  )
  // It can change with any message (the window in front, or the assistant's own move).
  // biome-ignore lint/correctness/useExhaustiveDependencies: reload when a run starts or ends
  useEffect(() => {
    void load()
  }, [load, busy])
  useEffect(() => {
    if (!open) return
    void load()
    const onDown = (e: PointerEvent) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener("pointerdown", onDown, true)
    return () => document.removeEventListener("pointerdown", onDown, true)
  }, [open, load])

  const current = slots.find((s) => s.current)
  if (!current) return null
  const pick = async (name: string) => {
    const r = await request<{ contexts: Slot[] }>("agent/contexts", "POST", { name }).catch(
      () => null,
    )
    if (r) setSlots(r.contexts)
    setOpen(false)
  }
  const read = async (name: string) => {
    const r = await request<{ note?: string; written?: string }>(
      `agent/contexts?note=${name}`,
    ).catch(() => null)
    setNote({ name, text: r?.note || "", written: r?.written || "" })
  }
  return (
    <span className="agent-context" ref={root}>
      <button
        type="button"
        className="agent-context-chip"
        title={`The assistant takes you to be in your ${current.name} context: ${current.about}. It follows the window in front; click to change it or read its notes.`}
        onClick={() => {
          setOpen((o) => !o)
          setNote(null)
        }}
      >
        {current.name}
      </button>
      {open && (
        <span className="agent-context-menu" role="menu">
          {slots.map((s) => (
            <span key={s.name} className="agent-context-row" data-on={s.current}>
              <button
                type="button"
                role="menuitem"
                title={s.about}
                onClick={() => void pick(s.name)}
              >
                {s.name}
              </button>
              <button
                type="button"
                className="agent-context-note"
                disabled={!s.note_written}
                title={s.note_written ? `Its note, written ${s.note_written}` : "No note yet"}
                onClick={() => void read(s.name)}
              >
                note
              </button>
            </span>
          ))}
          {note && (
            <span className="agent-context-text">
              <strong>
                {note.name} · {note.written}
              </strong>
              {note.text}
            </span>
          )}
        </span>
      )}
    </span>
  )
}
