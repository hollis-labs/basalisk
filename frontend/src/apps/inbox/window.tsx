import { Archive, ArchiveRestore, Forward, MailOpen, MailPlus, RefreshCw, Send } from "lucide-react"
import { type KeyboardEvent, useCallback, useEffect, useMemo, useRef, useState } from "react"
import { INBOX_CHANGED, type InboxItem, type InboxSource, inbox } from "../../api/inbox"
import { Markdown } from "../../cite/ui"
import { composeMessage } from "../../desktop/message"
import "./inbox.css"

type View = "unread" | "all" | "archived"

const ASK: Record<string, string> = {
  question: "question",
  decision: "decision",
  approval_request: "approval",
}

const idOf = (it: InboxItem) => `${it.provider}:${it.account}:${it.id}`
const who = (it: InboxItem) => it.from_name || it.from.split("/").pop() || it.from

function when(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ""
  const now = new Date()
  const time = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
  if (d.toDateString() === now.toDateString()) return time
  const day = d.toLocaleDateString([], {
    day: "numeric",
    month: "short",
    ...(d.getFullYear() === now.getFullYear() ? {} : { year: "numeric" }),
  })
  return `${day} ${time}`
}

/**
 * The Inbox window: the list on the left, the open message on the right. Opening a message
 * marks it read; e archives, u shows unread only, j/k move. An ask (a question, a decision,
 * an approval) shows its options as buttons; a reply goes to the sender, in the thread.
 */
export function InboxWindow() {
  const [view, setView] = useState<View>("unread")
  const [items, setItems] = useState<InboxItem[]>([])
  const [sources, setSources] = useState<InboxSource[]>([])
  const [open, setOpen] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [said, setSaid] = useState("")
  const [draft, setDraft] = useState("")
  const viewRef = useRef(view)
  viewRef.current = view

  const load = useCallback(async () => {
    setBusy(true)
    try {
      const v = viewRef.current
      const r = await inbox.list({ unread: v === "unread", archived: v === "archived" })
      setSources(r.sources)
      // A message opened in the unread view stays listed until the view is reloaded.
      setItems(v === "archived" ? r.items.filter((i) => i.archived) : r.items)
      setError("")
    } catch (e) {
      setError(e instanceof Error ? e.message : "The inbox could not be read.")
    } finally {
      setBusy(false)
    }
  }, [])

  // biome-ignore lint/correctness/useExhaustiveDependencies: reload when the view changes
  useEffect(() => {
    void load()
    const timer = window.setInterval(() => void load(), 60_000)
    return () => window.clearInterval(timer)
  }, [load, view])

  const current = useMemo(() => items.find((i) => idOf(i) === open), [items, open])
  const patch = (it: InboxItem, change: Partial<InboxItem>) =>
    setItems((list) => list.map((i) => (idOf(i) === idOf(it) ? { ...i, ...change } : i)))
  const fail = (e: unknown) => setError(e instanceof Error ? e.message : "That did not work.")

  const show = (it: InboxItem) => {
    setOpen(idOf(it))
    setDraft("")
    setSaid("")
    if (it.read) return
    patch(it, { read: true })
    inbox.read(it).catch((e) => {
      patch(it, { read: false })
      fail(e)
    })
  }
  const step = (delta: number) => {
    if (!items.length) return
    const at = items.findIndex((i) => idOf(i) === open)
    const next = items[Math.min(items.length - 1, Math.max(0, at + delta))]
    if (next) show(next)
  }
  const archive = (it: InboxItem) => {
    const to = !it.archived
    const at = items.findIndex((i) => idOf(i) === idOf(it))
    setItems((list) => list.filter((i) => idOf(i) !== idOf(it)))
    const next = items[at + 1] ?? items[at - 1]
    setOpen(next ? idOf(next) : "")
    inbox.archive(it, to).catch((e) => {
      fail(e)
      void load()
    })
  }
  const markAll = async () => {
    const unread = items.filter((i) => !i.read)
    if (!unread.length) return
    setItems((list) => list.map((i) => ({ ...i, read: true })))
    try {
      // A few at a time: each is one call through the gateway.
      for (let n = 0; n < unread.length; n += 6)
        await Promise.all(unread.slice(n, n + 6).map((i) => inbox.read(i)))
    } catch (e) {
      fail(e)
    }
    window.dispatchEvent(new Event(INBOX_CHANGED))
    void load()
  }
  const answer = async (it: InboxItem, body: string, choice = "") => {
    if (!body.trim() && !choice) return
    setBusy(true)
    try {
      await inbox.reply(it, body.trim(), choice)
      const label = it.ask?.options?.find((o) => o.id === choice)?.label
      setSaid(choice ? `Answered: ${label ?? choice}. Sent to ${who(it)}.` : `Sent to ${who(it)}.`)
      setDraft("")
      patch(it, { read: true })
    } catch (e) {
      fail(e)
    } finally {
      setBusy(false)
    }
  }

  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    const t = e.target as HTMLElement
    if (t.tagName === "TEXTAREA" || t.tagName === "INPUT" || e.metaKey || e.ctrlKey || e.altKey)
      return
    if (e.key === "j" || e.key === "ArrowDown") step(1)
    else if (e.key === "k" || e.key === "ArrowUp") step(-1)
    else if (e.key === "e" && current) archive(current)
    else if (e.key === "u") setView(view === "unread" ? "all" : "unread")
    else return
    e.preventDefault()
  }

  const unread = items.filter((i) => !i.read).length
  const broken = sources.filter((s) => s.error)

  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: the window's own keys (j, k, e, u)
    <div className="inbox" onKeyDown={onKey}>
      <header className="inbox-head">
        <div className="inbox-views" role="tablist">
          {(["unread", "all", "archived"] as View[]).map((v) => (
            <button
              key={v}
              type="button"
              role="tab"
              aria-selected={view === v}
              data-on={view === v}
              onClick={() => {
                setView(v)
                setOpen("")
              }}
            >
              {v}
            </button>
          ))}
        </div>
        <span className="inbox-count">
          {items.length} {items.length === 1 ? "message" : "messages"}
          {view !== "unread" && unread > 0 ? ` · ${unread} unread` : ""}
        </span>
        <span className="inbox-spacer" />
        <button
          type="button"
          className="inbox-tool"
          title="Write a new message (⌥⇧M)"
          onClick={() => composeMessage()}
        >
          <MailPlus aria-hidden="true" size={14} strokeWidth={1.6} />
          <span>New</span>
        </button>
        <button
          type="button"
          className="inbox-tool"
          disabled={!unread}
          title="Mark every listed message read"
          onClick={() => void markAll()}
        >
          <MailOpen aria-hidden="true" size={14} strokeWidth={1.6} />
          <span>Mark all read</span>
        </button>
        <button
          type="button"
          className="inbox-tool"
          title="Check for new messages"
          aria-label="Refresh"
          data-busy={busy}
          onClick={() => void load()}
        >
          <RefreshCw aria-hidden="true" size={14} strokeWidth={1.6} />
        </button>
      </header>
      {(error || broken.length > 0) && (
        <p className="inbox-error" role="alert">
          {error || broken.map((s) => `${s.title}: ${s.error}`).join(" · ")}
        </p>
      )}
      <div className="inbox-body">
        <ul className="inbox-list">
          {items.map((it) => (
            <li key={idOf(it)}>
              <button
                type="button"
                className="inbox-row"
                data-open={idOf(it) === open}
                data-unread={!it.read}
                onClick={() => show(it)}
              >
                <span className="inbox-row-top">
                  <span className="inbox-from">{who(it)}</span>
                  <time dateTime={it.at}>{when(it.at)}</time>
                </span>
                <span className="inbox-subject">{it.subject || "(no subject)"}</span>
                <span className="inbox-tags">
                  {it.ask && <span className="inbox-tag inbox-ask">{ASK[it.ask.kind]}</span>}
                  <span className="inbox-tag">{it.kind.replace("_", " ")}</span>
                </span>
              </button>
            </li>
          ))}
          {!items.length && !busy && (
            <li className="inbox-none">
              {view === "unread" ? "Nothing unread." : "Nothing here."}
            </li>
          )}
        </ul>
        <section className="inbox-detail">
          {current ? (
            <>
              <header className="inbox-detail-head">
                <h2>{current.subject || "(no subject)"}</h2>
                <p>
                  <strong>{who(current)}</strong>
                  {current.from_role ? ` · ${current.from_role}` : ""} · {when(current.at)} ·{" "}
                  {current.kind.replace("_", " ")}
                </p>
                <p className="inbox-addr">
                  <button
                    type="button"
                    className="cite-urn"
                    title={`Write to ${who(current)}`}
                    onClick={() => composeMessage({ to: current.from })}
                  >
                    {current.from}
                  </button>{" "}
                  → {current.account}
                  {current.ask?.re ? ` · ${current.ask.re}` : ""}
                </p>
                <div className="inbox-detail-tools">
                  <button
                    type="button"
                    className="inbox-tool"
                    title="Send this message on to someone else"
                    onClick={() =>
                      composeMessage({
                        to: "",
                        subject: /^fwd:/i.test(current.subject)
                          ? current.subject
                          : `Fwd: ${current.subject}`,
                        body: `\n\n---\nForwarded message from ${who(current)} (${current.from}), ${when(current.at)}:\n\n${current.body}`,
                      })
                    }
                  >
                    <Forward aria-hidden="true" size={14} strokeWidth={1.6} />
                    <span>Forward</span>
                  </button>
                  <button
                    type="button"
                    className="inbox-tool"
                    title={current.archived ? "Back to the inbox" : "Archive (e)"}
                    onClick={() => archive(current)}
                  >
                    {current.archived ? (
                      <ArchiveRestore aria-hidden="true" size={14} strokeWidth={1.6} />
                    ) : (
                      <Archive aria-hidden="true" size={14} strokeWidth={1.6} />
                    )}
                    <span>{current.archived ? "Unarchive" : "Archive"}</span>
                  </button>
                </div>
              </header>
              <div className="inbox-text">
                <Markdown source={current.body || "*(empty)*"} />
              </div>
              {current.ask?.options?.length ? (
                <div className="inbox-options">
                  {current.ask.options.map((o) => (
                    <button
                      key={o.id}
                      type="button"
                      className="inbox-option"
                      data-recommended={!!o.recommended}
                      disabled={busy}
                      title={o.why}
                      onClick={() => void answer(current, draft, o.id)}
                    >
                      <strong>{o.label}</strong>
                      {o.recommended && <em>recommended</em>}
                      {o.why && <span>{o.why}</span>}
                    </button>
                  ))}
                  {current.ask.default && (
                    <p className="inbox-default">
                      Without an answer the sender goes with “
                      {current.ask.options.find((o) => o.id === current.ask?.default)?.label ??
                        current.ask.default}
                      ”{current.ask.due ? ` at ${when(current.ask.due)}` : ""}.
                    </p>
                  )}
                </div>
              ) : null}
              <form
                className="inbox-reply"
                onSubmit={(e) => {
                  e.preventDefault()
                  void answer(current, draft)
                }}
              >
                <textarea
                  value={draft}
                  rows={3}
                  placeholder={
                    current.ask?.options?.length
                      ? "A note to go with your choice, or a reply of your own…  ⌘↵ sends"
                      : `Reply to ${who(current)}…  ⌘↵ sends`
                  }
                  onChange={(e) => setDraft(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                      e.preventDefault()
                      void answer(current, draft)
                    }
                  }}
                />
                <div className="inbox-reply-foot">
                  <span role="status">{said || `Sent from ${current.account}`}</span>
                  <button type="submit" className="inbox-send" disabled={busy || !draft.trim()}>
                    <Send aria-hidden="true" size={13} strokeWidth={1.6} />
                    <span>Reply</span>
                  </button>
                </div>
              </form>
            </>
          ) : (
            <p className="inbox-pick">
              {items.length ? "Pick a message." : "You are all caught up."}
              <span>j / k move · e archive · u unread only</span>
            </p>
          )}
        </section>
      </div>
    </div>
  )
}
