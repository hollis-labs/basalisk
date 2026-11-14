/**
 * Drafts: code, content and plans the owner and the agent work on together (docs/collab.md).
 * Select a passage and say what about it; the agent reads the marks, answers them and
 * proposes changes as new revisions, which are shown as a comparison.
 */
import { FilePen } from "lucide-react"
import { collab } from "../../api/collab"
import "../../collab/agent"
import { registerApp, registerCommands } from "../../desktop/registry"
import { DraftsWindow } from "./window"

registerApp({
  id: "drafts",
  title: "Drafts",
  icon: FilePen,
  menu: true,
  size: [1240, 780],
  bare: true,
  multi: true,
  defaultKey: "drafts",
  render: ({ winId, args }) => <DraftsWindow winId={winId} args={args} />,
})

registerCommands([
  {
    id: "drafts.from-window",
    title: "Draft: start one from what this window shows",
    section: "Drafts",
    icon: FilePen,
    keywords: "review annotate selection collaborate agent",
    when: (desk) => !!desk.wmState().focus,
    run: async (desk) => {
      const c = await desk.context()
      const body = c?.selection ?? ""
      if (!c || !body) {
        desk.open("drafts")
        return
      }
      const ext = c.file?.path.split(".").pop()?.toLowerCase() ?? ""
      const lang =
        {
          go: "go",
          ts: "typescript",
          tsx: "typescript",
          js: "javascript",
          py: "python",
          md: "markdown",
          css: "css",
          sh: "shell",
          yaml: "yaml",
          yml: "yaml",
          json: "json",
        }[ext] ?? "markdown"
      const doc = await collab.create({ kind: "draft", title: c.title || "Untitled", lang, body })
      desk.open("drafts", { args: { name: doc.name }, key: doc.name, title: doc.title })
    },
  },
])
