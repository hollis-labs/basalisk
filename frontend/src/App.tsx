import { Button } from "@hollis-labs/design-components"
import { useCallback, useEffect, useMemo, useState } from "react"
import { ApiError, loadAccount, type Profile, request } from "./api/account"
import { Notice } from "./components/forms"
import { Desktop } from "./desktop/desktop"
import { applyDeskTheme, storedTheme } from "./desktop/themes"
import { Entry } from "./pages/entry"
import { type Session, SessionContext } from "./session"
import { TerminalHost } from "./terminal/host"

// The desktop theme owns the palette, before first paint and from then on.
applyDeskTheme(storedTheme())

export function App() {
  const [profile, setProfile] = useState<Profile | null>(null)
  const [setup, setSetup] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const signedOut = useCallback(() => {
    setProfile(null)
    setSetup(false)
  }, [])
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
    window.addEventListener("basalisk:unauthorized", signedOut)
    return () => window.removeEventListener("basalisk:unauthorized", signedOut)
  }, [signedOut])

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
  // The desktop is the whole app once signed in: a bar, a canvas and windows. The terminal
  // host wraps it so one terminal deck can move between its window and the drop-down.
  return (
    <SessionContext.Provider value={session}>
      <TerminalHost>
        <Desktop />
      </TerminalHost>
    </SessionContext.Provider>
  )
}
