/**
 * ADE on the desktop: one hub for the whole window. A window that wants the owner and the
 * agent to point at the same things registers itself as a surface (useSurface with this
 * hub); the agent then gets the hub's tools (look, point, mark, guide, and each surface's
 * own actions) beside the desktop's, and what it points at is drawn over the desktop.
 *
 * The packages are the shared ones (@hollis-labs/ade-core, ade-react); this file is the
 * adapter between them and the desktop's own agent tools.
 */
import { adeTools, createHub } from "@hollis-labs/ade-core"
import { AttentionLayer, GuideBar } from "@hollis-labs/ade-react"
import "@hollis-labs/ade-react/styles.css"
import { type AgentTool, registerAgentToolSource } from "../agent/tools"
import "./ade.css"

export const hub = createHub()

// The hub's tools as the desktop's: the same name, schema and level; the desk is not needed.
registerAgentToolSource(() =>
  adeTools(hub).map(
    (t): AgentTool => ({
      name: t.name,
      description: t.description,
      input_schema: t.input_schema,
      level: t.level,
      describe: t.describe,
      run: (input, _desk, ctx) => t.run(input, { signal: ctx.signal }),
    }),
  ),
)

// Beside the desk on window.basalisk, for the console and for tests.
;(window as unknown as { basaliskAde?: unknown }).basaliskAde = { hub, tools: () => adeTools(hub) }

/**
 * What the agent points at, and the guide when one runs. The owner's own selection is left
 * to each window, which already shows it.
 */
export function AdeLayer() {
  return (
    <>
      <AttentionLayer hub={hub} agentLabel="Bas" drawUser={false} />
      <div className="ade-dock">
        <GuideBar hub={hub} />
      </div>
    </>
  )
}
