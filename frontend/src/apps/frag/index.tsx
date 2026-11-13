/**
 * Frag: quick capture into the Fragments Engine, native to Basalisk. An overlay over the
 * desktop (⌥⇧F), queued on this server and delivered behind it (see docs/frag.md).
 */
import { Zap } from "lucide-react"
import { registerCommands, registerOverlay } from "../../desktop/registry"
import { Capture } from "./capture"
import "./frag.css"

registerOverlay({ id: "frag", render: ({ close }) => <Capture close={close} /> })

registerCommands([
  {
    id: "frag.capture",
    title: "Frag: capture a fragment",
    section: "Frag",
    icon: Zap,
    keywords: "note quick capture fragment idea jot",
    keys: "alt+shift+f",
    run: (desk) => desk.toggleOverlay("frag"),
  },
])
