/**
 * The agent: one assistant beside the windows (docs/agent.md). It registers a side panel,
 * the command that summons it, and the desktop's tools (tools.ts).
 */
import { Sparkles } from "lucide-react"
import { nativeBrowser } from "../api/browser"
import { type DeskAPI, registerCommands, registerPanel } from "../desktop/registry"
import { AgentPanel, FOCUS_EVENT } from "./panel"
import { noteContext, start } from "./session"
import "./contexts"
import "./tools"

registerPanel({
  id: "agent",
  title: "Agent",
  width: 400,
  render: ({ close }) => <AgentPanel close={close} />,
})

/** Open the panel and give it the keyboard, noting first what the owner was looking at. */
async function summon(desk: DeskAPI) {
  await start(desk)
  await noteContext()
  // A page in a native view holds the keyboard until it is asked to let go.
  const st = desk.wmState()
  const focused = st.wins.find((w) => w.id === st.focus)
  if (focused?.app === "browser" && nativeBrowser.available())
    await nativeBrowser.send({ op: "blur", id: focused.id }).catch(() => undefined)
  if (desk.panelOpen("agent")) window.dispatchEvent(new Event(FOCUS_EVENT))
  else desk.togglePanel("agent", true)
}

registerCommands([
  {
    id: "agent.toggle",
    title: "Agent: open or close the panel",
    section: "Agent",
    icon: Sparkles,
    keywords: "ai assistant chat ask claude model",
    keys: "cmd+j",
    run: (desk) => {
      const inPanel = !!document.activeElement?.closest(".desk-panel")
      if (desk.panelOpen("agent") && inPanel) desk.togglePanel("agent", false)
      else void summon(desk)
    },
  },
  {
    id: "agent.ask",
    title: "Agent: ask about this window",
    section: "Agent",
    icon: Sparkles,
    keywords: "ai assistant selection explain",
    run: (desk) => void summon(desk),
  },
])
