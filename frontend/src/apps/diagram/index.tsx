/**
 * Diagram: architecture and flow drawings the owner and the agent keep together
 * (docs/collab.md). Nodes, edges and groups as plain JSON in Caster; every save is a
 * revision, and any two are compared on the canvas: what was added, removed or changed.
 */
import { Workflow } from "lucide-react"
import "../../collab/agent"
import { registerApp } from "../../desktop/registry"
import { DiagramWindow } from "./window"

registerApp({
  id: "diagram",
  title: "Diagram",
  icon: Workflow,
  menu: true,
  size: [1280, 800],
  bare: true,
  multi: true,
  defaultKey: "diagram",
  render: ({ winId, args }) => <DiagramWindow winId={winId} args={args} />,
})
