import { createContext, useContext } from "react"
import type { DeskAPI } from "./registry"

export const DeskContext = createContext<DeskAPI | null>(null)

/** The desktop, for anything rendered inside it. */
export function useDesk(): DeskAPI {
  const desk = useContext(DeskContext)
  if (!desk) throw new Error("useDesk outside the desktop")
  return desk
}
