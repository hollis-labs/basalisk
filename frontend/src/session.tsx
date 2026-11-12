import { createContext, useContext } from "react"
import type { Profile } from "./api/account"

export interface Session {
  profile: Profile
  /** Replace the signed-in profile after a save. */
  setProfile: (p: Profile) => void
  /** Drop back to the sign-in screen. */
  signedOut: () => void
}

export const SessionContext = createContext<Session | null>(null)

export function useSession(): Session {
  const s = useContext(SessionContext)
  if (!s) throw new Error("Session context missing")
  return s
}
