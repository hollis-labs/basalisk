import { Button } from "@hollis-labs/design-components"
import { useEffect, useState } from "react"
import { request } from "../api/account"
import { Notice } from "../components/forms"

type Evidence = { captureID: string; integrationID: string; updatedAt: string; code: string }
type AppReadiness = {
  deviceID: string
  label: string
  authorization: string
  contextAllowed: boolean
  connection: { state: string; integrationID?: string; name?: string }
  worker: { state: string; observedAt?: string }
  queue: {
    queued: number
    needsAttention: number
    saved: number
    stopped: number
    delivering: number
  }
  lastConfirmed?: Evidence
  latestPending?: Evidence
}
type Snapshot = {
  generatedAt: string
  validForSeconds: number
  apps: AppReadiness[]
  nextAfter?: string
}

const labels: Record<string, string> = {
  active: "Capture permission active",
  revoked: "Pairing revoked",
  expired: "Pairing expired",
  capture_not_granted: "Capture permission not granted",
  not_configured: "Delivery is awaiting setup",
  not_assigned: "No delivery worker assigned to this connection",
  configured: "Delivery configured; worker has not started",
  running: "Delivery worker active",
  stopped: "Delivery worker stopped",
  stale: "Worker activity is out of date",
  missing: "No FE connection selected",
  invalid: "Connection needs repair",
}

export function ReadinessPage({ timezone }: { timezone: string }) {
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(true)
  const [query, setQuery] = useState({ after: "", revision: 0 })
  const after = query.after
  const [receivedAt, setReceivedAt] = useState(0)
  const [now, setNow] = useState(Date.now())
  useEffect(() => {
    let active = true
    let pending = false
    async function load() {
      if (pending) return
      pending = true
      setBusy(true)
      try {
        const next = await request<Snapshot>(
          `capture-readiness${query.after ? `?after=${encodeURIComponent(query.after)}` : ""}`,
        )
        if (active) {
          setSnapshot(next)
          setReceivedAt(Date.now())
          setNow(Date.now())
          setError("")
        }
      } catch (e) {
        if (active) setError(e instanceof Error ? e.message : "Could not refresh capture status.")
      } finally {
        pending = false
        if (active) setBusy(false)
      }
    }
    void load()
    const timer = window.setInterval(() => void load(), 10000)
    const clock = window.setInterval(() => setNow(Date.now()), 5000)
    return () => {
      active = false
      window.clearInterval(timer)
      window.clearInterval(clock)
    }
  }, [query])
  const stale = !!error || (!!snapshot && now - receivedAt > snapshot.validForSeconds * 1000)
  const date = (value: string) => new Date(value).toLocaleString(undefined, { timeZone: timezone })
  function changePage(cursor: string) {
    setSnapshot(null)
    setQuery((q) => ({ after: cursor, revision: q.revision + 1 }))
  }
  return (
    <div className="page-content">
      <div className="page-intro">
        <p className="eyebrow">CONNECTIONS</p>
        <h1>Capture readiness</h1>
        <p>Pairing, delivery setup, and the notes Basalisk is looking after.</p>
      </div>
      <Notice error={error} />
      <div className="toolbar">
        <p role="status">
          {snapshot
            ? `${stale ? "Out of date · last known status" : "Local status"} · ${date(snapshot.generatedAt)}`
            : busy
              ? "Loading capture status…"
              : "Capture status unavailable."}
        </p>
        <Button
          onClick={() => setQuery((q) => ({ ...q, revision: q.revision + 1 }))}
          disabled={busy}
        >
          Refresh
        </Button>
      </div>
      <section className="panel stack">
        <h2>Service health is not connected yet</h2>
        <p>
          This view shows Basalisk’s local records and delivery activity. Live service and machine
          health will come from Cerberus. An active worker or a past delivery does not prove FE is
          reachable now.
        </p>
      </section>
      <div className="stack">
        {snapshot?.apps.length === 0 && (
          <section className="panel">
            <h2>No paired apps on this page</h2>
            <p>Pair an app in Devices, then select its FE connection.</p>
          </section>
        )}
        {snapshot?.apps.map((app) => (
          <section key={app.deviceID} className="panel stack">
            <h2>{app.label}</h2>
            <p>{labels[app.authorization] ?? app.authorization}</p>
            <p>
              {app.connection.state === "bound"
                ? `FE connection: ${app.connection.name}`
                : labels[app.connection.state]}
              {" · "}
              {app.contextAllowed ? "Session context permitted" : "Session context not permitted"}
            </p>
            <p>{labels[app.worker.state] ?? "Worker status unknown"}</p>
            {app.worker.observedAt && (
              <p className="text-label text-fg-muted">
                Worker observed {date(app.worker.observedAt)}
              </p>
            )}
            <p>
              {app.queue.queued} queued ({app.queue.delivering} sending) ·{" "}
              {app.queue.needsAttention} need attention · {app.queue.saved} confirmed ·{" "}
              {app.queue.stopped} stopped
            </p>
            {app.latestPending && (
              <p>
                Latest pending update: {app.latestPending.code.replaceAll("_", " ")} ·{" "}
                {date(app.latestPending.updatedAt)}. Review the Fragment outbox for details.
              </p>
            )}
            <p>
              {app.lastConfirmed
                ? `Last confirmed delivery: ${date(app.lastConfirmed.updatedAt)}${app.lastConfirmed.integrationID !== app.connection.integrationID ? " · a previous connection" : ""}`
                : "No confirmed delivery recorded for this app."}
            </p>
          </section>
        ))}
      </div>
      <div className="actions">
        {after && (
          <Button onClick={() => changePage("")} disabled={busy}>
            First page
          </Button>
        )}
        {snapshot?.nextAfter && (
          <Button onClick={() => changePage(snapshot.nextAfter ?? "")} disabled={busy}>
            More apps
          </Button>
        )}
      </div>
    </div>
  )
}
