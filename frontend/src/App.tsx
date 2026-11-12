import { ManifestShell, type PageComponent } from "@hollis-labs/app-shell"
import { Button } from "@hollis-labs/design-components"
import { Cable, History, Home, Inbox, Laptop, LogOut, Shield, User } from "lucide-react"
import { useCallback, useEffect, useMemo, useState } from "react"
import { ApiError, loadAccount, type Profile, request } from "./api/account"
import { Notice } from "./components/forms"
import { manifest } from "./manifest"
import { AccountPage } from "./pages/account"
import { DevicesPage } from "./pages/devices"
import { Entry } from "./pages/entry"
import { HomePage } from "./pages/home"
import { IntegrationsPage } from "./pages/integrations"
import { OutboxPage } from "./pages/outbox"
import { ReadinessPage } from "./pages/readiness"
import { SecurityPage } from "./pages/security"
import { SessionsPage } from "./pages/sessions"
import { SignOutPage } from "./pages/signout"
import { type Session, SessionContext, useSession } from "./session"

// Kit icon names used by the manifest, resolved by the shell.
const icons = {
  home: Home,
  history: History,
  inbox: Inbox,
  cable: Cable,
  laptop: Laptop,
  user: User,
  shield: Shield,
  "log-out": LogOut,
}

// Pages that read the owner's timezone get it from the session.
const withTimezone =
  (Page: (p: { timezone: string }) => React.ReactNode): PageComponent =>
  () => <Page timezone={useSession().profile.timezone} />

function usePath() {
  const [path, setPath] = useState(window.location.pathname)
  useEffect(() => {
    const onPop = () => setPath(window.location.pathname)
    window.addEventListener("popstate", onPop)
    return () => window.removeEventListener("popstate", onPop)
  }, [])
  const navigate = useCallback((to: string) => {
    if (to !== window.location.pathname) window.history.pushState(null, "", to)
    setPath(to)
  }, [])
  return [path, navigate] as const
}

export function App() {
  const [profile, setProfile] = useState<Profile | null>(null)
  const [setup, setSetup] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [path, navigate] = usePath()
  const signedOut = useCallback(() => {
    setProfile(null)
    setSetup(false)
    navigate("/")
  }, [navigate])
  useEffect(() => {
    async function load() {
      try {
        const state = await request<{ setup_required: boolean }>("setup")
        setSetup(state.setup_required)
        if (!state.setup_required) {
          try {
            setProfile(await loadAccount())
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
  }, [])
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

  const pages = useMemo<Record<string, PageComponent>>(
    () => ({
      home: () => <HomePage onNavigate={navigate} />,
      sessions: withTimezone(SessionsPage),
      outbox: () => <OutboxPage />,
      readiness: withTimezone(ReadinessPage),
      devices: withTimezone(DevicesPage),
      account: () => <AccountPage />,
      integrations: withTimezone(IntegrationsPage),
      security: () => <SecurityPage />,
      "sign-out": () => <SignOutPage />,
    }),
    [navigate],
  )
  const session = useMemo<Session | null>(
    () => (profile ? { profile, setProfile, signedOut } : null),
    [profile, signedOut],
  )

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
  if (!session) return <Entry setup={setup} onReady={setProfile} />
  return (
    <SessionContext.Provider value={session}>
      <ManifestShell
        manifest={manifest}
        apiBase="/api"
        path={path}
        onNavigate={navigate}
        pages={pages}
        icons={icons}
      />
    </SessionContext.Provider>
  )
}
