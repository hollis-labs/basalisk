/**
 * The agent: one assistant beside the windows (docs/agent.md). It registers a side panel,
 * the command that summons it, and the desktop's tools (tools.ts).
 */
import { Bot, Sparkles } from "lucide-react"
import { nativeBrowser } from "../api/browser"
import {
  type DeskAPI,
  registerApp,
  registerCommands,
  registerPanel,
  registerStartup,
} from "../desktop/registry"
import { AgentPanel, FOCUS_EVENT, TaskWindow } from "./panel"
import { noteContext, start } from "./session"
import { startTasks } from "./tasks"
import "./contexts"
import "./tools"

registerPanel({
  id: "agent",
  title: "Agent",
  width: 400,
  render: ({ close }) => <AgentPanel close={close} />,
})

// A task agent's own window: its conversation, beside the others (docs/agent.md).
registerApp({
  id: "agent-task",
  title: "Task agent",
  icon: Bot,
  menu: false,
  size: [460, 560],
  bare: true,
  multi: true,
  render: ({ args }) =>
    typeof args.task === "string" ? <TaskWindow task={args.task} /> : <p>No task.</p>,
})

// Task agents are followed from the moment the desktop is up, panel open or not: this
// page runs the tools they ask for.
registerStartup(startTasks)

// The global summon chord (⌃⌥⌘J, the Mac shell): Basalisk has been brought forward from
// another app; open the panel and put the keyboard in it.
registerStartup((desk) => {
  window.addEventListener("basalisk:summon", () => void summon(desk))
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
