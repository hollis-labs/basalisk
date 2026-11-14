/**
 * Reader: a read-it-later library and video player over Fragments Engine, plus RSS and Atom
 * feeds (docs/reader.md). Watch is the same window opened on the videos.
 */
import { BookOpen, MonitorPlay } from "lucide-react"
import { registerApp } from "../../desktop/registry"
import { ReaderWindow } from "./window"

registerApp({
  id: "reader",
  title: "Reader",
  icon: BookOpen,
  menu: true,
  size: [1180, 760],
  bare: true,
  render: () => <ReaderWindow start="unread" />,
})

registerApp({
  id: "watch",
  title: "Watch",
  icon: MonitorPlay,
  menu: true,
  size: [1180, 760],
  bare: true,
  render: () => <ReaderWindow start="videos" />,
})
