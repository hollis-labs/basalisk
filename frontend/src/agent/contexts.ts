/**
 * How the built-in apps describe their windows (registry.ts, registerContext). They live
 * here for now, beside the agent that first needed them; each can move into its app.
 */
import { request } from "../api/account"
import { nativeBrowser } from "../api/browser"
import { filesLocations } from "../apps/files/model"
import { registerContext } from "../desktop/registry"

// Files: the folder the window is showing (it changes as you navigate, so it is not in the args).
registerContext("files", (win) => {
  const loc = filesLocations.get(win.id)
  return loc ? { file: loc } : {}
})

// Terminal: the folder the active shell was started in.
registerContext("terminal", async () => {
  const st = await request<{ sessions: { id: string; cwd: string; exited: boolean }[] }>("terminal")
  const active = window.localStorage.getItem("basalisk.terminal.active")
  const cwd = st.sessions.find((s) => s.id === active && !s.exited)?.cwd
  return cwd ? { cwd } : {}
})

// Browser: the page and what is selected on it. The page lives in a native view, so the
// selection is asked for through the view, not read from this document.
registerContext("browser", async (win) => {
  if (!nativeBrowser.available()) return {}
  try {
    const state = await nativeBrowser.send<{ url?: string; title?: string }>({
      op: "state",
      id: win.id,
    })
    const picked = await nativeBrowser.send<{ result?: unknown }>({
      op: "eval",
      id: win.id,
      js: "String(window.getSelection ? window.getSelection() : '').slice(0, 8000)",
    })
    const selection =
      typeof picked?.result === "string" && picked.result ? picked.result : undefined
    return {
      url: state?.url || undefined,
      extra: state?.title ? { pageTitle: state.title } : undefined,
      selection,
    }
  } catch {
    return {}
  }
})
