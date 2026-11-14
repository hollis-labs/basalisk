import { Send } from "lucide-react"
import { type KeyboardEvent, useEffect, useRef, useState } from "react"
import { type InboxAccount, type InboxRecipient, inbox } from "../../api/inbox"
import type { MessageDraft } from "../../desktop/message"

interface Kept extends Required<MessageDraft> {
  /** The recipient as typed or picked: a name is shown, the address is what is sent. */
  toName: string
  from: string
}

const blank: Kept = { to: "", toName: "", subject: "", body: "", kind: "notice", from: "" }
// The draft outlives the box: Esc hides it and the text is still there next time.
let kept: Kept = blank
let incoming: MessageDraft | null = null

/** Hand the box what to open with (index.tsx, on MESSAGE_EVENT). */
export function setIncoming(d: MessageDraft) {
  incoming = d
}

function start(): Kept {
  if (!incoming) return kept
  const d = incoming
  incoming = null
  // What was asked for replaces the same fields of a kept draft; the rest stays.
  kept = {
    ...kept,
    ...(d.to !== undefined ? { to: d.to, toName: "" } : {}),
    ...(d.subject !== undefined ? { subject: d.subject } : {}),
    ...(d.body !== undefined ? { body: d.body } : {}),
    ...(d.kind ? { kind: d.kind } : {}),
  }
  return kept
}

const short = (address: string) => address.split("/").pop() || address

/**
 * The compose box: who from (one of the owner's addresses), who to (a name from the
 * registry, or an address), a subject and the text. ⌘↩ sends; Esc hides and keeps the draft.
 */
export function Compose({ close }: { close: () => void }) {
  const [draft, setDraftState] = useState<Kept>(start)
  const [accounts, setAccounts] = useState<InboxAccount[]>([])
  const [found, setFound] = useState<InboxRecipient[]>([])
  const [pick, setPick] = useState(0)
  const [listOpen, setListOpen] = useState(false)
  const [status, setStatus] = useState<{ tone: "idle" | "busy" | "ok" | "error"; text: string }>({
    tone: "idle",
    text: "",
  })
  const toRef = useRef<HTMLInputElement>(null)
  const subjectRef = useRef<HTMLInputElement>(null)
  const bodyRef = useRef<HTMLTextAreaElement>(null)
  const sending = useRef(false)

  const set = (patch: Partial<Kept>) =>
    setDraftState((d) => {
      kept = { ...d, ...patch }
      return kept
    })

  // biome-ignore lint/correctness/useExhaustiveDependencies: once, when the box opens
  useEffect(() => {
    void inbox
      .recipients(draft.to && !draft.toName ? draft.to : "")
      .then((r) => {
        setAccounts(r.accounts)
        if (!kept.from && r.accounts[0]) set({ from: r.accounts[0].address })
        // An address that was clicked: show whose it is.
        const who = r.recipients.find((p) => p.address === kept.to)
        if (who?.name) set({ toName: who.name })
      })
      .catch((e) =>
        setStatus({
          tone: "error",
          text: e instanceof Error ? e.message : "Messages cannot be sent from this Basalisk.",
        }),
      )
    if (!draft.to) toRef.current?.focus()
    else if (!draft.subject) subjectRef.current?.focus()
    else bodyRef.current?.focus()
  }, [])

  // Look people up as the recipient is typed.
  const typed = draft.toName || draft.to
  useEffect(() => {
    if (!listOpen) return
    let live = true
    const timer = window.setTimeout(() => {
      void inbox
        .recipients(typed)
        .then((r) => {
          if (!live) return
          setFound(r.recipients.slice(0, 8))
          setPick(0)
        })
        .catch(() => undefined)
    }, 120)
    return () => {
      live = false
      window.clearTimeout(timer)
    }
  }, [typed, listOpen])

  const choose = (p: InboxRecipient) => {
    set({ to: p.address, toName: p.name || "" })
    setListOpen(false)
    subjectRef.current?.focus()
  }

  const send = async () => {
    if (sending.current) return
    const from = accounts.find((a) => a.address === draft.from) ?? accounts[0]
    if (!from) return setStatus({ tone: "error", text: "There is no address to send from." })
    if (!draft.to.startsWith("msg://"))
      return setStatus({
        tone: "error",
        text: "Pick who it goes to, or paste an address (msg://…).",
      })
    if (!(draft.subject + draft.body).trim())
      return setStatus({ tone: "error", text: "There is nothing to send yet." })
    sending.current = true
    setStatus({ tone: "busy", text: "sending…" })
    try {
      await inbox.send(from, {
        to: draft.to,
        kind: draft.kind,
        subject: draft.subject.trim(),
        body: draft.body,
      })
      setStatus({ tone: "ok", text: `sent to ${draft.toName || short(draft.to)}` })
      kept = { ...blank, from: draft.from }
      window.setTimeout(close, 700)
    } catch (e) {
      setStatus({
        tone: "error",
        text: `${e instanceof Error ? e.message : "It was not sent."} Your draft is kept.`,
      })
    } finally {
      sending.current = false
    }
  }

  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === "Escape") {
      e.preventDefault()
      e.stopPropagation()
      if (listOpen) setListOpen(false)
      else close()
    } else if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault()
      e.stopPropagation()
      void send()
    }
  }
  const onToKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (!listOpen || !found.length) return
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault()
      setPick((p) => (p + (e.key === "ArrowDown" ? 1 : -1) + found.length) % found.length)
    } else if (e.key === "Enter" || e.key === "Tab") {
      e.preventDefault()
      choose(found[pick])
    }
  }

  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: the overlay's own keys (Esc, ⌘↩)
    <div className="frag-layer" onKeyDown={onKey}>
      <button
        type="button"
        className="frag-scrim"
        aria-label="Hide the message (the draft is kept)"
        tabIndex={-1}
        onClick={close}
      />
      <section className="frag msg" role="dialog" aria-label="New message" aria-modal="true">
        <header className="frag-head">
          <span className="frag-dot" aria-hidden="true" />
          <span className="frag-brand">MESSAGE</span>
          <span className="frag-sep">{"//"}</span>
          <span className="frag-what">NEW</span>
          <span className="msg-spacer" />
          <select
            className="msg-select"
            aria-label="Kind of message"
            value={draft.kind}
            onChange={(e) => set({ kind: e.target.value })}
          >
            <option value="notice">notice</option>
            <option value="request">request</option>
            <option value="handoff">handoff</option>
          </select>
        </header>
        <div className="msg-row">
          <span className="msg-label">to</span>
          <div className="msg-to">
            <input
              ref={toRef}
              value={draft.toName || draft.to}
              spellCheck={false}
              placeholder="A name, a role, or an address (msg://…)"
              aria-label="To"
              onFocus={() => setListOpen(true)}
              onBlur={() => window.setTimeout(() => setListOpen(false), 150)}
              onChange={(e) => {
                const v = e.target.value
                set(v.startsWith("msg://") ? { to: v.trim(), toName: "" } : { to: "", toName: v })
                setListOpen(true)
              }}
              onKeyDown={onToKey}
            />
            {draft.to && draft.toName && <span className="msg-addr">{draft.to}</span>}
            {listOpen && found.length > 0 && (
              <ul className="msg-found">
                {found.map((p, i) => (
                  <li key={p.address}>
                    <button
                      type="button"
                      data-on={i === pick}
                      onMouseDown={(e) => {
                        e.preventDefault()
                        choose(p)
                      }}
                    >
                      <strong>{p.name || short(p.address)}</strong>
                      {p.role && <em>{p.role}</em>}
                      <span>{p.about || p.address}</span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
        <div className="msg-row">
          <span className="msg-label">subject</span>
          <input
            ref={subjectRef}
            value={draft.subject}
            aria-label="Subject"
            onChange={(e) => set({ subject: e.target.value })}
          />
        </div>
        <textarea
          ref={bodyRef}
          className="msg-body"
          rows={9}
          value={draft.body}
          aria-label="Message"
          placeholder="Write the message…"
          onChange={(e) => set({ body: e.target.value })}
        />
        <footer className="frag-foot">
          <span className="frag-status" data-tone={status.tone} role="status">
            {status.text}
          </span>
          {accounts.length > 1 ? (
            <select
              className="msg-select"
              aria-label="Send from"
              title="The address it is sent from"
              value={draft.from}
              onChange={(e) => set({ from: e.target.value })}
            >
              {accounts.map((a) => (
                <option key={a.address} value={a.address}>
                  from {short(a.address)}
                </option>
              ))}
            </select>
          ) : (
            accounts[0] && <span className="frag-hint">from {short(accounts[0].address)}</span>
          )}
          <span className="frag-hint">esc hide · ⌘↵</span>
          <button type="button" className="msg-send" onClick={() => void send()}>
            <Send aria-hidden="true" size={13} strokeWidth={1.6} />
            <span>Send</span>
          </button>
        </footer>
      </section>
    </div>
  )
}
