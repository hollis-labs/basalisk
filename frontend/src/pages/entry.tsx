import { Button, Input } from "@hollis-labs/design-components"
import { Shield, Waypoints } from "lucide-react"
import { type FormEvent, useState } from "react"
import { ApiError, loadAccount, type Profile, request } from "../api/account"
import { Field, Notice } from "../components/forms"

/** First-run setup (when no owner exists yet) or sign-in. Both start a session cookie. */
export function Entry({ setup, onReady }: { setup: boolean; onReady: (p: Profile) => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [mode, setMode] = useState(setup)
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = event.currentTarget
    const data = new FormData(form)
    if (mode && data.get("password") !== data.get("confirmation")) {
      setError("Passwords do not match.")
      return
    }
    setBusy(true)
    setError("")
    try {
      await request(mode ? "setup" : "login", "POST", {
        username: data.get("username"),
        password: data.get("password"),
        ...(mode ? { display_name: data.get("displayName") } : {}),
      })
      const profile = await loadAccount()
      form.reset()
      onReady(profile)
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) setMode(false)
      setError(e instanceof Error ? e.message : "Could not sign in.")
    } finally {
      setBusy(false)
    }
  }
  return (
    <main className="entry">
      <div className="entry-story">
        <div className="brand">
          <Waypoints aria-hidden="true" /> BASALISK
        </div>
        <p className="eyebrow">YOUR PERSONAL OPERATING SYSTEM</p>
        <h1>
          A place for
          <br />
          your own world.
        </h1>
        <p className="intro">
          Your data, your tools, your connections.
          <br />
          Start with a space that belongs to you.
        </p>
        <div className="entry-foot">
          <Shield aria-hidden="true" className="h-4 w-4" /> One owner. A private workspace.
        </div>
      </div>
      <section className="entry-panel">
        <p className="eyebrow">{mode ? "01 / MAKE IT YOURS" : "WELCOME BACK"}</p>
        <h2>{mode ? "Create your account" : "Sign in to Basalisk"}</h2>
        <p className="text-fg-muted">
          {mode ? "Set up the owner account for this workspace." : "Pick up where you left off."}
        </p>
        <form onSubmit={submit} className="stack" aria-busy={busy}>
          <fieldset disabled={busy} className="stack">
            {mode && (
              <Field label="Display name">
                {(id) => (
                  <Input
                    id={id}
                    name="displayName"
                    autoComplete="name"
                    required
                    maxLength={100}
                    autoFocus
                  />
                )}
              </Field>
            )}
            <Field label="Username">
              {(id) => (
                <Input id={id} name="username" autoComplete="username" required maxLength={64} />
              )}
            </Field>
            <Field label="Password">
              {(id) => (
                <Input
                  id={id}
                  name="password"
                  type="password"
                  autoComplete={mode ? "new-password" : "current-password"}
                  required
                  minLength={mode ? 12 : undefined}
                  maxLength={72}
                />
              )}
            </Field>
            {mode && (
              <>
                <p className="text-label text-fg-muted">
                  Use at least 12 characters. A memorable passphrase works well.
                </p>
                <Field label="Confirm password">
                  {(id) => (
                    <Input
                      id={id}
                      name="confirmation"
                      type="password"
                      autoComplete="new-password"
                      required
                      minLength={12}
                      maxLength={72}
                    />
                  )}
                </Field>
              </>
            )}
            <Notice error={error} />
            <Button type="submit" disabled={busy}>
              {busy ? "Please wait…" : mode ? "Create my workspace" : "Sign in"}
            </Button>
          </fieldset>
        </form>
        <p className="entry-note">
          {mode
            ? "This account administers your profile, preferences, and connected services."
            : "Your credentials stay with this Basalisk instance."}
        </p>
      </section>
    </main>
  )
}
