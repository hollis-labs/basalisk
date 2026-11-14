/**
 * Scratch: quick notes, commands, follow-ups and ideas as small items, viewed by day,
 * project, task and more (docs/scratch.md). The window is here; capture is the Frag box
 * switched to scratch (apps/frag/capture.tsx).
 */
import { StickyNote } from "lucide-react"
import { registerApp, registerCommands } from "../../desktop/registry"
import { openCaptureFor } from "../frag/capture"
import { ScratchWindow } from "./window"

registerApp({
  id: "scratch",
  title: "Scratch",
  icon: StickyNote,
  menu: true,
  size: [860, 620],
  bare: true,
  render: () => <ScratchWindow />,
})

registerCommands([
  {
    id: "scratch.capture",
    title: "Scratch: jot something down",
    section: "Scratch",
    icon: StickyNote,
    keywords: "note quick todo follow-up idea command jot capture",
    keys: "alt+shift+s",
    run: (desk) => openCaptureFor(desk, "scratch"),
  },
])
