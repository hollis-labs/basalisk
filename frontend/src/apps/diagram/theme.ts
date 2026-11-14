/**
 * The Diagram's theme layer: the colours of its looks (dark, light, neon), written into the
 * drawing so an exported SVG or PNG looks as it does on screen. This file is the only one in
 * the Diagram that names colours (eslint.config.js lists it as a theme file).
 */
import type { Change, Theme, Tone } from "./model"

export interface Palette {
  bg: string
  grid: string
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
export const PALETTES: Record<Theme, Palette> = {
  dark: {
    bg: "#14151b",
    grid: "#2a2c36",
    card: "#1c1e27",
    cardStroke: "#323646",
    text: "#e7e8ef",
    muted: "#9a9eb2",
    laneFill: "#181a22",
    laneStroke: "#2b2e3b",
    laneTitle: "#aab1cc",
    row: "#20232e",
    select: "#a78bfa",
    glow: false,
    tones: DARK_TONES,
    note: "#3a3420",
  },
  light: {
    bg: "#f4f5f9",
    grid: "#d9dce6",
    card: "#ffffff",
    cardStroke: "#c8cedb",
    text: "#1b2233",
    muted: "#5f6a83",
    laneFill: "#fbfbfd",
    laneStroke: "#d5d9e3",
    laneTitle: "#1b2233",
    row: "#f7f8fb",
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
  },
  neon: {
    bg: "#0a0f1f",
    grid: "#16213d",
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
  },
}

/** A comparison's colours: added, changed, removed. */
export const CHANGE_COLOR: Record<Exclude<Change, "same" | "moved">, string> = {
  added: "#3fb950",
  changed: "#e3a008",
  removed: "#f85149",
}

/** The cards' drop shadow. */
export const SHADOW = "#000"
