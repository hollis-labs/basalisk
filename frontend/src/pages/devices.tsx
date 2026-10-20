import { Button, Input } from "@hollis-labs/design-components"
import { type FormEvent, useCallback, useEffect, useState } from "react"
import { type Integration, request } from "../api/account"
import { Field, Notice } from "../components/forms"

interface Device {
  id: string
  label: string
  scopes: string[]
  expires_at: string
  revoked_at?: string
}
interface Grant {
  id: string
  code: string
  expires_at: string
}
interface Observation {
  id: string
  deviceId: string
  occurredAt: string
  receivedAt: string
  kind: string
  bundleId: string
  appName: string
  activitySessionId: string
  contextId: string
}
interface FragmentConnection {
  deviceId: string
  integrationId: string
  name: string
}

export function DevicesPage({ timezone }: { timezone: string }) {
  const [devices, setDevices] = useState<Device[]>([])
  const [observations, setObservations] = useState<Observation[]>([])
  const [grant, setGrant] = useState<Grant | null>(null)
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [confirm, setConfirm] = useState("")
  const [loading, setLoading] = useState(true)
  const [integrations, setIntegrations] = useState<Integration[]>([])
  const [connections, setConnections] = useState<FragmentConnection[]>([])
  const [pairingProfile, setPairingProfile] = useState("companion")
  const refresh = useCallback(async () => {
    const [nextDevices, nextObservations, nextIntegrations, nextConnections] = await Promise.all([
      request<Device[]>("devices"),
      request<Observation[]>("observations"),
      request<Integration[]>("integrations"),
      request<FragmentConnection[]>("fragment-connections"),
    ])
    setDevices(nextDevices)
    setObservations(nextObservations)
    setIntegrations(nextIntegrations.filter((i) => i.provider === "fragments-engine"))
    setConnections(nextConnections)
  }, [])
  useEffect(() => {
    void refresh()
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false))
    const interval = window.setInterval(() => {
      void refresh().catch(() => {})
    }, 10000)
    return () => window.clearInterval(interval)
  }, [refresh])
  useEffect(() => {
    if (!grant) return
    const timer = window.setTimeout(
      () => setGrant(null),
      Math.max(0, Date.parse(grant.expires_at) - Date.now()),
    )
    return () => window.clearTimeout(timer)
  }, [grant])
  async function create(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = event.currentTarget
    const data = new FormData(form)
    const label = data.get("label")
    setBusy(true)
    setError("")
    try {
      if (grant) await request(`devices/grants/${grant.id}`, "DELETE")
      setGrant(null)
      setGrant(
        await request<Grant>("devices/grants", "POST", {
          label,
          profile: pairingProfile,
          allowFragments: data.get("allowFragments") === "on",
        }),
      )
      form.reset()
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not create code.")
    } finally {
      setBusy(false)
    }
  }
  async function connectFragment(event: FormEvent<HTMLFormElement>, deviceId: string) {
    event.preventDefault()
    const integrationId = new FormData(event.currentTarget).get("integrationId")
    setBusy(true)
    setError("")
    try {
      await request(
        `fragment-connections/${deviceId}`,
        integrationId ? "PUT" : "DELETE",
        integrationId ? { integrationId } : undefined,
      )
      await refresh()
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not update fragment access.")
    } finally {
      setBusy(false)
    }
  }
  async function revoke(id: string, isGrant = false) {
    setBusy(true)
    setError("")
    try {
      await request(`devices/${isGrant ? "grants/" : ""}${id}`, "DELETE")
      if (isGrant) setGrant(null)
      setConfirm("")
      await refresh()
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not revoke access.")
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="page-content">
      <div className="page-intro">
        <p className="eyebrow">YOUR DEVICES</p>
        <h1>Apps &amp; devices</h1>
        <p>Connect your Mac companion and capture apps to this workspace.</p>
      </div>
      <Notice error={error} />
      <div className="account-grid">
        <section className="panel stack">
          <h2>Pair an app or device</h2>
          <p>Create a one-use code, then enter it in the app you want to connect.</p>
          <Field label="Server address for this connection">
            {(id) => <Input id={id} value={window.location.origin} readOnly />}
          </Field>
          <p className="text-label text-fg-muted">
            For a remote host, keep your SSH tunnel open and use its local address. Capture apps
            receive their own credentials. Choose their Fragments Engine connection below after
            pairing.
          </p>
          <form onSubmit={create}>
            <fieldset className="stack" disabled={busy}>
              <Field label="Application type">
                {(id) => (
                  <select
                    id={id}
                    value={pairingProfile}
                    onChange={(event) => setPairingProfile(event.target.value)}
                  >
                    <option value="companion">Mac activity companion</option>
                    <option value="capture">Capture app (Nil)</option>
                  </select>
                )}
              </Field>
              <p className="text-label text-fg-muted">
                {pairingProfile === "capture"
                  ? "Read work-session context and submit fragments. No permission to publish focus observations."
                  : "Read work-session context and publish Mac focus observations."}
              </p>
              <Field label="App or device name">
                {(id) => (
                  <Input id={id} name="label" required maxLength={128} placeholder="My MacBook" />
                )}
              </Field>
              {pairingProfile === "companion" && (
                <label className="text-label">
                  <input type="checkbox" name="allowFragments" /> Allow this Mac to create fragments
                  through a selected integration
                </label>
              )}
              <Button type="submit">Create pairing code</Button>
            </fieldset>
          </form>
          {grant && (
            <div className="inline-panel stack">
              <Field label="One-use pairing code">
                {(id) => <Input id={id} value={grant.code} readOnly autoComplete="off" />}
              </Field>
              <p className="text-label text-fg-muted">
                Expires{" "}
                {new Date(grant.expires_at).toLocaleTimeString(undefined, { timeZone: timezone })}.
                The code is shown only here and disappears when you leave this screen.
              </p>
              <Button variant="outline" disabled={busy} onClick={() => void revoke(grant.id, true)}>
                Revoke unused code
              </Button>
            </div>
          )}
          <p className="text-label text-fg-muted">
            Capture starts paused in the companion. Start a work session, then enable capture on the
            Mac. App exclusions and pause controls stay in the companion.
          </p>
        </section>
        <section className="panel stack">
          <h2>Paired apps &amp; devices</h2>
          {loading && <p role="status">Loading devices…</p>}
          {!loading && devices.length === 0 && (
            <p className="text-fg-muted">No paired devices yet.</p>
          )}
          {devices.map((device) => (
            <div key={device.id} className="inline-panel stack">
              <h3>{device.label}</h3>
              <p className="text-label text-fg-muted">
                {device.scopes.includes("basalisk.observations.write")
                  ? "Activity observations"
                  : "No observation access"}
                {device.scopes.includes("basalisk.fragments.create") ? " · Fragment capture" : ""}
              </p>
              <p className="text-label text-fg-muted">
                {device.revoked_at
                  ? "Revoked"
                  : Date.parse(device.expires_at) <= Date.now()
                    ? "Expired"
                    : `Access expires ${new Date(device.expires_at).toLocaleDateString(undefined, { timeZone: timezone })}`}
              </p>
              {!device.revoked_at && device.scopes.includes("basalisk.fragments.create") && (
                <form onSubmit={(event) => void connectFragment(event, device.id)}>
                  <fieldset className="stack" disabled={busy}>
                    <Field label="Fragments Engine connection">
                      {(id) => (
                        <select
                          id={id}
                          name="integrationId"
                          key={
                            connections.find((c) => c.deviceId === device.id)?.integrationId ??
                            "none"
                          }
                          defaultValue={
                            connections.find((c) => c.deviceId === device.id)?.integrationId ?? ""
                          }
                        >
                          <option value="">No access</option>
                          {integrations.map((i) => (
                            <option key={i.id} value={i.id}>
                              {i.name}
                            </option>
                          ))}
                        </select>
                      )}
                    </Field>
                    <p className="text-label text-fg-muted">
                      Choose a fragments-engine entry from Integrations &amp; keys. The credential
                      stays on the Basalisk host.
                    </p>
                    <Button variant="outline" type="submit">
                      Save fragment access
                    </Button>
                  </fieldset>
                </form>
              )}
              {!device.revoked_at &&
                (confirm === device.id ? (
                  <div className="stack">
                    <p>Revoke this connection’s access? It will need a new pairing code.</p>
                    <div className="actions">
                      <Button disabled={busy} onClick={() => void revoke(device.id)}>
                        Confirm revoke
                      </Button>
                      <Button variant="ghost" disabled={busy} onClick={() => setConfirm("")}>
                        Cancel
                      </Button>
                    </div>
                  </div>
                ) : (
                  <Button variant="outline" disabled={busy} onClick={() => setConfirm(device.id)}>
                    Revoke {device.label}
                  </Button>
                ))}
            </div>
          ))}
        </section>
      </div>
      <section className="panel stack" style={{ marginTop: "1.5rem" }}>
        <h2>Recent Mac activity</h2>
        <p className="text-fg-muted">
          Device-reported app focus and sleep/wake events from the last five minutes. Only
          observations matching an active session’s context are retained. Updates every ten seconds.
        </p>
        {observations.length === 0 && (
          <p>No recent observations. Pair your Mac, start a work session, and enable capture.</p>
        )}
        <ol className="activity-timeline">
          {observations.map((event) => (
            <li key={event.id}>
              <h3>{event.appName || event.kind}</h3>
              <p>{event.bundleId}</p>
              <p className="text-label text-fg-muted">
                {new Date(event.occurredAt).toLocaleTimeString(undefined, { timeZone: timezone })} ·{" "}
                {devices.find((d) => d.id === event.deviceId)?.label ?? "Paired Mac"}
              </p>
              <p className="activity-id text-label text-fg-muted">
                Session: {event.activitySessionId}
              </p>
            </li>
          ))}
        </ol>
      </section>
    </div>
  )
}
