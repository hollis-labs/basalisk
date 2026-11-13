/**
 * Desktop themes. Each palette comes from an Omarchy theme's colors.toml and is applied by
 * overriding the design tokens' base variables (--hl-*) on the root element, so every kit
 * component follows it, plus a few variables of the desktop's own (--desk-*).
 */
export interface DeskTheme {
  id: string
  name: string
  /** The desktop behind the windows and the bar. */
  desk: string
  /** Window and panel background. */
  bg: string
  /** Raised surfaces: inputs, hovered rows, inactive borders. */
  surface: string
  fg: string
  fgSoft: string
  muted: string
  accent: string
  /** Second accent, for the far end of the focused border and small highlights. */
  accent2: string
  selection: string
  ansi: [
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
    string,
  ]
}

export const THEMES: DeskTheme[] = [
  {
    id: "aura",
    name: "Aura",
    desk: "#0f0e15",
    bg: "#15141b",
    surface: "#2d2b38",
    fg: "#edecee",
    fgSoft: "#cdccce",
    muted: "#6d6d6d",
    accent: "#a277ff",
    accent2: "#61ffca",
    selection: "#29263c",
    ansi: [
      "#110f18",
      "#ff6767",
      "#61ffca",
      "#ffca85",
      "#a277ff",
      "#f694ff",
      "#82e2ff",
      "#edecee",
      "#6d6d6d",
      "#ff6767",
      "#9dff65",
      "#ffca85",
      "#a277ff",
      "#f694ff",
      "#82e2ff",
      "#ffffff",
    ],
  },
  {
    id: "moodpeak",
    name: "Moodpeak",
    desk: "#101318",
    bg: "#181c22",
    surface: "#23272d",
    fg: "#e0e6ed",
    fgSoft: "#d1d7e0",
    muted: "#7b8599",
    accent: "#4ecdc4",
    accent2: "#b388eb",
    selection: "#2c3444",
    ansi: [
      "#181c22",
      "#ff7b92",
      "#4ecdc4",
      "#82eeff",
      "#6a85ff",
      "#b388eb",
      "#c9aff0",
      "#d1d7e0",
      "#3d4455",
      "#ff9ead",
      "#82eeff",
      "#b388eb",
      "#96aaff",
      "#d1b3ff",
      "#a5f5ff",
      "#efedf2",
    ],
  },
  {
    id: "cyberphunk",
    name: "Cyberphunk",
    desk: "#010206",
    bg: "#060a14",
    surface: "#101e36",
    fg: "#e8fcff",
    fgSoft: "#9fb6c8",
    muted: "#4a6b82",
    accent: "#22e4ff",
    accent2: "#ff6bd6",
    selection: "#14294a",
    ansi: [
      "#010206",
      "#ff3b6b",
      "#44ffaa",
      "#ffaa33",
      "#3ac8ff",
      "#ff6bd6",
      "#22e4ff",
      "#e8fcff",
      "#4a6b82",
      "#ff4d7a",
      "#7dffcb",
      "#ffc46b",
      "#6bd4ff",
      "#ff8fe0",
      "#6befff",
      "#e8fcff",
    ],
  },
  {
    id: "blade-rainner",
    name: "Blade Rainner",
    desk: "#06090a",
    bg: "#0e1417",
    surface: "#1a2429",
    fg: "#c6d2d2",
    fgSoft: "#a9b8bb",
    muted: "#68797e",
    accent: "#e0a062",
    accent2: "#7ecfd4",
    selection: "#223038",
    ansi: [
      "#06090a",
      "#e05c52",
      "#7fa66a",
      "#d9a441",
      "#6aa7c4",
      "#c07fa8",
      "#7ecfd4",
      "#c6d2d2",
      "#4a5f66",
      "#f07168",
      "#94bc7e",
      "#e8b857",
      "#82bbd6",
      "#d394bc",
      "#96dfe3",
      "#e4edec",
    ],
  },
]

const KEY = "basalisk.desk.theme"
let active: DeskTheme = THEMES[0]

export function storedTheme(): DeskTheme {
  try {
    const id = window.localStorage.getItem(KEY)
    return THEMES.find((t) => t.id === id) ?? THEMES[0]
  } catch {
    return THEMES[0]
  }
}

/** The theme in effect (for the terminal palette). */
export const currentTheme = () => active

const channel = (hex: string, at: number) => Number.parseInt(hex.slice(at, at + 2), 16)

/** a blended towards b by amount (0..1), as #rrggbb. */
export function mix(a: string, b: string, amount: number): string {
  const part = (at: number) =>
    Math.round(channel(a, at) + (channel(b, at) - channel(a, at)) * amount)
      .toString(16)
      .padStart(2, "0")
  return `#${part(1)}${part(3)}${part(5)}`
}

const alpha = (hex: string, a: number) =>
  `rgb(${channel(hex, 1)} ${channel(hex, 3)} ${channel(hex, 5)} / ${a})`

/** Apply a theme to the document and remember it. */
export function applyDeskTheme(theme: DeskTheme) {
  active = theme
  const t = theme
  const vars: Record<string, string> = {
    "--hl-bg": t.bg,
    "--hl-bg-elevated": mix(t.bg, t.surface, 0.45),
    "--hl-surface": mix(t.bg, t.surface, 0.7),
    "--hl-surface-hover": t.surface,
    "--hl-surface-active": mix(t.surface, t.fg, 0.08),
    "--hl-border": mix(t.surface, t.fg, 0.1),
    "--hl-border-subtle": t.surface,
    "--hl-divider": t.surface,
    "--hl-fg": t.fg,
    "--hl-fg-secondary": t.fgSoft,
    "--hl-fg-muted": mix(t.fgSoft, t.bg, 0.38),
    "--hl-fg-faint": t.muted,
    "--hl-primary": t.accent,
    "--hl-primary-hover": mix(t.accent, "#ffffff", 0.14),
    "--hl-primary-active": t.accent,
    "--hl-primary-muted": alpha(t.accent, 0.14),
    "--hl-primary-fg": t.desk,
    "--hl-brand": t.accent,
    "--hl-brand-hover": mix(t.accent, "#ffffff", 0.14),
    "--hl-brand-active": t.accent,
    "--hl-brand-muted": alpha(t.accent, 0.14),
    "--hl-brand-fg": t.desk,
    "--hl-ring": t.accent,
    "--hl-selection": t.selection,
    "--hl-selection-fg": t.fg,
    "--hl-danger": t.ansi[1],
    "--hl-danger-muted": alpha(t.ansi[1], 0.14),
    "--hl-success": t.ansi[2],
    "--hl-success-muted": alpha(t.ansi[2], 0.14),
    "--hl-warning": t.ansi[3],
    "--hl-warning-muted": alpha(t.ansi[3], 0.14),
    "--hl-info": t.ansi[6],
    "--hl-info-muted": alpha(t.ansi[6], 0.14),
    "--dash-accent-color": t.accent,
    "--desk-bg": t.desk,
    "--desk-panel": t.bg,
    "--desk-surface": t.surface,
    "--desk-fg": t.fg,
    "--desk-fg-soft": t.fgSoft,
    "--desk-muted": t.muted,
    "--desk-accent": t.accent,
    "--desk-accent-2": t.accent2,
    "--desk-accent-glow": alpha(t.accent, 0.07),
    "--desk-accent-2-glow": alpha(t.accent2, 0.04),
    "--desk-accent-soft": alpha(t.accent, 0.16),
    "--desk-border": mix(t.surface, t.bg, 0.25),
    "--desk-selection": t.selection,
  }
  const root = document.documentElement
  for (const [name, value] of Object.entries(vars)) root.style.setProperty(name, value)
  root.setAttribute("data-mode", "dark")
  // The terminals watch this attribute to repaint in the new palette.
  root.setAttribute("data-desk-theme", t.id)
  try {
    window.localStorage.setItem(KEY, t.id)
  } catch {
    // Private storage: the theme lasts for this run only.
  }
}
