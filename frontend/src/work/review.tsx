/**
 * An item's review, in its detail: who rounds go to, the draft round (the owner's notes and
 * the agent's drafts, not yet sent), Send round, the rounds sent with the team's answers,
 * and sign-off. Sending and signing off are only ever the owner's click here; the agent can
 * draft notes and nothing more.
 */
import { BadgeCheck, MessageSquarePlus, Send } from "lucide-react"
import { useState } from "react"
import { ApiError } from "../api/account"
import { type WorkDetail, type WorkRound, work } from "../api/work"
import { useDesk } from "../desktop/context"
import { ago, message } from "./util"

const STATE_LABEL: Record<string, string> = { open: "Open", done: "Done", declined: "Declined" }

export function Review({
  ws,
  d,
  reload,
  changed,
}: {
  ws: string
  d: WorkDetail
  reload: () => Promise<void>
  changed: () => void
}) {
  const desk = useDesk()
  const [status, setStatus] = useState("")
  const [busy, setBusy] = useState(false)
  const [adding, setAdding] = useState<null | {
    kind: "comment" | "suggestion"
    section: string
    quote: string
    replace: string
    text: string
  }>(null)
  const [owners, setOwners] = useState<string | null>(null)

  const act = async (f: () => Promise<string | undefined>) => {
    setBusy(true)
    try {
      setStatus((await f()) ?? "")
    } catch (e) {
      setStatus(message(e))
    }
    setBusy(false)
    await reload()
    changed()
  }

  /** Start a note; a selection in one of the item's sections becomes a suggestion's quote. */
  const start = (kind: "comment" | "suggestion") => {
    let section = ""
    let quote = ""
    const sel = window.getSelection()
    const text = sel?.toString() ?? ""
    const node = sel?.anchorNode
    const el = node instanceof Element ? node : node?.parentElement
    const sec = el?.closest("[data-section]")?.getAttribute("data-section")
    if (sec && text.trim() && (d.sections[sec] ?? "").includes(text)) {
      section = sec
      quote = text
    }
    setAdding({ kind, section, quote, replace: quote, text: "" })
  }

  const send = async () => {
    const to = d.owners.length ? d.owners.join(", ") : "nobody (no owners are set)"
    const yes = await desk.confirm({
      title: `Send round ${(d.rounds[0]?.round ?? 0) + 1} on “${d.title}”?`,
      message: `${d.notes.length} note${d.notes.length === 1 ? "" : "s"} go to Caster as a round on this item, and mail goes from you to ${to}.${d.task ? ` Its Torque task (${d.task}) gets a comment.` : ""}`,
      confirmLabel: "Send round",
    })
    if (!yes) return
    await act(async () => {
      const r = await work.sendRound(ws, d.id)
      return `Round ${r.round} sent (${r.notes} note${r.notes === 1 ? "" : "s"}).${r.mailed.length ? ` Mailed ${r.mailed.join(", ")}.` : ""}${r.not_mailed.length ? ` Not mailed: ${r.not_mailed.join(", ")}.` : ""}`
    })
  }

  const signOff = async () => {
    const yes = await desk.confirm({
      title: `Sign off “${d.title}”?`,
      message: `Revision ${d.rev}'s text is approved as it is: the sign-off is kept on the item and that revision pinned in CAS.${d.stage === "review" ? " It moves on from review." : ""}${d.owners.length ? ` ${d.owners.join(", ")} ${d.owners.length === 1 ? "is" : "are"} told.` : ""}${d.notes.length ? " Your draft notes stay unsent." : ""}`,
      confirmLabel: "Sign off",
    })
    if (!yes) return
    const done = (r: Awaited<ReturnType<typeof work.signOff>>) =>
      `Signed off at revision ${r.rev}.${r.stage ? ` Now ${r.stage}.` : ""}${r.mailed.length ? ` Told ${r.mailed.join(", ")}.` : ""}${r.problem ? ` ${r.problem}` : ""}`
    await act(async () => {
      try {
        return done(await work.signOff(ws, d.id))
      } catch (e) {
        if (!(e instanceof ApiError) || e.status !== 409) throw e
        const anyway = await desk.confirm({
          title: "Some notes are still open",
          message: `${e.message}. Sign off anyway?`,
          confirmLabel: "Sign off anyway",
        })
        if (!anyway) return ""
        return done(await work.signOff(ws, d.id, true))
      }
    })
  }

  const so = d.sign_off
  return (
    <section className="work-sec work-review">
      <h4>Review</h4>
      {so && (
        <div className="work-signed" data-stale={d.stale_sign || undefined}>
          <BadgeCheck size={13} />
          <span>
            Signed off {ago(so.at)} at revision {so.rev}
            {d.stale_sign ? "; the text has changed since" : ""}
          </span>
          <span className="work-spacer" />
          <button
            type="button"
            className="work-link"
            disabled={busy}
            onClick={() =>
              void act(async () => {
                await work.unsign(ws, d.id)
                return "The sign-off is taken off."
              })
            }
          >
            Take off
          </button>
        </div>
      )}

      <div className="work-owners">
        <span className="work-dim">Rounds go to</span>
        {owners === null ? (
          <>
            <span title="The workspace's owners: Tether addresses">
              {d.owners.length ? d.owners.join(", ") : "nobody yet"}
            </span>
            <button
              type="button"
              className="work-link"
              onClick={() => setOwners(d.owners.join(", "))}
            >
              Change
            </button>
          </>
        ) : (
          <form
            onSubmit={(e) => {
              e.preventDefault()
              const list = owners
                .split(/[,\s]+/)
                .map((x) => x.trim())
                .filter(Boolean)
              void act(async () => {
                await work.setOwners(ws, list)
                setOwners(null)
                return "Owners saved for this workspace."
              })
            }}
          >
            <input
              className="work-input"
              // biome-ignore lint/a11y/noAutofocus: the owner asked to change it
              autoFocus
              value={owners}
              onChange={(e) => setOwners(e.target.value)}
              placeholder="msg://agent/…, msg://agent/…"
              aria-label="Owners"
            />
            <button type="submit" className="desk-button" disabled={busy}>
              Save
            </button>
            <button type="button" className="work-link" onClick={() => setOwners(null)}>
              Cancel
            </button>
          </form>
        )}
      </div>

      <div className="work-review-head">
        <span className="work-dim">
          {d.notes.length
            ? `Draft round: ${d.notes.length} note${d.notes.length === 1 ? "" : "s"}, not sent`
            : "No notes in the draft round"}
        </span>
        <span className="work-spacer" />
        <button
          type="button"
          className="work-icon"
          title="Comment (on the whole item, or a section)"
          onClick={() => start("comment")}
        >
          <MessageSquarePlus size={13} />
        </button>
        <button
          type="button"
          className="work-link"
          title="Suggest a change: select the passage in a section first"
          onClick={() => start("suggestion")}
        >
          Suggest
        </button>
      </div>

      {adding && (
        <form
          className="work-note work-note-form"
          onSubmit={(e) => {
            e.preventDefault()
            const a = adding
            void act(async () => {
              await work.addNote(ws, d.id, {
                kind: a.kind,
                section: a.section || undefined,
                quote: a.kind === "suggestion" ? a.quote : undefined,
                replace: a.kind === "suggestion" ? a.replace : undefined,
                text: a.text.trim() || undefined,
              })
              setAdding(null)
              return ""
            })
          }}
        >
          <div className="work-note-head">
            <b>{adding.kind === "suggestion" ? "Suggestion" : "Comment"}</b>
            <select
              className="work-select"
              value={adding.section}
              onChange={(e) => setAdding({ ...adding, section: e.target.value })}
              aria-label="Section"
            >
              {adding.kind === "comment" && <option value="">The whole item</option>}
              {adding.kind === "suggestion" && !adding.section && (
                <option value="">Section…</option>
              )}
              {d.order.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </div>
          {adding.kind === "suggestion" && (
            <>
              <textarea
                value={adding.quote}
                onChange={(e) => setAdding({ ...adding, quote: e.target.value })}
                placeholder="The passage as it is (exactly)"
                rows={2}
                aria-label="Instead of"
              />
              <textarea
                value={adding.replace}
                onChange={(e) => setAdding({ ...adding, replace: e.target.value })}
                placeholder="What to put instead"
                rows={2}
                aria-label="Put"
              />
            </>
          )}
          <textarea
            // biome-ignore lint/a11y/noAutofocus: the note just started
            autoFocus={adding.kind === "comment"}
            value={adding.text}
            onChange={(e) => setAdding({ ...adding, text: e.target.value })}
            placeholder={adding.kind === "comment" ? "The note" : "Why (optional)"}
            rows={2}
            aria-label="Note"
          />
          <div className="work-row">
            <button type="submit" className="desk-button" disabled={busy}>
              Add to the draft round
            </button>
            <button type="button" className="work-link" onClick={() => setAdding(null)}>
              Cancel
            </button>
          </div>
        </form>
      )}

      {d.notes.map((n) => (
        <div key={n.id} className="work-note" data-kind={n.kind}>
          <div className="work-note-head">
            <b>{n.kind === "suggestion" ? "Suggestion" : "Comment"}</b>
            {n.section && <span>{n.section}</span>}
            <span>on revision {n.item_rev}</span>
            <span className="work-spacer" />
            <button
              type="button"
              className="work-icon"
              title="Take it out"
              onClick={() =>
                void act(async () => {
                  await work.dropNote(ws, n.id)
                  return ""
                })
              }
            >
              ×
            </button>
          </div>
          {n.quote && <div className="work-quote">− {n.quote}</div>}
          {n.kind === "suggestion" && <div className="work-note-replace">+ {n.replace}</div>}
          {n.text && <div className="work-rowtext">{n.text}</div>}
        </div>
      ))}

      <div className="work-row">
        <button
          type="button"
          className="desk-button"
          disabled={busy || d.notes.length === 0}
          onClick={() => void send()}
          title="Write the draft round to Caster and mail it to the owners"
        >
          <Send size={12} /> Send round
        </button>
        <button
          type="button"
          className="desk-button"
          disabled={busy}
          onClick={() => void signOff()}
          title="Approve this revision's text"
        >
          <BadgeCheck size={12} /> {so && !d.stale_sign ? "Sign off again" : "Sign off"}
        </button>
      </div>
      {status && <p className="work-status">{status}</p>}

      {d.rounds.length > 0 && d.changed_since_round && (
        <p className="work-dim">Revised since round {d.rounds[0].round} was sent.</p>
      )}
      {d.rounds.map((r, i) => (
        <RoundBox key={r.round} r={r} open={i === 0} />
      ))}
    </section>
  )
}

function RoundBox({ r, open }: { r: WorkRound; open: boolean }) {
  return (
    <details className="work-round" open={open}>
      <summary>
        Round {r.round} · {r.sent_at ? ago(r.sent_at) : "sent"} ·{" "}
        {r.open ? `${r.open} open` : "all answered"}
      </summary>
      {r.problem && <p className="work-problem-inline">{r.problem}</p>}
      {r.notes.map((n) => (
        <div key={n.id} className="work-note" data-kind={n.kind} data-state={n.state}>
          <div className="work-note-head">
            <b>{n.kind === "suggestion" ? "Suggestion" : "Comment"}</b>
            {n.section && <span>{n.section}</span>}
            <span className="work-spacer" />
            <span className="work-state" data-state={n.state}>
              {STATE_LABEL[n.state] ?? n.state}
            </span>
          </div>
          {n.quote && <div className="work-quote">− {n.quote}</div>}
          {n.kind === "suggestion" && <div className="work-note-replace">+ {n.replace}</div>}
          {n.text && <div className="work-rowtext">{n.text}</div>}
          {n.reply && <div className="work-reply">↳ {n.reply}</div>}
        </div>
      ))}
    </details>
  )
}
