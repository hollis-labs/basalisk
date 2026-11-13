import { ManifestShell, type PageComponent } from "@hollis-labs/app-shell"
import { Button } from "@hollis-labs/design-components"
import {
  Cable,
  History,
  Home,
  Inbox,
  Laptop,
  LogOut,
  Monitor,
  Shield,
  SquareTerminal,
  User,
} from "lucide-react"
import { useCallback, useEffect, useMemo, useState } from "react"
import { ApiError, loadAccount, type Profile, request } from "./api/account"
import { SHELL_EVENT, shell } from "./api/shell"
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
import { ShellPage } from "./pages/shell"
import { SignOutPage } from "./pages/signout"
import { TerminalPage } from "./pages/terminal"
import { type Session, SessionContext, useSession } from "./session"
import { TerminalHost } from "./terminal/host"

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
  terminal: SquareTerminal,
  monitor: Monitor,
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

/**
 * In the desktop app's kiosk mode the window covers the whole display, including the strip
 * beside the camera housing. The layout is pushed below that strip while the menu bar is
 * hidden (see html.kiosk in index.css).
 */
function useKioskChrome(signedIn: boolean) {
  useEffect(() => {
    if (!signedIn) return
    const root = document.documentElement
    const apply = async () => {
      try {
        const s = await shell.status()
        const inset = s.available && s.kiosk && !s.menu_bar_visible ? s.screen.safe_top : 0
        root.classList.toggle("kiosk", s.available && s.kiosk)
        root.style.setProperty("--shell-safe-top", `${inset}px`)
      } catch {
        root.classList.remove("kiosk")
        root.style.setProperty("--shell-safe-top", "0px")
      }
    }
    void apply()
    const onShell = () => void apply()
    window.addEventListener(SHELL_EVENT, onShell)
    return () => window.removeEventListener(SHELL_EVENT, onShell)
  }, [signedIn])
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
  useKioskChrome(!!profile)
  useEffect(() => {
    window.addEventListener("basalisk:unauthorized", signedOut)
    return () => window.removeEventListener("basalisk:unauthorized", signedOut)
  }, [signedOut])

  const pages = useMemo<Record<string, PageComponent>>(
    () => ({
      home: () => <HomePage onNavigate={navigate} />,
      terminal: () => <TerminalPage />,
      shell: () => <ShellPage />,
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
      <TerminalHost>
        <ManifestShell
          manifest={manifest}
          apiBase="/api"
          path={path}
          onNavigate={navigate}
          pages={pages}
          icons={icons}
        />
      </TerminalHost>
    </SessionContext.Provider>
  )
}
