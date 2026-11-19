/**
 * Gatecrasher's approval cards in the assistant's panel: what waits on the owner from the
 * portfolio's approval service (a credential grant, a choice, a question, a secret), with
 * the card's own actions. Decided cards stay a while, showing how they ended.
 */
import { ShieldCheck } from "lucide-react"
import { useState } from "react"
import { decide, type GateCard, provideSecret, useGate } from "./gate"

export function GateCards() {
  const { on, cards } = useGate()
  if (!on || !cards.length) return null
  return (
    <>
      {cards.map((c) => (
        <GateCardView key={c.id} c={c} />
      ))}
    </>
  )
}

const when = (iso: string) => {
  const t = new Date(iso)
  return Number.isNaN(t.getTime())
    ? iso
    : t.toLocaleString([], { dateStyle: "short", timeStyle: "short" })
}

function GateCardView({ c }: { c: GateCard }) {
  const card = c.card
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState("")
  const [answer, setAnswer] = useState("")
  const [choice, setChoice] = useState("")
  const [sent, setSent] = useState(false)
  const pending = c.status === "pending"
  const actions = pending ? (card.actions ?? []) : []
  const decline = actions.find((a) => a.kind === "deny")

  const run = async (body: Parameters<typeof decide>[1]) => {
    setBusy(true)
    setProblem("")
    const said = await decide(c.id, body)
    setBusy(false)
    if (said) setProblem(said)
  }

  return (
    <section
      className="agent-questions agent-gate"
      data-tier={card.tier}
      data-status={c.status}
      aria-label={`Approval: ${card.title}`}
    >
      <header className="agent-questions-head">
        <ShieldCheck aria-hidden="true" size={13} />
        <span className="agent-questions-title">{card.title}</span>
        <span className="agent-gate-tier">{card.tier}</span>
      </header>
      {card.summary && <p className="agent-gate-summary">{card.summary}</p>}
      <dl className="agent-gate-facts">
        <dt>From</dt>
        <dd>{card.source}</dd>
        <dt>For</dt>
        <dd>
          {card.principal.sub} ({card.principal.kind})
        </dd>
        {card.requested && (
          <>
            <dt>Grant</dt>
            <dd>
              {card.requested.audience}: {card.requested.scopes.join(", ") || "no scopes"}
              {card.requested.ttl_seconds
                ? `, ${Math.round(card.requested.ttl_seconds / 60)} min`
                : ""}
            </dd>
          </>
        )}
        {pending && (
          <>
            <dt>Expires</dt>
            <dd>{when(card.expires_at)}</dd>
          </>
        )}
      </dl>
      {card.prompt?.question && <p className="agent-gate-question">{card.prompt.question}</p>}

      {pending && card.type === "approve" && (
        <div className="agent-approval-actions">
          {actions
            .filter((a) => a.kind === "approve")
            .map((a) => (
              <button
                key={a.kind}
                type="button"
                className="desk-button"
                data-primary="true"
                disabled={busy}
                onClick={() => void run({ outcome: "approved" })}
              >
                {a.label}
              </button>
            ))}
          <span className="agent-spacer" />
          {decline && (
            <button
              type="button"
              className="desk-button"
              disabled={busy}
              onClick={() => void run({ outcome: "denied" })}
            >
              {decline.label}
            </button>
          )}
        </div>
      )}

      {pending && card.type === "choose" && (
        <div className="agent-gate-form">
          {(card.prompt?.choices ?? []).map((ch) => (
            <label key={ch.id} className="agent-gate-choice">
              <input
                type="radio"
                name={`choice-${c.id}`}
                checked={choice === ch.id}
                onChange={() => setChoice(ch.id)}
              />
              {ch.label}
            </label>
          ))}
          <div className="agent-approval-actions">
            <button
              type="button"
              className="desk-button"
              data-primary="true"
              disabled={busy || !choice}
              onClick={() => void run({ outcome: "approved", choice })}
            >
              Choose
            </button>
            <span className="agent-spacer" />
            {decline && (
              <button
                type="button"
                className="desk-button"
                disabled={busy}
                onClick={() => void run({ outcome: "denied" })}
              >
                {decline.label}
              </button>
            )}
          </div>
        </div>
      )}

      {pending && card.type === "answer" && (
        <div className="agent-gate-form">
          <textarea
            className="agent-gate-input"
            rows={3}
            maxLength={4000}
            value={answer}
            onChange={(e) => setAnswer(e.target.value)}
            placeholder="Your answer"
          />
          <div className="agent-approval-actions">
            <button
              type="button"
              className="desk-button"
              data-primary="true"
              disabled={busy || !answer.trim()}
              onClick={() => void run({ outcome: "approved", answer: answer.trim() })}
            >
              Answer
            </button>
            <span className="agent-spacer" />
            {decline && (
              <button
                type="button"
                className="desk-button"
                disabled={busy}
                onClick={() => void run({ outcome: "denied" })}
              >
                {decline.label}
              </button>
            )}
          </div>
        </div>
      )}

      {pending && card.type === "request_secret" && card.secret && (
        <SecretForm
          form={card.secret}
          sent={sent}
          busy={busy}
          onSent={() => setSent(true)}
          onProblem={setProblem}
          decline={decline ? () => void run({ outcome: "denied" }) : undefined}
          declineLabel={decline?.label}
        />
      )}

      {!pending && (
        <p className="agent-gate-outcome">
          {c.status}
          {card.outcome?.decider ? ` by ${card.outcome.decider.sub}` : ""}
          {card.outcome?.response?.choice ? `: ${card.outcome.response.choice}` : ""}
          {card.outcome?.response?.secret_ref ? " (secret received)" : ""}
        </p>
      )}
      {problem && (
        <p className="agent-notice" data-tone="error">
          {problem}
        </p>
      )}
    </section>
  )
}

/**
 * The write-only secret field. The value lives only in this input until it is posted to the
 * intake, then the field is emptied. It is a password field, so the browser neither shows
 * nor offers to keep it.
 */
function SecretForm({
  form,
  sent,
  busy,
  onSent,
  onProblem,
  decline,
  declineLabel,
}: {
  form: NonNullable<GateCard["card"]["secret"]>
  sent: boolean
  busy: boolean
  onSent: () => void
  onProblem: (s: string) => void
  decline?: () => void
  declineLabel?: string
}) {
  const [value, setValue] = useState("")
  const [posting, setPosting] = useState(false)
  if (sent) {
    return (
      <p className="agent-gate-summary">
        Sent to the requester. This card closes when they confirm it arrived.
      </p>
    )
  }
  const submit = async () => {
    setPosting(true)
    onProblem("")
    const v = value
    setValue("")
    const said = await provideSecret(form, v)
    setPosting(false)
    if (said) onProblem(said)
    else onSent()
  }
  return (
    <form
      className="agent-gate-form"
      autoComplete="off"
      onSubmit={(e) => {
        e.preventDefault()
        if (value) void submit()
      }}
    >
      <input
        className="agent-gate-input"
        type="password"
        autoComplete="new-password"
        spellCheck={false}
        data-1p-ignore="true"
        aria-label="Secret value"
        placeholder="Secret value (sent only to the requester)"
        value={value}
        onChange={(e) => setValue(e.target.value)}
      />
      <div className="agent-approval-actions">
        <button
          type="submit"
          className="desk-button"
          data-primary="true"
          disabled={posting || busy || !value}
        >
          Provide
        </button>
        <span className="agent-spacer" />
        {decline && (
          <button
            type="button"
            className="desk-button"
            disabled={posting || busy}
            onClick={decline}
          >
            {declineLabel ?? "Decline"}
          </button>
        )}
      </div>
    </form>
  )
}
