import { PanelsTopLeft } from "lucide-react"
import { registerApp } from "../../desktop/registry"
import { GenuiWindow } from "./window"

registerApp({
  id: "genui",
  title: "GenUI",
  icon: PanelsTopLeft,
  menu: true,
  size: [1060, 760],
  bare: true,
  multi: false,
  render: (props) => <GenuiWindow {...props} />,
})
