/** The agent's panel: the running thread, what it is doing, and a box to talk to it. */
import { ArrowUp, ChevronRight, Plus, Square, X } from "lucide-react"
import { useEffect, useLayoutEffect, useRef, useState } from "react"
import { Markdown } from "../cite/ui"
import { useDesk } from "../desktop/context"
import type { DeskSnapshot } from "./api"
import {
  type Approval,
  answer,
  type Item,
  newThread,
  noteContext,
  send,
  start,
  stop,
  useAgentSession,
} from "./session"
import { describeCall } from "./tools"
import "./agent.css"

/** Asks the open panel to take the keyboard (the shortcut, when the panel is already open). */
export const FOCUS_EVENT = "basalisk:agent-focus"

export function AgentPanel({ close }: { close: () => void }) {
  const desk = useDesk()
  const s = useAgentSession()
  const [draft, setDraft] = useState(() => {
    try {
      return window.sessionStorage.getItem("basalisk.agent.draft") ?? ""
    } catch {
      return ""
    }
  })
  const box = useRef<HTMLTextAreaElement>(null)
  const list = useRef<HTMLDivElement>(null)
  const pinned = useRef(true)

  useEffect(() => {
    void start(desk)
  }, [desk])

  // Take the keyboard when summoned.
  useEffect(() => {
    const focus = () => box.current?.focus()
    focus()
    window.addEventListener(FOCUS_EVENT, focus)
    return () => window.removeEventListener(FOCUS_EVENT, focus)
  }, [])

  // The context follows the windows while the panel is open.
  useEffect(() => {
    let last = desk.wmState().focus
    return desk.subscribe(() => {
      const now = desk.wmState().focus
      if (now === last) return
      last = now
      void noteContext()
    })
  }, [desk])

  useEffect(() => {
    try {
      window.sessionStorage.setItem("basalisk.agent.draft", draft)
    } catch {
      // Not kept.
    }
  }, [draft])

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
    <div
      className="agent"
      // What they were looking at is noted before a click here can move the keyboard.
      onPointerDownCapture={() => {
        if (!box.current?.contains(document.activeElement)) void noteContext()
      }}
    >
      <header className="agent-head">
        <span className="agent-title">Agent</span>
        {s.state?.model && <span className="agent-model">{s.state.model}</span>}
        <span className="agent-spacer" />
        <button
          type="button"
          className="agent-icon"
          title="New conversation"
          disabled={!!s.run || !s.items.length}
          onClick={() => void newThread()}
        >
          <Plus size={14} />
        </button>
        <button type="button" className="agent-icon" title="Close" onClick={close}>
          <X size={14} />
        </button>
      </header>

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
        {s.loaded && ready && !s.items.length && (
          <p className="agent-empty">
            Ask about what is in front of you, or ask for something to be done. The agent sees which
            window has focus and what you have selected.
          </p>
        )}
        {s.items.map((item) => (
          <ItemView key={item.id} item={item} />
        ))}
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
        <div className="agent-box">
          <textarea
            ref={box}
            rows={1}
            value={draft}
            placeholder={ready ? "Ask, or say what to do" : "No model configured"}
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
  )
}

function ItemView({ item }: { item: Item }) {
  switch (item.kind) {
    case "user":
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
}

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
function ToolRow({ item }: { item: Extract<Item, { kind: "tool" }> }) {
  const [open, setOpen] = useState(false)
  const input = pretty(item.input)
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
      {open && (
        <div className="agent-tool-body">
          {input && input !== "{}" && <pre>{input}</pre>}
          {item.result !== undefined && <pre>{pretty(item.result).slice(0, 6000)}</pre>}
        </div>
      )}
    </div>
  )
}

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
          Once
        </button>
        <button type="button" className="desk-button" onClick={() => void answer("session")}>
          This session
        </button>
        <button type="button" className="desk-button" onClick={() => void answer("always")}>
          Always
        </button>
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
