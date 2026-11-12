import { Button } from "@hollis-labs/design-components"
import { useState } from "react"
import { ApiError, request } from "../api/account"
import { Notice } from "../components/forms"
import { useSession } from "../session"

/** The shell has no user-menu region, so signing out is a (footer) page with one button. */
export function SignOutPage() {
  const { signedOut } = useSession()
  const [error, setError] = useState("")
  async function logout() {
    try {
      await request("logout", "POST", {})
      signedOut()
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) signedOut()
      else setError("Could not sign out. Please try again.")
    }
  }
  return (
    <div className="page-content">
      <div className="panel stack form-width">
        <h2>Sign out of Basalisk</h2>
        <p className="text-fg-muted">You will need your password to come back.</p>
        <Notice error={error} />
        <Button onClick={() => void logout()}>Sign out</Button>
      </div>
    </div>
  )
}
