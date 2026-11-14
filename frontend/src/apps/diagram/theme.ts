/**
 * The Diagram's theme layer: the colours of its looks (dark, light, neon), written into the
 * drawing so an exported SVG or PNG looks as it does on screen. This file is the only one in
 * the Diagram that names colours (eslint.config.js lists it as a theme file).
 */
import { mix as blend, currentTheme, type DeskTheme } from "../../desktop/themes"
import type { Change, Theme, Tone } from "./model"

export interface Palette {
  bg: string
  /** The canvas grid: fine lines (or dots) and every fifth, stronger. */
  grid: string
  gridMajor: string
  card: string
  cardStroke: string
  text: string
  muted: string
  laneFill: string
  laneStroke: string
  laneTitle: string
  row: string
  select: string
  glow: boolean
  tones: Record<Tone, string>
  note: string
  /** Text on a filled tone (a step badge, a mark count). */
  onTone: string
  /** A faint highlight along a card's top edge. */
  sheen: string
  /** How dark the cards' shadow is. */
  shadow: number
}

const DARK_TONES: Record<Tone, string> = {
  blue: "#6c8cff",
  amber: "#e5a33a",
  red: "#ef5f67",
  green: "#3fbf7f",
  violet: "#a78bfa",
  teal: "#2cc2b0",
  pink: "#ec6fb1",
  slate: "#8b93a7",
}
export const PALETTES: Record<Exclude<Theme, "app">, Palette> = {
  dark: {
    bg: "#131419",
    grid: "#1a1c23",
    gridMajor: "#21232c",
    card: "#1d1f28",
    cardStroke: "#343849",
    text: "#eceef5",
    muted: "#9599ad",
    laneFill: "#17181f",
    laneStroke: "#2a2d39",
    laneTitle: "#b4bbd6",
    row: "#232632",
    select: "#a78bfa",
    glow: false,
    tones: DARK_TONES,
    note: "#3a3420",
    onTone: "#101218",
    sheen: "#2c2f3c",
    shadow: 0.45,
  },
  light: {
    bg: "#f3f4f8",
    grid: "#e8eaf1",
    gridMajor: "#dcdfe8",
    card: "#ffffff",
    cardStroke: "#d2d8e3",
    text: "#18202f",
    muted: "#5d6880",
    laneFill: "#fafbfd",
    laneStroke: "#dce0e9",
    laneTitle: "#18202f",
    row: "#f6f7fa",
    select: "#6d4fe0",
    glow: false,
    tones: {
      blue: "#2f5bd3",
      amber: "#b8740f",
      red: "#c8423f",
      green: "#1f8a50",
      violet: "#6d4fe0",
      teal: "#0f8f83",
      pink: "#c03c86",
      slate: "#5d6678",
    },
    note: "#fff3c4",
    onTone: "#ffffff",
    sheen: "#ffffff",
    shadow: 0.08,
  },
  neon: {
    bg: "#0a0f1f",
    grid: "#0f1730",
    gridMajor: "#15213f",
    card: "#0e1630",
    cardStroke: "#24345e",
    text: "#eaf2ff",
    muted: "#8fa3c8",
    laneFill: "#0c1328",
    laneStroke: "#22325a",
    laneTitle: "#b9c8ea",
    row: "#111b38",
    select: "#f0f",
    glow: true,
    tones: {
      blue: "#38bdf8",
      amber: "#fbbf24",
      red: "#fb7185",
      green: "#4ade80",
      violet: "#a78bfa",
      teal: "#2dd4bf",
      pink: "#f472b6",
      slate: "#94a3b8",
    },
    note: "#2a2410",
    onTone: "#071022",
    sheen: "#16213d",
    shadow: 0.5,
  },
}

/** A comparison's colours: added, changed, removed. */
export const CHANGE_COLOR: Record<Exclude<Change, "same" | "moved">, string> = {
  added: "#3fb950",
  changed: "#e3a008",
  removed: "#f85149",
}

/** The cards' shadow. */
export const SHADOW = "#000000"

/**
 * The "app" look: the desktop theme's own colours (its background, surfaces, text, accent,
 * and its terminal colours as the tones), so a diagram sits in the app like its windows.
 * (blend(a, b, t) moves a towards b by t.)
 */
export function paletteFromDesk(t: DeskTheme): Palette {
  const bg = blend(t.desk, t.bg, 0.5)
  const card = blend(t.bg, t.surface, 0.5)
  return {
    bg,
    grid: blend(bg, t.fg, 0.035),
    gridMajor: blend(bg, t.fg, 0.07),
    card,
    cardStroke: blend(t.surface, t.fg, 0.12),
    text: t.fg,
    muted: blend(t.fgSoft, bg, 0.3),
    laneFill: blend(bg, t.surface, 0.18),
    laneStroke: blend(t.surface, bg, 0.2),
    laneTitle: t.fgSoft,
    row: blend(t.bg, t.surface, 0.3),
    select: t.accent,
    glow: false,
    tones: {
      blue: t.ansi[4],
      amber: t.ansi[3],
      red: t.ansi[1],
      green: t.ansi[2],
      violet: t.accent,
      teal: t.ansi[6],
      pink: t.ansi[5],
      slate: blend(t.fgSoft, bg, 0.35),
    },
    note: blend(card, t.ansi[3], 0.16),
    onTone: t.desk,
    sheen: blend(card, t.fg, 0.06),
    shadow: 0.32,
  }
}

/** The palette a diagram is drawn in: its look, and its own background if it names one. */
export function paletteFor(theme: Theme, background?: string): Palette {
  const base = theme === "app" ? paletteFromDesk(currentTheme()) : PALETTES[theme]
  if (!background) return base
  return {
    ...base,
    bg: background,
    grid: blend(background, base.text, 0.035),
    gridMajor: blend(background, base.text, 0.07),
  }
}
