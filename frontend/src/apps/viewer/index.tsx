/**
 * Viewer: look at an artifact (an image, a video, a recording, a PDF, an HTML mock) with the
 * agent, and point at what you mean (docs/collab.md). Drag a box on it and say what about
 * it; a video's marks remember the moment. The marks live with the artifact in Caster.
 */
import { ScanSearch } from "lucide-react"
import "../../collab/agent"
import { registerApp, registerCommands, registerFileHandler } from "../../desktop/registry"
import { REVIEWABLE, ViewerWindow } from "./window"

registerApp({
  id: "viewer",
  title: "Viewer",
  icon: ScanSearch,
  menu: true,
  size: [1240, 800],
  bare: true,
  multi: true,
  defaultKey: "viewer",
  render: ({ winId, args }) => <ViewerWindow winId={winId} args={args} />,
})

// Pictures, video and sound open here (HTML and PDF keep opening in the Browser; the
// command below brings one here to mark up).
registerFileHandler({
  app: "viewer",
  match: (name) => /\.(png|jpe?g|gif|webp|avif|mp4|webm|mov|m4v|mp3|wav|m4a)$/.test(name),
})

registerCommands([
  {
    id: "viewer.review",
    title: "Review: mark up the file this window shows",
    section: "Review",
    icon: ScanSearch,
    keywords: "annotate image video mock pdf artifact point agent",
    when: (desk) => !!desk.wmState().focus,
    run: async (desk) => {
      const c = await desk.context()
      const file = c?.file
      if (!file || !REVIEWABLE.test(file.path)) {
        desk.open("viewer")
        return
      }
      desk.open("viewer", {
        args: { file },
        key: `${file.root}:${file.path}`,
        title: file.path.split("/").pop(),
      })
    },
  },
])
