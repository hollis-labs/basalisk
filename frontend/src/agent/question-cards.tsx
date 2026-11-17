/**
 * Question cards: what the agent asked, each with its options and room for the owner's own
 * words. Answers are held as they are given and can be changed; one Send at the end gives
 * them to the agent together.
 */
import { ChevronRight, X } from "lucide-react"
import { useEffect, useState } from "react"
import { useDesk } from "../desktop/context"
import type { Question, QuestionSet } from "./api"
import { dismissSet, holdAnswer, setsOf, submitSet, useQuestions, waiting } from "./questions"
import type { AgentSession } from "./session"

/** The cards for one thread: the running thread's (task null) or a task's. */
export function QuestionCards({ session }: { session: AgentSession }) {
  const sets = setsOf(useQuestions(), session.thread)
  const busy = !!session.use().run
  if (!sets.length) return null
  return (
    <>
      {sets.map((set) => (
        <SetCard key={set.id} set={set} session={session} busy={busy} />
      ))}
    </>
  )
}

function SetCard({
  set,
  session,
  busy,
}: {
  set: QuestionSet
  session: AgentSession
  busy: boolean
}) {
  const desk = useDesk()
  const [note, setNote] = useState("")
  const [sending, setSending] = useState(false)
  const [problem, setProblem] = useState("")
  const left = waiting([set])
  const total = set.questions.length
  const answered = set.questions.filter((q) => q.state === "answered").length

  // Cards under the same heading sit together, in the order they were asked.
  const groups: { name: string; questions: Question[] }[] = []
  for (const q of set.questions) {
    const name = q.group ?? ""
    const at = groups.find((g) => g.name === name)
    if (at) at.questions.push(q)
    else groups.push({ name, questions: [q] })
  }

  const send = async () => {
    setSending(true)
    setProblem("")
    const said = await submitSet(desk, session, set.id, note)
    if (said) {
      setProblem(said)
      setSending(false)
    }
  }

  return (
    <section className="agent-questions" aria-label={`Questions: ${set.title}`}>
      <header className="agent-questions-head">
        <span className="agent-questions-title">{set.title}</span>
        <span className="agent-questions-count">
          {left ? `${total - left} of ${total} answered` : "all answered"}
        </span>
        <button
          type="button"
          className="agent-icon"
          title="Put these questions away without answering"
          onClick={() => void dismissSet(set.id)}
        >
          <X size={12} />
        </button>
      </header>
      {groups.map((g) => (
        <div key={g.name} className="agent-question-group">
          {g.name && <div className="agent-question-group-name">{g.name}</div>}
          {g.questions.map((q) => (
            <Card key={q.id} set={set.id} q={q} onProblem={setProblem} />
          ))}
        </div>
      ))}
      <footer className="agent-questions-foot">
        <input
          className="agent-question-note"
          value={note}
          placeholder="Anything to add (optional)"
          onChange={(e) => setNote(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !busy && !sending) void send()
          }}
        />
        <button
          type="button"
          className="desk-button"
          data-primary="true"
          disabled={busy || sending}
          title={
            busy
              ? "The agent is still working: send when it is done"
              : left
                ? `Send now; ${left} will go as not answered`
                : "Send the answers together"
          }
          onClick={() => void send()}
        >
          {sending
            ? "Sending…"
            : answered === total
              ? "Send answers"
              : `Send ${answered} of ${total}`}
        </button>
      </footer>
      {problem && (
        <p className="agent-notice" data-tone="error">
          {problem}
        </p>
      )}
    </section>
  )
}

function Card({
  set,
  q,
  onProblem,
}: {
  set: string
  q: Question
  onProblem: (text: string) => void
}) {
  const [text, setText] = useState(q.answer.text)
  const [open, setOpen] = useState(false)
  // The server's word for the card replaces what is typed only when it differs from what
  // was last kept (another window answered it).
  const [kept, setKept] = useState(q.answer.text)
  useEffect(() => {
    if (q.answer.text !== kept) {
      setKept(q.answer.text)
      setText(q.answer.text)
    }
  }, [q.answer.text, kept])

  const hold = async (options: string[], words: string, skip = false) => {
    setKept(skip ? "" : words.trim())
    if (skip) setText("")
    const said = await holdAnswer(set, q, { options, text: words }, skip)
    onProblem(said)
  }
  const pick = (id: string) => {
    const has = q.answer.options.includes(id)
    const next = q.multi
      ? has
        ? q.answer.options.filter((o) => o !== id)
        : [...q.answer.options, id]
      : has
        ? []
        : [id]
    void hold(next, text)
  }

  return (
    <div className="agent-question" data-state={q.state}>
      <div className="agent-question-prompt">{q.prompt}</div>
      {q.detail && (
        <>
          <button type="button" className="agent-question-more" onClick={() => setOpen((o) => !o)}>
            <ChevronRight size={11} className="agent-tool-caret" data-open={open} />
            Background
          </button>
          {open && <div className="agent-question-detail">{q.detail}</div>}
        </>
      )}
      {q.options.length > 0 && (
        <div className="agent-question-options" title={q.multi ? "Choose any" : "Choose one"}>
          {q.options.map((o) => {
            const on = q.answer.options.includes(o.id)
            return (
              <button
                key={o.id}
                type="button"
                aria-pressed={on}
                className="agent-question-option"
                data-on={on}
                title={o.note}
                onClick={() => pick(o.id)}
              >
                <span className="agent-question-mark" data-multi={!!q.multi} data-on={on} />
                <span className="agent-question-label">
                  {o.label}
                  {o.recommended && <span className="agent-question-rec">suggested</span>}
                  {o.note && <span className="agent-question-option-note">{o.note}</span>}
                </span>
              </button>
            )
          })}
        </div>
      )}
      <textarea
        className="agent-question-text"
        rows={q.options.length ? 1 : 2}
        value={text}
        placeholder={q.options.length ? "Something else, or a note" : "Your answer"}
        onChange={(e) => setText(e.target.value)}
        onBlur={() => {
          if (text.trim() !== q.answer.text) void hold(q.answer.options, text)
        }}
      />
      <div className="agent-question-foot">
        {q.link && (
          <span
            className="agent-question-link"
            title="When sent, the answer is also written to this item in your plans"
          >
            also noted on your {q.link.kind}
          </span>
        )}
        <span className="agent-spacer" />
        {q.state === "skipped" ? (
          <span className="agent-question-skipped">skipped</span>
        ) : (
          <button
            type="button"
            className="agent-question-skip"
            title="Leave this one; it is sent as skipped"
            onClick={() => void hold([], "", true)}
          >
            Skip
          </button>
        )}
      </div>
    </div>
  )
}

/** A line for the strip: how many questions wait in task threads, which are not in view. */
export function taskAsks(sets: QuestionSet[], task: string) {
  return waiting(setsOf(sets, task))
}
