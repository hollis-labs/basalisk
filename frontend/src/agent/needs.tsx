/**
 * Needs you: the top of the assistant's panel. The owner's new mail is sorted as it arrives
 * (internal/assist): what asks them to decide is here with its options; what only tells is
 * held and counted; what asks for something else is with the assistant. Each card can say
 * where mail like it belongs from now on, which becomes a rule.
 */
import { BellOff, BellRing, ChevronDown, ChevronRight, Inbox } from "lucide-react"
import { useCallback, useEffect, useState } from "react"
import { request } from "../api/account"
import { INBOX_CHANGED, type InboxItem, inbox } from "../api/inbox"
import { useDesk } from "../desktop/context"

interface Sorted {
  id: string
  account: string
  provider: string
  from: string
  from_name?: string
  kind: string
  subject: string
  start: string
  at: string
  class: string
  ask?: InboxItem["ask"]
  why?: string
}

const who = (m: Sorted) => m.from_name || m.from.split("/").pop() || m.from
const asItem = (m: Sorted) => ({ provider: m.provider, id: m.id, account: m.account }) as InboxItem

export function NeedsStrip() {
  const desk = useDesk()
  const [needs, setNeeds] = useState<Sorted[]>([])
  const [held, setHeld] = useState<Sorted[]>([])
  const [withBas, setWithBas] = useState(0)
  const [on, setOn] = useState<boolean | null>(null)
  const [open, setOpen] = useState("")
  const [showHeld, setShowHeld] = useState(false)
  const [note, setNote] = useState("")
  const [said, setSaid] = useState("")

  const load = useCallback(
    () =>
      request<{ needs: Sorted[]; held: Sorted[]; with_assistant: Sorted[]; answers?: Sorted[] }>(
        "assist/needs",
      )
        .then((r) => {
          setNeeds(r.needs ?? [])
          setHeld(r.held ?? [])
          setWithBas((r.with_assistant?.length ?? 0) + (r.answers?.length ?? 0))
          setOn(true)
        })
        .catch(() => setOn((v) => v ?? false)),
    [],
  )
  useEffect(() => {
    void load()
    const timer = window.setInterval(() => void load(), 30_000)
    window.addEventListener(INBOX_CHANGED, load)
    return () => {
      window.clearInterval(timer)
      window.removeEventListener(INBOX_CHANGED, load)
    }
  }, [load])

  const act = async (
    m: Sorted,
    body: { state: string; rule?: { class: string; from?: string; kind?: string } },
    tell = "",
  ) => {
    await request(`assist/mail/${encodeURIComponent(m.id)}`, "POST", body).catch(() => undefined)
    if (tell) setSaid(tell)
    await load()
  }
  const answer = async (m: Sorted, choice: string) => {
    try {
      await inbox.reply(asItem(m), note.trim(), choice)
      await act(m, { state: "done" }, `Answered ${who(m)}.`)
      setNote("")
      setOpen("")
    } catch (e) {
      setSaid(e instanceof Error ? e.message : "It was not sent.")
    }
  }

  if (!on || (!needs.length && !held.length && !withBas))
    return said ? <p className="needs-said">{said}</p> : null
  return (
    <section className="needs" aria-label="Needs you">
      {needs.length > 0 && (
        <h3>
          <BellRing size={13} strokeWidth={1.7} aria-hidden="true" /> Needs you · {needs.length}
        </h3>
      )}
      {needs.map((m) => (
        <div key={m.id} className="needs-card" data-open={open === m.id}>
          <button
            type="button"
            className="needs-head"
            onClick={() => setOpen(open === m.id ? "" : m.id)}
          >
            {open === m.id ? (
              <ChevronDown size={13} aria-hidden="true" />
            ) : (
              <ChevronRight size={13} aria-hidden="true" />
            )}
            <strong>{who(m)}</strong>
            <span>{m.subject}</span>
          </button>
          {open === m.id && (
            <div className="needs-body">
              <p>{m.start}</p>
              {m.ask?.options?.length ? (
                <>
                  <div className="needs-options">
                    {m.ask.options.map((o) => (
                      <button
                        key={o.id}
                        type="button"
                        data-recommended={!!o.recommended}
                        title={o.why}
                        onClick={() => void answer(m, o.id)}
                      >
                        {o.label}
                      </button>
                    ))}
                  </div>
                  <input
                    value={note}
                    placeholder="A note to go with your choice (optional)"
                    onChange={(e) => setNote(e.target.value)}
                  />
                </>
              ) : (
                <form
                  className="needs-reply"
                  onSubmit={(e) => {
                    e.preventDefault()
                    if (note.trim()) void answer(m, "")
                  }}
                >
                  <input
                    value={note}
                    placeholder={`Your answer to ${who(m)}`}
                    onChange={(e) => setNote(e.target.value)}
                  />
                  <button type="submit" disabled={!note.trim()}>
                    Send
                  </button>
                </form>
              )}
              <div className="needs-tools">
                <button type="button" onClick={() => desk.open("inbox")}>
                  <Inbox size={12} aria-hidden="true" /> Open in Inbox
                </button>
                <button type="button" onClick={() => void act(m, { state: "dismissed" })}>
                  Not now
                </button>
                <button
                  type="button"
                  title={`Hold ${m.kind} mail from ${who(m)} from now on; it is counted, not shown`}
                  onClick={() =>
                    void act(
                      m,
                      { state: "open", rule: { class: "digest", from: m.from, kind: m.kind } },
                      `Held from now on: ${m.kind} mail from ${who(m)}.`,
                    )
                  }
                >
                  <BellOff size={12} aria-hidden="true" /> This is noise
                </button>
              </div>
            </div>
          )}
        </div>
      ))}
      {(held.length > 0 || withBas > 0) && (
        <button type="button" className="needs-held" onClick={() => setShowHeld((s) => !s)}>
          {showHeld ? (
            <ChevronDown size={13} aria-hidden="true" />
          ) : (
            <ChevronRight size={13} aria-hidden="true" />
          )}
          {held.length > 0 && <span>{held.length} held for you</span>}
          {withBas > 0 && <span>{withBas} with Bas</span>}
        </button>
      )}
      {showHeld &&
        held.slice(0, 30).map((m) => (
          <div key={m.id} className="needs-row">
            <span title={m.start}>
              <strong>{who(m)}</strong> {m.subject}
            </span>
            <button
              type="button"
              title={`Show me mail from ${who(m)} from now on`}
              onClick={() =>
                void act(
                  m,
                  { state: "open", rule: { class: "needs", from: m.from } },
                  `From now on you are shown mail from ${who(m)}.`,
                )
              }
            >
              Always tell me
            </button>
            <button type="button" onClick={() => void act(m, { state: "dismissed" })}>
              Clear
            </button>
          </div>
        ))}
      {showHeld && held.length > 1 && (
        <button
          type="button"
          className="needs-clear"
          onClick={() =>
            void Promise.all(
              held.map((m) =>
                request(`assist/mail/${encodeURIComponent(m.id)}`, "POST", { state: "dismissed" }),
              ),
            ).then(load)
          }
        >
          Clear all {held.length}
        </button>
      )}
      {said && <p className="needs-said">{said}</p>}
    </section>
  )
}
