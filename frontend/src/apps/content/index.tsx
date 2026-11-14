/**
 * Content: ideas to published, for articles, videos, posts and games (docs/work.md). The
 * Work engine's window with the Content preset.
 */
import { Megaphone } from "lucide-react"
import { registerApp } from "../../desktop/registry"
import { WorkWindow } from "../../work/window"

registerApp({
  id: "content",
  title: "Content",
  icon: Megaphone,
  menu: true,
  size: [1180, 720],
  bare: true,
  render: () => <WorkWindow app="content" appTitle="Content" />,
})
