/**
 * Shortcuts. A chord is written the way the native shell writes one: modifiers in the order
 * ctrl, alt, shift, cmd, then the key ("ctrl+alt+t", "cmd+k", "ctrl+`"). Keys are named by
 * their position on the keyboard, not by the character Option turns them into, so a chord
 * pressed in the page and one the native event tap swallowed spell the same.
 */
import { allCommands } from "./registry"

/**
 * Set while something is recording a shortcut (Settings, Shortcuts): the desktop's own key
 * handler stands down so the chord being recorded does not also run a command.
 */
export const keyCapture = { active: false }

const CODES: Record<string, string> = {
  Backquote: "`",
  Minus: "-",
  Equal: "=",
  BracketLeft: "[",
  BracketRight: "]",
  Backslash: "\\",
  Semicolon: ";",
  Quote: "'",
  Comma: ",",
  Period: ".",
  Slash: "/",
  Space: "space",
  Enter: "return",
  Escape: "escape",
  Tab: "tab",
  Backspace: "delete",
  ArrowLeft: "left",
  ArrowRight: "right",
  ArrowUp: "up",
  ArrowDown: "down",
}

function keyName(code: string): string | null {
  if (code.startsWith("Key")) return code.slice(3).toLowerCase()
  if (code.startsWith("Digit")) return code.slice(5)
  if (/^F\d{1,2}$/.test(code)) return code.toLowerCase()
  return CODES[code] ?? null
}

/** The chord a key event spells, or null for a modifier on its own or an unnamed key. */
export function chordOf(e: KeyboardEvent): string | null {
  const key = keyName(e.code)
  if (!key) return null
  const mods = [e.ctrlKey && "ctrl", e.altKey && "alt", e.shiftKey && "shift", e.metaKey && "cmd"]
  return [...mods.filter(Boolean), key].join("+")
}

/** A shortcut needs Control, Option or Command, unless it is a function key. */
export function usableChord(chord: string): boolean {
  const parts = chord.split("+")
  const key = parts[parts.length - 1]
  return /^f\d{1,2}$/.test(key) || parts.some((p) => p === "ctrl" || p === "alt" || p === "cmd")
}

const SYMBOLS: Record<string, string> = {
  ctrl: "⌃",
  alt: "⌥",
  shift: "⇧",
  cmd: "⌘",
  return: "↩",
  escape: "Esc",
  space: "Space",
  delete: "⌫",
  tab: "⇥",
  left: "←",
  right: "→",
  up: "↑",
  down: "↓",
}

/** "ctrl+alt+t" as "⌃⌥T". */
export function prettyChord(chord: string): string {
  if (!chord) return ""
  const parts = chord.split("+")
  const key = parts.pop() ?? ""
  return parts.map((p) => SYMBOLS[p] ?? p).join("") + (SYMBOLS[key] ?? key.toUpperCase())
}

/** The shortcut of a command: the user's choice if there is one ("" means none), else the default. */
export function chordFor(id: string, keys: Record<string, string>): string {
  if (id in keys) return keys[id]
  return allCommands().find((c) => c.id === id)?.keys ?? ""
}

/** Chord to command id, with the user's choices applied over the defaults. */
export function bindings(keys: Record<string, string>): Map<string, string> {
  const map = new Map<string, string>()
  const commands = allCommands()
  // Defaults first, then the user's, so a chord the user moved goes to the command they chose.
  for (const c of commands) {
    if (!(c.id in keys) && c.keys) map.set(c.keys, c.id)
  }
  for (const c of commands) {
    const chord = keys[c.id]
    if (chord) map.set(chord, c.id)
  }
  return map
}
