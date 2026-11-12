import { Button, Input } from "@hollis-labs/design-components"
import { type FormEvent, useState } from "react"
import { request } from "../api/account"
import { Field, Notice } from "../components/forms"

export function SecurityPage() {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [message, setMessage] = useState("")
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = event.currentTarget
    const data = new FormData(form)
    setMessage("")
    if (data.get("new") !== data.get("confirmation")) {
      setError("Passwords do not match.")
      return
    }
    setBusy(true)
    setError("")
    try {
      await request("password", "PUT", {
        current_password: data.get("current"),
        new_password: data.get("new"),
      })
      form.reset()
      setMessage("Password updated. Your other sessions were signed out.")
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not change password.")
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="page-content">
      <div className="page-intro">
        <p className="eyebrow">ADMINISTRATION</p>
        <h2>Security</h2>
        <p>Keep access to your workspace in your hands.</p>
      </div>
      <form className="panel stack form-width" onSubmit={submit} aria-busy={busy}>
        <h2>Change password</h2>
        <p className="text-fg-muted">
          Changing your password signs out every other session. This one stays signed in.
        </p>
        <fieldset disabled={busy} className="stack">
          <Field label="Current password">
            {(id) => (
              <Input
                id={id}
                name="current"
                type="password"
                autoComplete="current-password"
                required
              />
            )}
          </Field>
          <Field label="New password">
            {(id) => (
              <Input
                id={id}
                name="new"
                type="password"
                autoComplete="new-password"
                minLength={12}
                maxLength={72}
                required
              />
            )}
          </Field>
          <Field label="Confirm new password">
            {(id) => (
              <Input
                id={id}
                name="confirmation"
                type="password"
                autoComplete="new-password"
                minLength={12}
                maxLength={72}
                required
              />
            )}
          </Field>
          <Notice error={error} message={message} />
          <Button type="submit" disabled={busy}>
            {busy ? "Updating…" : "Update password"}
          </Button>
        </fieldset>
      </form>
    </div>
  )
}
