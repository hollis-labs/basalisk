/** The agent's panel: the running thread, what it is doing, and a box to talk to it. */

import { ArrowUp, ChevronRight, Plus, Square, X } from "lucide-react"
import {
  createContext,
  memo,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react"
import { cardFor, ResultCardView } from "../cite/cards"
import { Markdown } from "../cite/ui"
import { useDesk } from "../desktop/context"
import { SlashMenu } from "../slash/menu"
import type { DeskSnapshot, TaskInfo } from "./api"
import { seenNews } from "./attention"
import { ContextChip } from "./contexts-chip"
import { GateCards } from "./gate-cards"
import { NeedsStrip } from "./needs"
import { QuestionCards, taskAsks } from "./question-cards"
import { useQuestions } from "./questions"
import { type AgentSession, type Approval, type Item, mainSession, taskSession } from "./session"
import { closeTask, openTask, toneOf, useTasks } from "./tasks"
import { describeCall } from "./tools"
import "./agent.css"

/** Asks the open panel to take the keyboard (the shortcut, when the panel is already open). */
export const FOCUS_EVENT = "basalisk:agent-focus"

/** The session the parts below talk to: the running thread's, or a task's. */
const SessionContext = createContext<AgentSession>(mainSession)

export function AgentPanel({ close }: { close: () => void }) {
  const s = mainSession.use()
  return (
    <Conversation
      session={mainSession}
      draftKey="basalisk.agent.draft"
      summoned
      head={
        <header className="agent-head">
          <span className="agent-title">Bas</span>
          {s.state?.model && <span className="agent-model">{s.state.model}</span>}
          <ContextChip busy={!!s.run} />
          <span className="agent-spacer" />
          <button
            type="button"
            className="agent-icon"
            title="New conversation"
            disabled={!!s.run || !s.items.length}
            onClick={() => void mainSession.newThread()}
          >
            <Plus size={14} />
          </button>
          <button type="button" className="agent-icon" title="Close" onClick={close}>
            <X size={14} />
          </button>
        </header>
      }
      above={
        <>
          <NeedsStrip />
          <TaskStrip />
        </>
      }
      empty="Ask about what is in front of you, or ask for something to be done. The agent sees which window has focus and what you have selected."
      placeholder="Ask, or say what to do"
    />
  )
}

/** The task agents, above the running thread: one line each, opening the task's window. */
function TaskStrip() {
  const tasks = useTasks()
  if (!tasks.length) return null
  return (
    <ul className="agent-tasks" aria-label="Task agents">
      {tasks.slice(0, 6).map((t) => (
        <TaskRow key={t.id} task={t} />
      ))}
    </ul>
  )
}

/** One task in the strip. It says so when its agent is waiting on a permission question. */
function TaskRow({ task: t }: { task: TaskInfo }) {
  const desk = useDesk()
  const questions = taskAsks(useQuestions(), t.id)
  const asks = !!taskSession(t.id).use().approval || questions > 0
  // A run the assistant started for itself (sorting mail) stays out of sight unless it needs
  // an answer or failed; it is listed in Done for you.
  if (t.origin.startsWith("trigger:mail") && !asks && t.state !== "failed") return null
  return (
    <li className="agent-task" data-state={t.state} data-asks={asks}>
      <button
        type="button"
        className="agent-task-open"
        title={
          questions
            ? "It has questions for you: open its window to answer"
            : asks
              ? "It is asking permission: open its window to answer"
              : "Open this task's window"
        }
        onClick={() => openTask(desk, t.id, t.title)}
      >
        <span className="agent-task-dot" data-tone={asks ? "warning" : toneOf(t.state)} />
        <span className="agent-task-title">{t.title}</span>
        <span className="agent-task-state">{asks ? "asks you" : t.state}</span>
      </button>
      {t.state !== "running" && (
        <button
          type="button"
          className="agent-icon"
          title="Put this task away"
          onClick={() => void closeTask(t.id)}
        >
          <X size={12} />
        </button>
      )}
    </li>
  )
}

/** A task agent's window: its own conversation, which can be followed up. */
export function TaskWindow({ task }: { task: string }) {
  const session = taskSession(task)
  const s = session.use()
  const listed = useTasks().find((t) => t.id === task)
  const info = listed ?? s.task
  return (
    <Conversation
      session={session}
      draftKey={`basalisk.agent.draft.${task}`}
      head={
        <header className="agent-head">
          <span className="agent-task-dot" data-tone={toneOf(info?.state ?? "")} />
          <span className="agent-title">{info?.title ?? "Task"}</span>
          <span className="agent-model">{info?.state}</span>
        </header>
      }
      empty="This task has nothing in it yet."
      placeholder="Say more to this task's agent"
    />
  )
}

function Conversation({
  session,
  draftKey,
  head,
  above,
  empty,
  placeholder,
  summoned,
}: {
  session: AgentSession
  /** Where the unsent text is kept across reloads. */
  draftKey: string
  head: React.ReactNode
  above?: React.ReactNode
  empty: string
  placeholder: string
  /** The panel: takes the keyboard when summoned, and offers the slash commands. */
  summoned?: boolean
}) {
  const desk = useDesk()
  const s = session.use()
  const { start, noteContext, send, stop } = session
  const [draft, setDraft] = useState(() => {
    try {
      return window.sessionStorage.getItem(draftKey) ?? ""
    } catch {
      return ""
    }
  })
  const box = useRef<HTMLTextAreaElement>(null)
  const list = useRef<HTMLDivElement>(null)
  const pinned = useRef(true)

  useEffect(() => {
    void start(desk)
    seenNews() // what the assistant said unasked is in front of the owner now
  }, [desk, start])

  // Take the keyboard when summoned.
  useEffect(() => {
    if (!summoned) return
    const focus = () => box.current?.focus()
    focus()
    window.addEventListener(FOCUS_EVENT, focus)
    return () => window.removeEventListener(FOCUS_EVENT, focus)
  }, [summoned])

  // The context follows the windows while the panel is open.
  useEffect(() => {
    let last = desk.wmState().focus
    return desk.subscribe(() => {
      const now = desk.wmState().focus
      if (now === last) return
      last = now
      void noteContext()
    })
  }, [desk, noteContext])

  // Kept shortly after the typing pauses, not on every key; and when the panel or page goes.
  const latest = useRef(draft)
  latest.current = draft
  useEffect(() => {
    const save = () => {
      try {
        window.sessionStorage.setItem(draftKey, latest.current)
      } catch {
        // Not kept.
      }
    }
    window.addEventListener("pagehide", save)
    return () => {
      window.removeEventListener("pagehide", save)
      save()
    }
  }, [draftKey])
  useEffect(() => {
    const timer = window.setTimeout(() => {
      try {
        window.sessionStorage.setItem(draftKey, draft)
      } catch {
        // Not kept.
      }
    }, 250)
    return () => window.clearTimeout(timer)
  }, [draft, draftKey])

  // Typing redraws this component; the thread below is only rebuilt when it changes.
  const rows = useMemo(() => folded(s.items), [s.items])

  // Stay at the bottom while the answer grows, unless the owner scrolled up to read.
  // biome-ignore lint/correctness/useExhaustiveDependencies: scroll whenever the content changes
  useLayoutEffect(() => {
    const el = list.current
    if (el && pinned.current) el.scrollTop = el.scrollHeight
  }, [s.items, s.approval])

  // The box grows with what is typed, up to a third of the panel.
  // biome-ignore lint/correctness/useExhaustiveDependencies: re-measure when the text changes
  useLayoutEffect(() => {
    const el = box.current
    if (!el) return
    el.style.height = "auto"
    el.style.height = `${Math.min(el.scrollHeight, 220)}px`
  }, [draft])

  const submit = () => {
    const text = draft.trim()
    if (!text || s.run) return
    setDraft("")
    pinned.current = true
    void send(text).then((ok) => {
      if (!ok) setDraft(text)
    })
  }

  const ready = !!s.state?.ready
  return (
    <SessionContext.Provider value={session}>
      <div
        className="agent"
        // What they were looking at is noted before a click here can move the keyboard.
        onPointerDownCapture={() => {
          if (!box.current?.contains(document.activeElement)) void noteContext()
        }}
      >
        {head}
        {above}

        <div
          className="agent-list"
          ref={list}
          onScroll={(e) => {
            const el = e.currentTarget
            pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40
          }}
        >
          {!s.loaded && <p className="agent-empty">…</p>}
          {s.loaded && s.state && !s.state.available && (
            <p className="agent-empty">The agent is not part of this server.</p>
          )}
          {s.loaded && s.state?.available && !ready && (
            <p className="agent-empty">
              No model is configured yet. Set <code>agent_gateway_url</code> and{" "}
              <code>agent_model</code> in Basalisk's config and put the gateway key in{" "}
              <code>~/.config/basalisk/agent-key</code>.
            </p>
          )}
          {s.loaded && ready && !s.items.length && <p className="agent-empty">{empty}</p>}
          {rows.map((item) =>
            item.kind === "steps" ? (
              <StepsFold key={item.id} tools={item.tools} />
            ) : (
              <ItemView key={item.id} item={item} />
            ),
          )}
          {session === mainSession && <GateCards />}
          <QuestionCards session={session} />
          {s.run && !s.approval && <Working items={s.items} />}
          {s.approval && <ApprovalCard q={s.approval} />}
          {s.error && (
            <p className="agent-notice" data-tone="error">
              {s.error}
            </p>
          )}
        </div>

        <footer className="agent-foot">
          <ContextLine context={s.context} />
          {summoned && <SlashMenu box={box} text={draft} setText={setDraft} />}
          <div className="agent-box">
            <textarea
              ref={box}
              rows={1}
              value={draft}
              placeholder={ready ? placeholder : "No model configured"}
              disabled={!ready}
              spellCheck
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                  e.preventDefault()
                  submit()
                } else if (e.key === "Escape") {
                  e.preventDefault()
                  // Back to the window, leaving the panel open.
                  const focus = desk.wmState().focus
                  if (focus) desk.wm({ type: "focus", id: focus })
                  e.currentTarget.blur()
                  document.querySelector<HTMLElement>(`[data-win="${focus}"]`)?.focus()
                }
              }}
            />
            {s.run ? (
              <button type="button" className="agent-send" title="Stop" onClick={stop}>
                <Square size={13} />
              </button>
            ) : (
              <button
                type="button"
                className="agent-send"
                title="Send (Enter)"
                disabled={!ready || !draft.trim()}
                onClick={submit}
              >
                <ArrowUp size={15} />
              </button>
            )}
          </div>
        </footer>
      </div>
    </SessionContext.Provider>
  )
}

type ToolItem = Extract<Item, { kind: "tool" }>

/**
 * The thread with its steps folded: tool calls that came one after another become one line,
 * so the conversation reads as what was said and what came of it. A call still running, and
 * one whose result is a card (a task started, a list of records), stay as they are.
 */
/** Whether a tool call folds away, remembered per item: items are replaced, never changed. */
const quietCache = new WeakMap<Item, boolean>()
function isQuiet(item: Item) {
  let quiet = quietCache.get(item)
  if (quiet === undefined) {
    quiet =
      item.kind === "tool" &&
      !item.running &&
      !cardFor(item.name, item.input, item.result, !!item.isError)
    quietCache.set(item, quiet)
  }
  return quiet
}

function folded(items: Item[]): (Item | { kind: "steps"; id: string; tools: ToolItem[] })[] {
  const out: (Item | { kind: "steps"; id: string; tools: ToolItem[] })[] = []
  let run: ToolItem[] = []
  const flush = () => {
    if (run.length === 1) out.push(run[0])
    else if (run.length > 1) out.push({ kind: "steps", id: `steps-${run[0].id}`, tools: run })
    run = []
  }
  for (const item of items) {
    if (isQuiet(item)) run.push(item as ToolItem)
    else {
      flush()
      out.push(item)
    }
  }
  flush()
  return out
}

/** Several steps as one line; opening it shows each. */
function StepsFold({ tools }: { tools: ToolItem[] }) {
  const [open, setOpen] = useState(false)
  const failed = tools.filter((t) => t.isError).length
  const last = tools[tools.length - 1]
  return (
    <div className="agent-steps" data-open={open}>
      <button type="button" className="agent-tool-line" onClick={() => setOpen((o) => !o)}>
        <ChevronRight size={12} className="agent-tool-caret" data-open={open} />
        <span className="agent-tool-name">
          {tools.length} steps
          {open
            ? ""
            : ` · ${(last.input !== undefined && describeCall(last.name, last.input)) || last.name.replaceAll("_", " ")}`}
        </span>
        <span className="agent-tool-state">{failed ? `${failed} failed` : ""}</span>
      </button>
      {open && tools.map((t) => <ToolRow key={t.id} item={t} />)}
    </div>
  )
}

const ItemView = memo(function ItemView({ item }: { item: Item }) {
  switch (item.kind) {
    case "user":
      if (item.auto) return <div className="agent-auto">{item.auto}</div>
      return (
        <div className="agent-user">
          {item.context?.focused && <WhereChip context={item.context} />}
          <div className="agent-user-text">{item.text}</div>
        </div>
      )
    case "text":
      return item.text ? (
        <div className="agent-text">
          <Markdown source={item.text} />
        </div>
      ) : null
    case "tool":
      return <ToolRow item={item} />
    case "notice":
      return (
        <p className="agent-notice" data-tone={item.tone}>
          {item.text}
        </p>
      )
  }
})

const pretty = (v: unknown) => {
  if (v === undefined) return ""
  if (typeof v === "string") {
    try {
      return JSON.stringify(JSON.parse(v), null, 2)
    } catch {
      return v
    }
  }
  return JSON.stringify(v, null, 2)
}

/** One tool call: a line that says what was done, opening to what was sent and what came back. */
const ToolRow = memo(function ToolRow({ item }: { item: Extract<Item, { kind: "tool" }> }) {
  const [open, setOpen] = useState(false)
  // Only worked out when the row is open.
  const input = open ? pretty(item.input) : ""
  return (
    <div className="agent-tool" data-error={!!item.isError} data-running={item.running}>
      <button type="button" className="agent-tool-line" onClick={() => setOpen((o) => !o)}>
        <ChevronRight size={12} className="agent-tool-caret" data-open={open} />
        <span className="agent-tool-name">
          {(item.input !== undefined && describeCall(item.name, item.input)) ||
            item.name.replaceAll("_", " ")}
        </span>
        <span className="agent-tool-state">
          {item.running ? "working" : item.isError ? "failed" : ""}
        </span>
      </button>
      <ResultCardView item={item} />
      {open && (
        <div className="agent-tool-body">
          {input && input !== "{}" && <pre>{input}</pre>}
          {item.result !== undefined && <pre>{pretty(item.result).slice(0, 6000)}</pre>}
        </div>
      )}
    </div>
  )
})

function Working({ items }: { items: Item[] }) {
  const last = items[items.length - 1]
  // Text arriving is its own sign of life.
  if (last?.kind === "text" && last.text) return null
  if (last?.kind === "tool" && last.running) return null
  return (
    <p className="agent-working" aria-live="polite">
      <span />
      <span />
      <span />
    </p>
  )
}

function ApprovalCard({ q }: { q: Approval }) {
  const { answer } = useContext(SessionContext)
  const first = useRef<HTMLButtonElement>(null)
  useEffect(() => first.current?.focus(), [])
  const input = pretty(q.input)
  const summary = describeCall(q.tool, q.input)
  return (
    <div className="agent-approval" role="alertdialog" aria-label="The agent asks permission">
      <div className="agent-approval-title">
        Allow <strong>{q.tool.replaceAll("_", " ")}</strong>?
      </div>
      {summary && <div className="agent-approval-summary">{summary}</div>}
      {input && input !== "{}" && <pre>{input.slice(0, 2000)}</pre>}
      <div className="agent-approval-actions">
        <button
          ref={first}
          type="button"
          className="desk-button"
          data-primary="true"
          onClick={() => void answer("once")}
        >
          {q.standing ? "Once" : "Allow"}
        </button>
        {q.standing && (
          <>
            <button type="button" className="desk-button" onClick={() => void answer("session")}>
              This session
            </button>
            <button type="button" className="desk-button" onClick={() => void answer("always")}>
              Always
            </button>
          </>
        )}
        <span className="agent-spacer" />
        <button type="button" className="desk-button" onClick={() => void answer("deny")}>
          No
        </button>
      </div>
    </div>
  )
}

function whereText(context: DeskSnapshot | null | undefined) {
  const w = context?.focused
  if (!w) return null
  const n = w.selection?.length ?? 0
  return { label: `${w.app} · ${w.title}`, selected: n ? `${n.toLocaleString()} selected` : "" }
}

/** Under the box: what the next message will carry. */
function ContextLine({ context }: { context: DeskSnapshot | null }) {
  const where = whereText(context)
  return (
    <div className="agent-context" title="What the agent is told you are looking at">
      {where ? (
        <>
          <span className="agent-context-label">{where.label}</span>
          {where.selected && <span className="agent-context-sel">{where.selected}</span>}
        </>
      ) : (
        <span className="agent-context-label">No window in focus</span>
      )}
    </div>
  )
}

/** On a sent message: where it was said. */
function WhereChip({ context }: { context: DeskSnapshot }) {
  const where = whereText(context)
  if (!where) return null
  return (
    <div className="agent-where">
      {where.label}
      {where.selected && ` · ${where.selected}`}
    </div>
  )
}
