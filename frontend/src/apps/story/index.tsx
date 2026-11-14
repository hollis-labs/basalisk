/**
 * Story: video scripts and storyboards with the writing team (docs/story.md). A board of
 * scripts by stage; a script as a storyboard of scene cards; the text in Caster.
 */
import { Clapperboard } from "lucide-react"
import { registerApp } from "../../desktop/registry"
import { StoryWindow } from "./window"

registerApp({
  id: "story",
  title: "Story",
  icon: Clapperboard,
  menu: true,
  size: [1100, 700],
  bare: true,
  render: () => <StoryWindow />,
})
