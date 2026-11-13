/**
 * Settings → Agent: what the agent can do (every tool, by app, with how it is allowed), the
 * standing permissions ("Always" answers, which can be removed here), and the model in use.
 */
import { useCallback, useEffect, useState } from "react"
import { request } from "../api/account"
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
