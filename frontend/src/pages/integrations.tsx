import { Button, Input } from "@hollis-labs/design-components"
import { Cable, KeyRound, Plus } from "lucide-react"
import { type FormEvent, useCallback, useEffect, useState } from "react"
import { type Integration, request } from "../api/account"
import { Field, Notice } from "../components/forms"

export function IntegrationsPage({ timezone }: { timezone: string }) {
  const [items, setItems] = useState<Integration[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [message, setMessage] = useState("")
  const [adding, setAdding] = useState(false)
  const [rotating, setRotating] = useState<string | null>(null)
  const [removing, setRemoving] = useState<string | null>(null)
  const refresh = useCallback(async () => {
    setItems(await request<Integration[]>("integrations"))
  }, [])
  useEffect(() => {
    refresh()
      .catch(() => setError("Could not load integrations. Reload to try again."))
      .finally(() => setLoading(false))
  }, [refresh])
  async function add(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = event.currentTarget
    const data = new FormData(form)
    setBusy(true)
    setError("")
    setMessage("")
    try {
      await request("integrations", "POST", Object.fromEntries(data))
      form.reset()
      setAdding(false)
      await refresh()
      setMessage("Integration saved. Its API key is stored and hidden.")
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save integration.")
    } finally {
      setBusy(false)
    }
  }
  async function rotate(event: FormEvent<HTMLFormElement>, id: string) {
    event.preventDefault()
    const form = event.currentTarget
    setBusy(true)
    setError("")
    setMessage("")
    try {
      await request(`integrations/${id}/credential`, "PUT", {
        credential: new FormData(form).get("credential"),
      })
      form.reset()
      setRotating(null)
      await refresh()
      setMessage("Stored API key replaced.")
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not replace API key.")
    } finally {
      setBusy(false)
    }
  }
  async function remove(id: string) {
    setBusy(true)
    setError("")
    setMessage("")
    try {
      await request(`integrations/${id}`, "DELETE")
      setRemoving(null)
      await refresh()
      setMessage("Integration and its stored credential removed.")
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not remove integration.")
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="page-content">
      <div className="page-intro">
        <p className="eyebrow">CONNECTIONS</p>
        <h1>Integrations & keys</h1>
        <p>The services that power your personal tools.</p>
      </div>
      <div className="toolbar">
        <p className="text-fg-muted">
          API keys are hidden after saving. Connections are not tested yet.
        </p>
        <Button
          disabled={busy || adding}
          onClick={() => {
            setAdding(true)
            setError("")
            setMessage("")
          }}
        >
          <Plus className="h-4 w-4" aria-hidden="true" /> Add integration
        </Button>
      </div>
      <Notice error={error} message={message} />
      {adding && (
        <form className="panel stack form-width" onSubmit={add} aria-busy={busy}>
          <h2>Add an integration</h2>
          <fieldset className="stack" disabled={busy}>
            <Field label="Name">
              {(id) => (
                <Input
                  id={id}
                  name="name"
                  placeholder="My personal tools"
                  required
                  maxLength={100}
                  autoFocus
                />
              )}
            </Field>
            <Field label="Service / provider">
              {(id) => (
                <Input
                  id={id}
                  name="provider"
                  placeholder="e.g. GitHub, Tesseract, or another service"
                  required
                  maxLength={100}
                />
              )}
            </Field>
            <Field label="API base URL (optional)">
              {(id) => (
                <Input
                  id={id}
                  name="baseUrl"
                  type="url"
                  placeholder="https://api.example.com"
                  maxLength={1000}
                />
              )}
            </Field>
            <Field label="API key">
              {(id) => (
                <Input
                  id={id}
                  name="credential"
                  type="password"
                  autoComplete="off"
                  required
                  maxLength={8192}
                />
              )}
            </Field>
            <p className="text-label text-fg-muted">
              Stored privately on this machine. Saving does not contact the service.
            </p>
            <div className="actions">
              <Button type="submit" disabled={busy}>
                {busy ? "Saving…" : "Save integration"}
              </Button>
              <Button
                type="button"
                variant="ghost"
                onClick={() => setAdding(false)}
                disabled={busy}
              >
                Cancel
              </Button>
            </div>
          </fieldset>
        </form>
      )}
      {loading ? (
        <p role="status">Loading integrations…</p>
      ) : items.length === 0 && !adding ? (
        <div className="empty panel">
          <Cable className="h-8 w-8 text-fg-muted" aria-hidden="true" />
          <h2>Your connections start here.</h2>
          <p>
            Add an API key for a service you want to use.
            <br />
            Your personal tools can build on these connections.
          </p>
        </div>
      ) : (
        <div className="integration-list">
          {items.map((item) => (
            <article className="panel integration" key={item.id}>
              <div className="integration-heading">
                <div className="service-icon">
                  <KeyRound aria-hidden="true" className="h-5 w-5" />
                </div>
                <div className="min-w-0">
                  <h2>{item.name}</h2>
                  <p>{item.provider}</p>
                </div>
                <span className="credential-status">
                  {item.credentialPresent ? "Key stored" : "Key missing"}
                </span>
              </div>
              {item.baseUrl && <p className="integration-url">{item.baseUrl}</p>}
              <p className="text-label text-fg-muted">
                Updated {new Date(item.updatedAt).toLocaleString(undefined, { timeZone: timezone })}{" "}
                · Not tested
              </p>
              <div className="actions">
                <Button
                  variant="outline"
                  disabled={busy}
                  onClick={() => {
                    setRotating(item.id)
                    setRemoving(null)
                    setError("")
                  }}
                >
                  Replace key
                </Button>
                <Button
                  variant="ghost"
                  disabled={busy}
                  onClick={() => {
                    setRemoving(item.id)
                    setRotating(null)
                    setError("")
                  }}
                >
                  Remove
                </Button>
              </div>
              {rotating === item.id && (
                <form
                  className="stack inline-panel"
                  onSubmit={(event) => void rotate(event, item.id)}
                >
                  <fieldset className="stack" disabled={busy}>
                    <Field label={`New API key for ${item.name}`}>
                      {(id) => (
                        <Input
                          id={id}
                          name="credential"
                          type="password"
                          autoComplete="off"
                          maxLength={8192}
                          required
                          autoFocus
                        />
                      )}
                    </Field>
                    <p className="text-label text-fg-muted">
                      This replaces Basalisk’s stored key. It does not revoke the old key at the
                      provider.
                    </p>
                    <div className="actions">
                      <Button type="submit" disabled={busy}>
                        Save replacement
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        disabled={busy}
                        onClick={() => setRotating(null)}
                      >
                        Cancel
                      </Button>
                    </div>
                  </fieldset>
                </form>
              )}
              {removing === item.id && (
                <div className="inline-panel stack">
                  <p>
                    Remove <strong>{item.name}</strong> and its stored credential? This does not
                    revoke the key at the provider.
                  </p>
                  <div className="actions">
                    <Button
                      variant="destructive"
                      disabled={busy}
                      onClick={() => void remove(item.id)}
                    >
                      Remove integration
                    </Button>
                    <Button variant="ghost" disabled={busy} onClick={() => setRemoving(null)}>
                      Keep integration
                    </Button>
                  </div>
                </div>
              )}
            </article>
          ))}
        </div>
      )}
    </div>
  )
}
