/**
 * Done for you: the record of what the assistant did in the owner's name (internal/assist):
 * receipts it sent, status it asked for, replies and messages it sent, mail it settled. The
 * chat is told outcomes; this is where the steps are.
 */
import { ListChecks, RefreshCw } from "lucide-react"
import { useCallback, useEffect, useState } from "react"
import { openTask, useTasks } from "../../agent/tasks"
import { request } from "../../api/account"
import { UrnLink } from "../../cite/ui"
import { useDesk } from "../../desktop/context"
import { registerApp } from "../../desktop/registry"
import "./assist.css"

interface Entry {
  id: number
  at: string
  kind: "ack" | "route" | "status_ask" | "reply" | "send" | "settle"
  summary: string
  with?: string
  ref?: string
  asked: boolean
  detail?: { body?: string; subject?: string; receipt?: string; about?: string; choice?: string }
}

const KIND: Record<string, string> = {
  ack: "receipt",
  status_ask: "status asked",
  reply: "reply",
  send: "sent",
  settle: "settled",
  route: "passed on",
}

interface MailRule {
  id: number
  from?: string
  kind?: string
  subject_has?: string
  class: "needs" | "bas" | "digest" | "settle"
  note?: string
}

const CLASS: Record<string, string> = {
  needs: "shown to you",
  bas: "handed to Bas",
  digest: "held and counted",
  settle: "marked read, on the record",
}

/** The owner's rules for sorting their mail: what each matches, where it sends it, and a way to drop it. */
function Rules() {
  const [rules, setRules] = useState<MailRule[] | null>(null)
  const load = useCallback(
    () =>
      request<{ rules: MailRule[] }>("assist/rules")
        .then((r) => setRules(r.rules ?? []))
        .catch(() => setRules(null)),
    [],
  )
  useEffect(() => {
    void load()
  }, [load])
  if (rules === null)
    return <p className="assist-none">Mail is not being sorted on this Basalisk.</p>
  return (
    <div className="assist-rules">
      <p className="assist-none">
        New mail is sorted by the first rule that matches, newest first; with no rule, by what it
        is: a question, decision, approval or escalation is shown to you; a request without options
        is handed to Bas; the rest is held. Make a rule from a card (“This is noise”, “Always tell
        me”), or tell Bas how you want something handled.
      </p>
      {rules.map((r) => (
        <div key={r.id} className="assist-row assist-rule">
          <span>
            {[
              r.from && `from ${r.from}`,
              r.kind && `kind ${r.kind}`,
              r.subject_has && `subject has “${r.subject_has}”`,
            ]
              .filter(Boolean)
              .join(", ")}
          </span>
          <i>{CLASS[r.class] ?? r.class}</i>
          <button
            type="button"
            onClick={() => void request(`assist/rules/${r.id}`, "DELETE").then(load)}
          >
            Remove
          </button>
        </div>
      ))}
      {!rules.length && <p className="assist-none">No rules yet.</p>}
    </div>
  )
}

function AssistWindow() {
  const [tab, setTab] = useState<"done" | "rules">("done")
  const [list, setList] = useState<Entry[]>([])
  const [open, setOpen] = useState(0)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const load = useCallback(async (before = 0) => {
    setBusy(true)
    try {
      const r = await request<{ activity: Entry[] }>(
        `assist/activity${before ? `?before=${before}` : ""}`,
      )
      setList((old) => (before ? [...old, ...r.activity] : r.activity))
      setError("")
    } catch (e) {
      setError(e instanceof Error ? e.message : "The record could not be read.")
    } finally {
      setBusy(false)
    }
  }, [])
  useEffect(() => {
    void load()
    const timer = window.setInterval(() => void load(), 30_000)
    return () => window.clearInterval(timer)
  }, [load])

  const desk = useDesk()
  const runs = useTasks().filter((t) => t.origin.startsWith("trigger:"))
  let day = ""
  return (
    <div className="assist">
      <header>
        <div className="assist-tabs">
          <button type="button" data-on={tab === "done"} onClick={() => setTab("done")}>
            Done for you
          </button>
          <button type="button" data-on={tab === "rules"} onClick={() => setTab("rules")}>
            Mail rules
          </button>
        </div>
        <span>
          {tab === "done"
            ? "What Bas did in your name. Nothing here needs you; it is the record."
            : ""}
        </span>
        <button type="button" aria-label="Refresh" data-busy={busy} onClick={() => void load()}>
          <RefreshCw size={14} strokeWidth={1.6} aria-hidden="true" />
        </button>
      </header>
      {tab === "rules" && <Rules />}
      {error && <p className="assist-error">{error}</p>}
      {tab === "done" && runs.length > 0 && (
        <div className="assist-runs">
          <h3>Working in the background</h3>
          {runs.slice(0, 8).map((t) => (
            <button
              key={t.id}
              type="button"
              className="assist-row"
              onClick={() => openTask(desk, t.id, t.title)}
            >
              <time>
                {new Date(t.updated_at).toLocaleTimeString([], {
                  hour: "2-digit",
                  minute: "2-digit",
                })}
              </time>
              <i>{t.state || "running"}</i>
              <span>{t.title}</span>
            </button>
          ))}
        </div>
      )}
      <ul hidden={tab !== "done"}>
        {list.map((e) => {
          const d = new Date(e.at)
          const thisDay = d.toLocaleDateString([], {
            weekday: "long",
            month: "short",
            day: "numeric",
          })
          const head = thisDay !== day
          day = thisDay
          return (
            <li key={e.id}>
              {head && <h3>{thisDay}</h3>}
              <button
                type="button"
                className="assist-row"
                data-open={open === e.id}
                onClick={() => setOpen(open === e.id ? 0 : e.id)}
              >
                <time>{d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}</time>
                <i data-kind={e.kind}>{KIND[e.kind] ?? e.kind}</i>
                <span>{e.summary}</span>
                {e.asked && <em title="You were asked first, or had said Always">asked</em>}
              </button>
              {open === e.id && (
                <div className="assist-detail">
                  {e.with && (
                    <p>
                      With <UrnLink urn={e.with} />
                    </p>
                  )}
                  {e.detail?.subject && <p className="assist-subject">{e.detail.subject}</p>}
                  {e.detail?.body && <pre>{e.detail.body}</pre>}
                  {e.detail?.choice && <p>Chose option “{e.detail.choice}”.</p>}
                  {e.ref && <p className="assist-ref">message {e.ref}</p>}
                </div>
              )}
            </li>
          )
        })}
        {!list.length && !busy && (
          <li className="assist-none">
            Nothing yet. When the assistant answers, settles or sends anything for you, it is listed
            here.
          </li>
        )}
        {list.length >= 80 && list.length % 80 === 0 && (
          <li>
            <button
              type="button"
              className="assist-more"
              onClick={() => void load(list[list.length - 1].id)}
            >
              Earlier
            </button>
          </li>
        )}
      </ul>
    </div>
  )
}

registerApp({
  id: "assist",
  title: "Done for you",
  icon: ListChecks,
  menu: true,
  size: [720, 640],
  bare: true,
  render: () => <AssistWindow />,
})
