/**
 * Settings → Agent: what the agent can do (every tool, by app, with how it is allowed), the
 * standing permissions ("Always" answers, which can be removed here), and the model in use.
 */
import { useCallback, useEffect, useState } from "react"
import { request } from "../api/account"
import { openFileRef, placeOf, usePlaces } from "../cite/files"
import { type AgentState, agentApi } from "./api"
import { toolSpecs } from "./tools"

const LEVELS: Record<string, string> = {
  read: "Looks",
  act: "Acts on screen",
  write: "Asks first",
  confirm: "Asks every time",
}
const APPS: Record<string, string> = {
  desktop: "Desktop",
  window: "Desktop",
  browser: "Browser",
  notes: "Notes",
  files: "Files",
  terminal: "Terminal",
  frag: "Frag",
  tesseract: "Tesseract",
  torque: "Torque",
}
const appOf = (name: string) => {
  const prefix = name.split("_")[0]
  return APPS[prefix] ?? prefix.charAt(0).toUpperCase() + prefix.slice(1)
}
/** A tool's first sentence, for a one-line summary. */
const firstSentence = (s: string) => {
  const m = /^(.+?[.!?])(\s|$)/.exec(s)
  return m ? m[1] : s
}

export function AgentSettings() {
  const [state, setState] = useState<AgentState | null>(null)
  const [stateError, setStateError] = useState("")
  const [grants, setGrants] = useState<string[] | null>(null)
  const [grantError, setGrantError] = useState("")
  const [busy, setBusy] = useState("")
  // Tools the server runs itself (Tether gateway): not in the page's toolSpecs().
  const [serverTools, setServerTools] = useState<ReturnType<typeof toolSpecs>>([])

  const loadGrants = useCallback(() => {
    request<{ tools: string[] }>("agent/grants")
      .then((r) => {
        setGrants([...(r.tools ?? [])].sort())
        setGrantError("")
      })
      .catch((e) => setGrantError(e instanceof Error ? e.message : "Could not read them."))
  }, [])

  useEffect(() => {
    agentApi
      .state()
      .then(setState)
      .catch((e) => setStateError(e instanceof Error ? e.message : "Could not reach the agent."))
    loadGrants()
    request<{ tools: ReturnType<typeof toolSpecs> }>("agent/tools")
      .then((r) => setServerTools(r.tools ?? []))
      .catch(() => setServerTools([]))
  }, [loadGrants])

  const remove = async (tool: string) => {
    setBusy(tool)
    try {
      await request(`agent/grants/${encodeURIComponent(tool)}`, "DELETE")
      loadGrants()
    } catch (e) {
      setGrantError(e instanceof Error ? e.message : "Could not remove it.")
    } finally {
      setBusy("")
    }
  }

  const groups = new Map<string, ReturnType<typeof toolSpecs>>()
  const known = new Set(toolSpecs().map((t) => t.name))
  const all = [...toolSpecs(), ...serverTools.filter((t) => !known.has(t.name))]
  for (const t of all.sort((a, b) => a.name.localeCompare(b.name))) {
    const app = appOf(t.name)
    groups.set(app, [...(groups.get(app) ?? []), t])
  }

  return (
    <div className="shell-page agent-settings">
      <section className="panel shell-section">
        <h2>Model</h2>
        {stateError ? (
          <p className="text-danger">{stateError}</p>
        ) : !state ? (
          <p className="text-fg-muted">Asking the server…</p>
        ) : (
          <dl className="shell-facts">
            <dt>Agent</dt>
            <dd>{state.available ? "available" : "not available on this server"}</dd>
            <dt>Model</dt>
            <dd>
              {state.ready ? (
                <code>{state.model || "configured"}</code>
              ) : (
                <span className="text-fg-muted">
                  none configured (set agent_gateway_url and agent_model, see docs/agent.md)
                </span>
              )}
            </dd>
          </dl>
        )}
      </section>

      <section className="panel shell-section">
        <h2>Standing permissions</h2>
        <p className="text-fg-muted">
          Tools you answered <strong>Always</strong> run without asking. Remove one and the agent
          asks again the next time. Answers of <strong>This session</strong> are not listed: they
          end when Basalisk restarts.
        </p>
        {grantError && <p className="text-danger">{grantError}</p>}
        {grants === null ? (
          !grantError && <p className="text-fg-muted">Reading them…</p>
        ) : grants.length === 0 ? (
          <p>No tool has a standing permission: every tool that asks will ask.</p>
        ) : (
          <dl className="shell-facts">
            {grants.map((tool) => (
              <Row key={tool} label={tool.replaceAll("_", " ")}>
                <button
                  type="button"
                  className="desk-button"
                  disabled={busy === tool}
                  onClick={() => void remove(tool)}
                >
                  Remove
                </button>
              </Row>
            ))}
          </dl>
        )}
      </section>

      <Automation />

      {/* The list is long: it takes the page's full width. */}
      <section className="panel shell-section" style={{ gridColumn: "1 / -1" }}>
        <h2>What the agent can do</h2>
        <p className="text-fg-muted">
          <strong>Looks</strong> and <strong>Acts on screen</strong> run at once.{" "}
          <strong>Asks first</strong> asks once, for the session, or always.{" "}
          <strong>Asks every time</strong> asks for each call.
        </p>
        {[...groups.entries()].map(([app, tools]) => (
          <div key={app} className="agent-settings-group">
            <h3>{app}</h3>
            <dl className="shell-facts">
              {tools.map((t) => (
                <Row key={t.name} label={t.name.replaceAll("_", " ")} title={t.description}>
                  <span>
                    <strong>{LEVELS[t.level ?? "write"] ?? LEVELS.write}</strong>
                    {grants?.includes(t.name) ? " · always allowed" : ""}
                    <span className="text-fg-muted"> · {firstSentence(t.description)}</span>
                  </span>
                </Row>
              ))}
            </dl>
          </div>
        ))}
      </section>
    </div>
  )
}

function Row({
  label,
  title,
  children,
}: {
  label: string
  title?: string
  children: React.ReactNode
}) {
  return (
    <>
      <dt title={title}>{label}</dt>
      <dd>{children}</dd>
    </>
  )
}

// ---- What runs without the model (GET /api/agent/automation) ----

interface Automation {
  rules: { id: string; target: string; help?: string }[]
  rules_file?: string
  rules_problem?: string
  reflexes: {
    name: string
    about?: string
    action: string
    trigger?: string
    enabled: boolean
    cooldown_seconds?: number
    fired_count?: number
    last_fired_at?: string
  }[]
  reflexes_file?: string
  reflexes_problem?: string
}

const TARGETS: Record<string, string> = {
  say: "answers with a fixed reply",
  help: "lists these",
  model: "hands the message to the model",
}
const targetText = (t: string) =>
  t.startsWith("tool:") ? `calls ${t.slice(5).replaceAll("_", " ")}` : (TARGETS[t] ?? t)

const TRIGGERS: Record<string, string> = {
  predicate: "on a message",
  event: "on an event",
  interval: "as time passes",
}

const ACTIONS: Record<string, string> = {
  inject_reminder: "reminds the agent",
  halt_session: "stops the run",
  dispatch_to_agent: "starts a task agent",
}

function when(iso?: string) {
  if (!iso) return ""
  const d = new Date(iso)
  return Number.isNaN(d.getTime())
    ? iso
    : d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })
}

function Automation() {
  const [a, setA] = useState<Automation | null>(null)
  const [error, setError] = useState("")
  useEffect(() => {
    request<Automation>("agent/automation")
      .then(setA)
      .catch((e) => setError(e instanceof Error ? e.message : "Could not read them."))
  }, [])
  return (
    <section className="panel shell-section" style={{ gridColumn: "1 / -1" }}>
      <h2>Without the model</h2>
      <p className="text-fg-muted">
        What answers or acts before any model is asked. Read only here; the files below are where
        your own go (see “The router” and “Reflexes” in docs/agent.md).
      </p>
      {error && <p className="text-danger">{error}</p>}
      {!a && !error && <p className="text-fg-muted">Reading them…</p>}
      {a && (
        <>
          <div className="agent-settings-group">
            <h3>Router rules</h3>
            <p className="text-fg-muted">Tried in this order; the first that matches runs.</p>
            {a.rules.length === 0 ? (
              <p>No rules are in use.</p>
            ) : (
              <dl className="shell-facts">
                {a.rules.map((r) => (
                  <Row key={r.id} label={r.id}>
                    {r.help ? (
                      <span>{r.help}</span>
                    ) : (
                      <span className="text-fg-muted">{targetText(r.target)}</span>
                    )}
                  </Row>
                ))}
              </dl>
            )}
            <OwnFile path={a.rules_file} problem={a.rules_problem} />
          </div>
          <div className="agent-settings-group">
            <h3>Reflexes</h3>
            {a.reflexes.length === 0 ? (
              <p>No reflexes are in use.</p>
            ) : (
              <dl className="shell-facts">
                {a.reflexes.map((r) => (
                  <Row key={r.name} label={r.name}>
                    <span>
                      <strong>{r.enabled ? "On" : "Off"}</strong>
                      {r.about ? ` · ${r.about}` : ""}
                      <span className="text-fg-muted">
                        {r.trigger ? ` · ${TRIGGERS[r.trigger] ?? r.trigger}` : ""}
                        {` · ${ACTIONS[r.action] ?? r.action}`}
                        {r.fired_count
                          ? ` · fired ${r.fired_count} time${r.fired_count === 1 ? "" : "s"}, last ${when(r.last_fired_at)}`
                          : " · has not fired"}
                        {r.cooldown_seconds
                          ? ` · at most once in ${cooldown(r.cooldown_seconds)}`
                          : ""}
                      </span>
                    </span>
                  </Row>
                ))}
              </dl>
            )}
            <OwnFile path={a.reflexes_file} problem={a.reflexes_problem} />
          </div>
        </>
      )}
    </section>
  )
}

function cooldown(s: number) {
  if (s < 120) return `${s} seconds`
  if (s < 7200) return `${Math.round(s / 60)} minutes`
  return `${Math.round(s / 3600)} hours`
}

/** Where the owner's own go: a link that opens it in its app when a place holds it. */
function OwnFile({ path, problem }: { path?: string; problem?: string }) {
  const places = usePlaces()
  if (!path && !problem) return null
  const ref = path ? placeOf(path, places) : null
  return (
    <dl className="shell-facts">
      {path && (
        <>
          <dt>Your own</dt>
          <dd>
            {ref ? (
              <button
                type="button"
                className="cite-file"
                title={`Open ${path}`}
                onClick={(e) => void openFileRef(ref, e.currentTarget)}
              >
                <code>{path}</code>
              </button>
            ) : (
              <code>{path}</code>
            )}
          </dd>
        </>
      )}
      {problem && (
        <>
          <dt>Not in use</dt>
          <dd className="text-danger">{problem}</dd>
        </>
      )}
    </dl>
  )
}
