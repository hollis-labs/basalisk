import { AppShell, Button, Input } from "@hollis-labs/design-components"
import { AccountProfile } from "@hollis-labs/kit-account"
import { NavRail, type NavRailItem, PageHeader } from "@hollis-labs/kit-dashboard"
import { type SettingsDraft, type SettingsGroup, SettingsRenderer } from "@hollis-labs/kit-settings"
import {
  Cable,
  History,
  Home,
  Inbox,
  Laptop,
  LogOut,
  Settings,
  Shield,
  User,
  Waypoints,
} from "lucide-react"
import { type FormEvent, useCallback, useEffect, useState } from "react"
import { ApiError, type Profile, request } from "./api/account"
import { Field, Notice } from "./components/forms"
import { DevicesPage } from "./pages/devices"
import { IntegrationsPage } from "./pages/integrations"
import { OutboxPage } from "./pages/outbox"
import { ReadinessPage } from "./pages/readiness"
import { SessionsPage } from "./pages/sessions"

const preferences: SettingsGroup = {
  id: "preferences",
  label: "Preferences",
  schema: {
    type: "object",
    additionalProperties: false,
    required: ["theme", "timezone"],
    properties: {
      theme: { type: "string", title: "Appearance", enum: ["dark", "light", "system"] },
      timezone: {
        type: "string",
        title: "Timezone",
        description: "IANA timezone, such as America/Chicago.",
        minLength: 1,
        maxLength: 100,
      },
    },
  },
  fields: {
    theme: { editable: true, secret: false, restart_required: false },
    timezone: { editable: true, secret: false, restart_required: false },
  },
  capabilities: { can_read: true, can_update: true, can_validate: true, can_reset: false },
}

function Entry({ setup, onReady }: { setup: boolean; onReady: (p: Profile) => void }) {
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
      const profile = await request<Profile>(mode ? "setup" : "login", "POST", {
        username: data.get("username"),
        password: data.get("password"),
        ...(mode ? { displayName: data.get("displayName") } : {}),
      })
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

function AccountPage({ profile, onChange }: { profile: Profile; onChange: (p: Profile) => void }) {
  const [draft, setDraft] = useState({ displayName: profile.displayName, email: profile.email })
  const [settingsDraft, setSettingsDraft] = useState<SettingsDraft>({})
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [message, setMessage] = useState("")
  async function save(path: string, value: object) {
    setBusy(true)
    setError("")
    setMessage("")
    try {
      onChange(await request<Profile>(path, "PUT", { ...value, revision: profile.revision }))
      setSettingsDraft({})
      setMessage("Changes saved.")
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save changes.")
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="page-content">
      <div className="page-intro">
        <p className="eyebrow">ADMINISTRATION</p>
        <h1>Your account</h1>
        <p>Make this workspace feel like yours.</p>
      </div>
      <Notice error={error} message={message} />
      <div className="account-grid">
        <div className="stack">
          <AccountProfile
            value={draft}
            onValueChange={setDraft}
            onSave={(value) => void save("account", value)}
            saving={busy}
          />
          <div className="panel">
            <h2>Workspace owner</h2>
            <p>
              Signed in as <strong>{profile.username}</strong>.
            </p>
            <p className="text-fg-muted">
              This account can manage all of this workspace’s settings and connections.
            </p>
          </div>
        </div>
        <SettingsRenderer
          contractVersion={1}
          groups={[preferences]}
          states={{
            preferences: {
              draft: settingsDraft,
              busy,
              values: {
                theme: { present: true, value: profile.theme, editable: true, has_override: true },
                timezone: {
                  present: true,
                  value: profile.timezone,
                  editable: true,
                  has_override: true,
                },
              },
            },
          }}
          onDraftChange={(_, value) => setSettingsDraft(value)}
          onSave={(_, changes) =>
            void save("preferences", {
              theme: profile.theme,
              timezone: profile.timezone,
              ...changes.set,
            })
          }
        />
      </div>
    </div>
  )
}

function SecurityPage({ onSignedOut }: { onSignedOut: () => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = event.currentTarget
    const data = new FormData(form)
    if (data.get("new") !== data.get("confirmation")) {
      setError("Passwords do not match.")
      return
    }
    setBusy(true)
    setError("")
    try {
      await request("password", "PUT", {
        currentPassword: data.get("current"),
        newPassword: data.get("new"),
      })
      form.reset()
      onSignedOut()
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
        <h1>Security</h1>
        <p>Keep access to your workspace in your hands.</p>
      </div>
      <form className="panel stack form-width" onSubmit={submit} aria-busy={busy}>
        <h2>Change password</h2>
        <p className="text-fg-muted">
          Changing your password signs out every session, including this one.
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
          <Notice error={error} />
          <Button type="submit" disabled={busy}>
            {busy ? "Updating…" : "Update password and sign out"}
          </Button>
        </fieldset>
      </form>
    </div>
  )
}

export function App() {
  const [profile, setProfile] = useState<Profile | null>(null)
  const [setup, setSetup] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [route, setRoute] = useState("home")
  const updateProfile = useCallback((p: Profile) => {
    setProfile(p)
  }, [])
  const signedOut = useCallback(() => {
    setProfile(null)
    setSetup(false)
    setRoute("home")
  }, [])
  useEffect(() => {
    async function load() {
      try {
        const state = await request<{ setupRequired: boolean }>("setup")
        setSetup(state.setupRequired)
        if (!state.setupRequired) {
          try {
            updateProfile(await request<Profile>("account"))
          } catch (e) {
            if (!(e instanceof ApiError && e.status === 401)) throw e
          }
        }
      } catch (e) {
        setError(e instanceof Error ? e.message : "Could not reach Basalisk.")
      } finally {
        setLoading(false)
      }
    }
    void load()
  }, [updateProfile])
  useEffect(() => {
    if (!profile) return
    const media = window.matchMedia("(prefers-color-scheme: dark)")
    const apply = () =>
      document.documentElement.setAttribute(
        "data-mode",
        profile.theme === "system" ? (media.matches ? "dark" : "light") : profile.theme,
      )
    apply()
    media.addEventListener("change", apply)
    return () => media.removeEventListener("change", apply)
  }, [profile])
  useEffect(() => {
    window.addEventListener("basalisk:unauthorized", signedOut)
    return () => window.removeEventListener("basalisk:unauthorized", signedOut)
  }, [signedOut])
  async function logout() {
    try {
      await request("logout", "POST", {})
      signedOut()
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) signedOut()
      else setError("Could not sign out. Please try again.")
    }
  }
  if (loading)
    return (
      <main className="entry-loading" role="status">
        Opening Basalisk…
      </main>
    )
  if (error && !profile)
    return (
      <main className="entry-loading">
        <Notice error={error} />
        <Button onClick={() => window.location.reload()}>Try again</Button>
      </main>
    )
  if (!profile) return <Entry setup={setup} onReady={updateProfile} />
  const nav: NavRailItem[] = [
    { key: "home", label: "Home", icon: <Home className="h-4 w-4" /> },
    { key: "sessions", label: "Work sessions", icon: <History className="h-4 w-4" /> },
    { key: "outbox", label: "Fragment outbox", icon: <Inbox className="h-4 w-4" /> },
    { key: "readiness", label: "Capture readiness", icon: <Cable className="h-4 w-4" /> },
    { key: "devices", label: "Devices", icon: <Laptop className="h-4 w-4" /> },
    { key: "account", label: "Account & preferences", icon: <User className="h-4 w-4" /> },
    { key: "integrations", label: "Integrations & keys", icon: <Cable className="h-4 w-4" /> },
    { key: "security", label: "Security", icon: <Shield className="h-4 w-4" /> },
  ].map((item) => ({
    ...item,
    active: route === item.key,
    onSelect: () => {
      setRoute(item.key)
      setError("")
    },
  }))
  const titles: Record<string, string> = {
    home: "Workspace",
    sessions: "Work sessions",
    devices: "Devices",
    outbox: "Fragment outbox",
    readiness: "Capture readiness",
    account: "Account & preferences",
    integrations: "Integrations & keys",
    security: "Security",
  }
  return (
    <AppShell
      nav={
        <NavRail
          items={nav}
          logo={<Waypoints className="h-4 w-4" />}
          logoLabel="Basalisk"
          footerExtra={
            <Button variant="ghost" size="icon" aria-label="Sign out" onClick={() => void logout()}>
              <LogOut className="h-4 w-4" />
            </Button>
          }
        />
      }
      header={<PageHeader title={titles[route]} />}
    >
      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-auto">
        <Notice error={error} />
        {route === "home" && (
          <div className="page-content">
            <div className="page-intro">
              <p className="eyebrow">YOUR WORKSPACE</p>
              <h1>Welcome, {profile.displayName}.</h1>
              <p>A home for the things you want to build, connect, and keep.</p>
            </div>
            <div className="welcome-panel">
              <Waypoints aria-hidden="true" className="h-8 w-8 text-primary" />
              <h2>Start with your foundations.</h2>
              <p>
                Your workspace is ready. Personalize your account, then add the services your tools
                will use.
              </p>
            </div>
            <div className="home-grid">
              <button
                type="button"
                className="panel destination"
                onClick={() => setRoute("sessions")}
              >
                <History aria-hidden="true" />
                <h2>Follow your work</h2>
                <p>Start a session and keep your context together.</p>
                <span>Open work sessions →</span>
              </button>
              <button
                type="button"
                className="panel destination"
                onClick={() => setRoute("account")}
              >
                <Settings aria-hidden="true" />
                <h2>Make it yours</h2>
                <p>Profile, appearance, and timezone.</p>
                <span>Open account →</span>
              </button>
              <button
                type="button"
                className="panel destination"
                onClick={() => setRoute("integrations")}
              >
                <Cable aria-hidden="true" />
                <h2>Connect your services</h2>
                <p>Keep API credentials in one place.</p>
                <span>Manage integrations →</span>
              </button>
            </div>
          </div>
        )}
        {route === "account" && <AccountPage profile={profile} onChange={updateProfile} />}
        {route === "sessions" && <SessionsPage timezone={profile.timezone} />}
        {route === "outbox" && <OutboxPage />}
        {route === "readiness" && <ReadinessPage timezone={profile.timezone} />}
        {route === "devices" && <DevicesPage timezone={profile.timezone} />}
        {route === "integrations" && <IntegrationsPage timezone={profile.timezone} />}
        {route === "security" && <SecurityPage onSignedOut={signedOut} />}
      </div>
    </AppShell>
  )
}
