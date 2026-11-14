/**
 * Writer: books, TTRPG settings and campaigns, a manuscript in an outline and a codex of
 * characters, places and things (docs/work.md). The Work engine's window with the Writer
 * preset.
 */
import { PenLine } from "lucide-react"
import { registerApp } from "../../desktop/registry"
import { WorkWindow } from "../../work/window"

registerApp({
  id: "writer",
  title: "Writer",
  icon: PenLine,
  menu: true,
  size: [1180, 720],
  bare: true,
  render: () => <WorkWindow app="writer" appTitle="Writer" />,
})
