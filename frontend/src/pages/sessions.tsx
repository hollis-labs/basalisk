import { Button, Input } from "@hollis-labs/design-components"
import { type FormEvent, useCallback, useEffect, useRef, useState } from "react"
import { ApiError, request } from "../api/account"
import { Field, Notice } from "../components/forms"

interface WorkContext {
  id: string
  project: string
  workstream: string
  source: string
  recordedAt: string
}
interface ActivitySession {
  sequence: number
  id: string
  userId: string
  title: string
  status: "active" | "paused" | "ended"
  revision: number
  context: WorkContext
  startedAt: string
  endedAt?: string
}
interface ActivityEvent {
  id: string
  kind: string
  context: WorkContext
  occurredAt: string
  source: string
}
interface State {
  identity: { installationId: string; userId: string }
  current: ActivitySession | null
}
interface Page<T> {
  items: T[]
  nextBefore?: number
}
interface Command {
  requestId: string
  action: "start" | "context" | "pause" | "resume" | "end"
  sessionId?: string
  revision?: number
  title?: string
  project?: string
  workstream?: string
}
const eventNames: Record<string, string> = {
  started: "Session started",
  context_changed: "Context changed",
  paused: "Session paused",
  resumed: "Session resumed",
  ended: "Session ended",
}
const errorMessage = (e: unknown) => (e instanceof Error ? e.message : "Could not load activity.")

interface ArtifactLink {
  sequence: number
  source: string
  receipt: {
    captureID: string
    fragmentID: string
    fragmentRevisionID?: string
    draftAt: string
    savedAt: string
    draft: { project: string; workstream: string; precedingApp?: string; contextID: string }
    saveContext?: { project: string; contextID: string }
  }
}
function ArtifactLinks({ id, timezone }: { id: string; timezone: string }) {
  const [page, setPage] = useState<Page<ArtifactLink>>({ items: [] })
  const [error, setError] = useState("")
  const expanded = useRef(false)
  useEffect(() => {
    let cancelled = false
    setPage({ items: [] })
    expanded.current = false
    async function load() {
      try {
        const result = await request<Page<ArtifactLink>>(`activity/sessions/${id}/artifacts`)
        if (!cancelled && !expanded.current) {
          setPage(result)
          setError("")
        }
      } catch (e) {
        if (!cancelled) setError(errorMessage(e))
      }
    }
    void load()
    const timer = window.setInterval(() => {
      if (!expanded.current) void load()
    }, 10000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [id])
  async function older() {
    expanded.current = true
    try {
      const next = await request<Page<ArtifactLink>>(
        `activity/sessions/${id}/artifacts?before=${page.nextBefore}`,
      )
      setPage((previous) => ({
        items: [...previous.items, ...next.items],
        nextBefore: next.nextBefore,
      }))
    } catch (e) {
      setError(errorMessage(e))
    }
  }
  return (
    <section className="stack">
      <h3>Saved fragments</h3>
      <Notice error={error} />
      {page.items.length === 0 && !error && (
        <p className="text-fg-muted">Fragments saved with session context will appear here.</p>
      )}
      <ol className="activity-timeline">
        {page.items.map(({ sequence, receipt, source }) => (
          <li key={sequence}>
            <h3>Fragment saved</h3>
            <p>
              <code>{receipt.fragmentID}</code>
            </p>
            <p>
              {receipt.draft.project || "No project set"}
              {receipt.draft.workstream ? ` · ${receipt.draft.workstream}` : ""}
              {receipt.draft.precedingApp ? ` · Following ${receipt.draft.precedingApp}` : ""}
            </p>
            <p className="text-label text-fg-muted">
              Draft opened{" "}
              {new Date(receipt.draftAt).toLocaleString(undefined, { timeZone: timezone })}
              {source === "basalisk.fe-response (server-confirmed)"
                ? " · FE save confirmed by Basalisk"
                : " · Save reported by Frag"}
            </p>
            {receipt.saveContext && receipt.saveContext.contextID !== receipt.draft.contextID && (
              <p>
                Save-time project: {receipt.saveContext.project || "Not set"}. Draft context
                preserved.
              </p>
            )}
          </li>
        ))}
      </ol>
      {!!page.nextBefore && (
        <Button variant="outline" onClick={() => void older()}>
          Older fragment links
        </Button>
      )}
    </section>
  )
}

function Timeline({ id, timezone }: { id: string; timezone: string }) {
  const [page, setPage] = useState<Page<ActivityEvent> | null>(null)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [retry, setRetry] = useState(0)
  // biome-ignore lint/correctness/useExhaustiveDependencies: The retry counter deliberately refetches a failed request.
  useEffect(() => {
    let cancelled = false
    setPage(null)
    setError("")
    request<Page<ActivityEvent>>(`activity/sessions/${id}/events`)
      .then((value) => {
        if (!cancelled) setPage(value)
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(errorMessage(e))
      })
    return () => {
      cancelled = true
    }
  }, [id, retry])
  async function older() {
    setBusy(true)
    setError("")
    try {
      const next = await request<Page<ActivityEvent>>(
        `activity/sessions/${id}/events?before=${page?.nextBefore}`,
      )
      setPage((previous) => ({
        items: [...(previous?.items ?? []), ...next.items],
        nextBefore: next.nextBefore,
      }))
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="stack">
      <ArtifactLinks id={id} timezone={timezone} />
      <Notice error={error} />
      {!page && !error && <p role="status">Loading history…</p>}
      {!page && error && (
        <Button variant="outline" onClick={() => setRetry((n) => n + 1)}>
          Retry history
        </Button>
      )}
      <ol className="activity-timeline">
        {page?.items.map((event) => (
          <li key={event.id}>
            <h3>{eventNames[event.kind] ?? event.kind}</h3>
            <p>
              {event.context.project || "No project set"} ·{" "}
              {event.context.workstream || "No workstream set"}
            </p>
            <p className="text-label text-fg-muted">
              <time dateTime={event.occurredAt}>
                {new Date(event.occurredAt).toLocaleString(undefined, { timeZone: timezone })}
              </time>
              {" · Set by you in Basalisk"}
            </p>
          </li>
        ))}
      </ol>
      {page?.nextBefore && (
        <Button variant="outline" disabled={busy} onClick={() => void older()}>
          {busy ? "Loading…" : "Older events"}
        </Button>
      )}
    </div>
  )
}

export function SessionsPage({ timezone }: { timezone: string }) {
  const [state, setState] = useState<State | null>(null)
  const [history, setHistory] = useState<Page<ActivitySession>>({ items: [] })
  const [selected, setSelected] = useState("")
  const [refresh, setRefresh] = useState(0)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [pending, setPending] = useState<Command | null>(null)
  const [confirmEnd, setConfirmEnd] = useState(false)
  const reload = useCallback(async (selectId?: string) => {
    const [next, sessions] = await Promise.all([
      request<State>("activity"),
      request<Page<ActivitySession>>("activity/sessions"),
    ])
    setState(next)
    setHistory(sessions)
    setSelected(selectId ?? next.current?.id ?? sessions.items[0]?.id ?? "")
    setRefresh((n) => n + 1)
    setConfirmEnd(false)
  }, [])
  useEffect(() => {
    void reload().catch((e: unknown) => setError(errorMessage(e)))
  }, [reload])

  async function send(command: Command) {
    setBusy(true)
    setError("")
    let accepted = false
    try {
      const result = await request<ActivitySession>("activity/commands", "POST", command)
      accepted = true
      setPending(null)
      await reload(result.id)
    } catch (e) {
      setError(
        accepted
          ? "Saved, but the view could not refresh. Refresh to see the latest activity."
          : errorMessage(e),
      )
      setPending(!accepted && (!(e instanceof ApiError) || e.status >= 500) ? command : null)
    } finally {
      setBusy(false)
    }
  }
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    void send({
      requestId: crypto.randomUUID(),
      action: state?.current ? "context" : "start",
      ...(state?.current
        ? { sessionId: state.current.id, revision: state.current.revision }
        : { title: String(data.get("title") ?? "") }),
      project: String(data.get("project") ?? ""),
      workstream: String(data.get("workstream") ?? ""),
    })
  }
  function transition(action: "pause" | "resume" | "end") {
    if (!state?.current) return
    void send({
      requestId: crypto.randomUUID(),
      action,
      sessionId: state.current.id,
      revision: state.current.revision,
    })
  }
  async function olderSessions() {
    setBusy(true)
    setError("")
    try {
      const next = await request<Page<ActivitySession>>(
        `activity/sessions?before=${history.nextBefore}`,
      )
      setHistory((old) => ({ items: [...old.items, ...next.items], nextBefore: next.nextBefore }))
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }
  const current = state?.current
  const disabled = busy || pending !== null
  const chosen = history.items.find((s) => s.id === selected)
  return (
    <div className="page-content">
      <div className="page-intro">
        <p className="eyebrow">YOUR ACTIVITY</p>
        <h1>Work sessions</h1>
        <p>Keep a thread through your work, even as you switch projects.</p>
      </div>
      <Notice error={error} />
      {pending && (
        <div className="panel stack">
          <p>The save has not been confirmed. Retry safely with the same request.</p>
          <Button disabled={busy} onClick={() => void send(pending)}>
            Retry save
          </Button>
        </div>
      )}
      <div className="toolbar">
        <p className="text-fg-muted">
          Sessions survive signing out. Context is set manually for now.
        </p>
        <Button
          variant="outline"
          disabled={disabled}
          onClick={() => {
            setBusy(true)
            setError("")
            void reload()
              .catch((e: unknown) => setError(errorMessage(e)))
              .finally(() => setBusy(false))
          }}
        >
          Refresh
        </Button>
      </div>
      {!state && !error && <p role="status">Loading sessions…</p>}
      {state && (
        <div className="account-grid">
          <section className="panel stack">
            <div>
              <p className="eyebrow">{current ? current.status : "READY WHEN YOU ARE"}</p>
              <h2>{current?.title ?? "Start a work session"}</h2>
              <p className="text-fg-muted">
                {current
                  ? "Changing context preserves earlier history."
                  : "Give this stretch of work a name. You can switch projects within it."}
              </p>
            </div>
            {current && (
              <Field label="Activity session ID">
                {(id) => <Input id={id} value={current.id} readOnly />}
              </Field>
            )}
            <form key={`${current?.id ?? "new"}-${current?.revision ?? 0}`} onSubmit={submit}>
              <fieldset disabled={disabled} className="stack">
                {!current && (
                  <Field label="Session name">
                    {(id) => (
                      <Input
                        id={id}
                        name="title"
                        required
                        maxLength={120}
                        placeholder="What are you working on?"
                      />
                    )}
                  </Field>
                )}
                <Field label="Project (optional)">
                  {(id) => (
                    <Input
                      id={id}
                      name="project"
                      defaultValue={current?.context.project ?? ""}
                      maxLength={200}
                    />
                  )}
                </Field>
                <Field label="Workstream (optional)">
                  {(id) => (
                    <Input
                      id={id}
                      name="workstream"
                      defaultValue={current?.context.workstream ?? ""}
                      maxLength={200}
                    />
                  )}
                </Field>
                <Button type="submit">
                  {busy ? "Saving…" : current ? "Save context" : "Start session"}
                </Button>
              </fieldset>
            </form>
            {current && (
              <div className="stack">
                <div className="actions">
                  <Button
                    variant="outline"
                    disabled={disabled}
                    onClick={() => transition(current.status === "paused" ? "resume" : "pause")}
                  >
                    {current.status === "paused" ? "Resume session" : "Pause session"}
                  </Button>
                  <Button variant="outline" disabled={disabled} onClick={() => setConfirmEnd(true)}>
                    End session
                  </Button>
                </div>
                {confirmEnd && (
                  <div className="inline-panel">
                    <p>End this session? Its history will stay available.</p>
                    <div className="actions">
                      <Button disabled={disabled} onClick={() => transition("end")}>
                        Confirm end
                      </Button>
                      <Button
                        variant="ghost"
                        disabled={disabled}
                        onClick={() => setConfirmEnd(false)}
                      >
                        Keep working
                      </Button>
                    </div>
                  </div>
                )}
              </div>
            )}
          </section>
          <section className="panel stack">
            <h2>Session history</h2>
            {history.items.length === 0 && (
              <p className="text-fg-muted">Your first session will begin your timeline.</p>
            )}
            <div className="activity-sessions">
              {history.items.map((session) => (
                <button
                  type="button"
                  key={session.id}
                  aria-pressed={selected === session.id}
                  className="activity-session"
                  onClick={() => setSelected(session.id)}
                >
                  <strong>{session.title}</strong>
                  <span>
                    {session.status} ·{" "}
                    {new Date(session.startedAt).toLocaleDateString(undefined, {
                      timeZone: timezone,
                    })}
                  </span>
                </button>
              ))}
            </div>
            {history.nextBefore && (
              <Button variant="outline" disabled={disabled} onClick={() => void olderSessions()}>
                Older sessions
              </Button>
            )}
            {chosen && (
              <div className="inline-panel stack">
                <h2>{chosen.title}</h2>
                <p className="activity-id text-label text-fg-muted">{chosen.id}</p>
                <Timeline key={`${chosen.id}-${refresh}`} id={chosen.id} timezone={timezone} />
              </div>
            )}
          </section>
        </div>
      )}
    </div>
  )
}
