/**
 * Frag: quick capture into the Fragments Engine, native to Basalisk. An overlay over the
 * desktop (⌥⇧F), queued on this server and delivered behind it (see docs/frag.md).
 */
import { Scissors, Zap } from "lucide-react"
import { useState } from "react"
import { registerCommands, registerOverlay } from "../../desktop/registry"
import { Capture } from "./capture"
import { CLIP_EVENT, takePendingClip } from "./clip"
import { ClipBox } from "./clipbox"
import "./frag.css"

/** The box: a clip when a Browser window just handed one over, else a note. */
function FragOverlay({ close }: { close: () => void }) {
  const [clip] = useState(takePendingClip)
  return clip ? <ClipBox page={clip} close={close} /> : <Capture close={close} />
}

registerOverlay({ id: "frag", render: ({ close }) => <FragOverlay close={close} /> })

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
  {
    id: "frag.clip",
    title: "Frag: clip this page",
    section: "Frag",
    icon: Scissors,
    keywords: "save page article web clipper",
    keys: "alt+shift+c",
    when: (desk) => {
      const st = desk.wmState()
      return st.wins.find((w) => w.id === st.focus)?.app === "browser"
    },
    run: (desk) => {
      const focus = desk.wmState().focus
      if (focus) window.dispatchEvent(new CustomEvent(CLIP_EVENT, { detail: focus }))
    },
  },
])
