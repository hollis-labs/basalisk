/** The desktop's own commands. Apps register theirs next to their code. */
import {
  AppWindow,
  ArrowDownToLine,
  ArrowLeftToLine,
  ArrowRightToLine,
  ArrowUpToLine,
  Columns2,
  Crown,
  LogOut,
  Maximize,
  Minus,
  Palette,
  PictureInPicture2,
  Search,
  Square,
  SquareTerminal,
  X,
} from "lucide-react"
import { type DeskAPI, registerCommands } from "./registry"
import { THEMES } from "./themes"

const focused = (d: DeskAPI) => d.wmState().focus
const hasFocus = (d: DeskAPI) => !!focused(d)
const tiling = (d: DeskAPI) => d.wmState().layout === "tile"

/** Runs a window action on the focused window. */
const onFocused = (run: (d: DeskAPI, id: string) => void) => (d: DeskAPI) => {
  const id = focused(d)
  if (id) run(d, id)
}

registerCommands([
  {
    id: "palette.toggle",
    title: "Command palette",
    section: "System",
    icon: Search,
    keys: "cmd+k",
    hidden: true,
    run: (d) => d.togglePalette(),
  },
  {
    id: "terminal.toggle",
    title: "Toggle terminal",
    section: "Terminal",
    icon: SquareTerminal,
    keywords: "shell drop down quake",
    keys: "ctrl+`",
    run: (d) => d.toggleTerminal(),
  },
  {
    id: "layout.toggle",
    title: "Switch between tiling and floating",
    section: "Layout",
    icon: Columns2,
    keys: "ctrl+alt+t",
    run: (d) => d.wm({ type: "layout" }),
  },
  {
    id: "window.next",
    title: "Next window",
    section: "Windows",
    icon: AppWindow,
    keys: "ctrl+alt+j",
    when: hasFocus,
    run: (d) => d.wm({ type: "cycle", delta: 1 }),
  },
  {
    id: "window.previous",
    title: "Previous window",
    section: "Windows",
    icon: AppWindow,
    keys: "ctrl+alt+k",
    when: hasFocus,
    run: (d) => d.wm({ type: "cycle", delta: -1 }),
  },
  {
    id: "window.close",
    title: "Close window",
    section: "Windows",
    icon: X,
    keys: "ctrl+alt+w",
    when: hasFocus,
    run: onFocused((d, id) => d.close(id)),
  },
  {
    id: "window.maximize",
    title: "Maximize or restore window",
    section: "Windows",
    icon: Square,
    keys: "ctrl+alt+return",
    when: hasFocus,
    run: onFocused((d, id) => d.wm({ type: "maximize", id })),
  },
  {
    id: "window.float",
    title: "Float or re-tile window",
    section: "Windows",
    icon: PictureInPicture2,
    keys: "ctrl+alt+f",
    when: (d) => hasFocus(d) && tiling(d),
    run: onFocused((d, id) => d.wm({ type: "float", id })),
  },
  {
    id: "window.master",
    title: "Make window the master",
    section: "Windows",
    icon: Crown,
    keys: "ctrl+alt+m",
    when: (d) => hasFocus(d) && tiling(d),
    run: onFocused((d, id) => d.wm({ type: "promote", id })),
  },
  {
    id: "window.hide",
    title: "Hide window",
    section: "Windows",
    icon: Minus,
    keys: "ctrl+alt+h",
    when: hasFocus,
    run: onFocused((d, id) => d.wm({ type: "minimize", id })),
  },
  {
    id: "window.left",
    title: "Window left: shrink the master, or snap to the left half",
    section: "Windows",
    icon: ArrowLeftToLine,
    keys: "ctrl+alt+left",
    when: hasFocus,
    run: (d) => {
      const s = d.wmState()
      if (s.layout === "tile") d.wm({ type: "ratio", value: s.ratio - 0.04 })
      else if (s.focus) d.wm({ type: "snap", id: s.focus, to: "left", area: d.area() })
    },
  },
  {
    id: "window.right",
    title: "Window right: grow the master, or snap to the right half",
    section: "Windows",
    icon: ArrowRightToLine,
    keys: "ctrl+alt+right",
    when: hasFocus,
    run: (d) => {
      const s = d.wmState()
      if (s.layout === "tile") d.wm({ type: "ratio", value: s.ratio + 0.04 })
      else if (s.focus) d.wm({ type: "snap", id: s.focus, to: "right", area: d.area() })
    },
  },
  {
    id: "window.fill",
    title: "Fill the desktop with the window",
    section: "Windows",
    icon: ArrowUpToLine,
    keys: "ctrl+alt+up",
    when: hasFocus,
    run: onFocused((d, id) =>
      d.wm(tiling(d) ? { type: "maximize", id } : { type: "snap", id, to: "full", area: d.area() }),
    ),
  },
  {
    id: "window.centre",
    title: "Centre the window",
    section: "Windows",
    icon: ArrowDownToLine,
    keys: "ctrl+alt+down",
    when: hasFocus,
    run: onFocused((d, id) => d.wm({ type: "snap", id, to: "center", area: d.area() })),
  },
  {
    id: "shell.kiosk",
    title: "Toggle full screen",
    section: "System",
    icon: Maximize,
    keywords: "kiosk fullscreen",
    when: (d) => !!d.shellStatus()?.available,
    run: (d) => d.setKiosk(!d.shellStatus()?.kiosk),
  },
  {
    id: "session.signout",
    title: "Sign out",
    section: "System",
    icon: LogOut,
    run: (d) => d.signOut(),
  },
  ...THEMES.map((t) => ({
    id: `theme.${t.id}`,
    title: `Theme: ${t.name}`,
    section: "Appearance",
    icon: Palette,
    keywords: "colour color appearance",
    run: (d: DeskAPI) => d.setTheme(t.id),
  })),
])
