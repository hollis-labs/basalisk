import { RotateCcw, X } from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { useDesk } from "./context"
import { chordFor, chordOf, keyCapture, prettyChord, usableChord } from "./keys"
import { allCommands, type Command } from "./registry"
import { updateSettings, useDeskSettings } from "./settings"

/** The shortcuts the native shell registers itself. They are fixed for now. */
const NATIVE = [
  { title: "Leave full screen and stop key swallowing", chord: "ctrl+alt+cmd+escape" },
  { title: "Toggle full screen (from any app)", chord: "ctrl+alt+cmd+f" },
  { title: "Show or hide the menu bar in full screen", chord: "ctrl+alt+cmd+m" },
]

/**
 * Settings, Shortcuts: every command and its shortcut. Click a shortcut and press the keys
 * to change it; Delete clears it; Escape leaves it alone. A chord can belong to one command,
 * so giving it to another takes it from the first.
 */
export function Shortcuts() {
  const desk = useDesk()
  const { keys } = useDeskSettings()
  const [recording, setRecording] = useState<string | null>(null)
  const [filter, setFilter] = useState("")
  const [note, setNote] = useState("")

  const commands = useMemo(() => allCommands(), [])
  const sections = useMemo(() => {
    const q = filter.trim().toLowerCase()
    const out = new Map<string, Command[]>()
    for (const c of commands) {
      if (
        q &&
        !`${c.title} ${c.section} ${prettyChord(chordFor(c.id, keys))}`.toLowerCase().includes(q)
      )
        continue
      out.set(c.section, [...(out.get(c.section) ?? []), c])
    }
    return [...out.entries()]
  }, [commands, filter, keys])

  // While recording, the next chord is the answer and nothing else sees it.
  useEffect(() => {
    if (!recording) return
    keyCapture.active = true
    const onKey = (e: KeyboardEvent) => {
      e.preventDefault()
      e.stopPropagation()
      const chord = chordOf(e)
      if (!chord) return // a modifier on its own: keep waiting
      if (chord === "escape") {
        setRecording(null)
        return
      }
      const next = { ...keys }
      if (chord === "delete") {
        next[recording] = ""
        setNote("")
      } else if (!usableChord(chord)) {
        setNote(`${prettyChord(chord)} needs Control, Option or Command.`)
        return
      } else {
        const holder = commands.find((c) => c.id !== recording && chordFor(c.id, keys) === chord)
        if (holder) {
          next[holder.id] = ""
          setNote(`${prettyChord(chord)} was "${holder.title}"; that command now has no shortcut.`)
        } else {
          setNote("")
        }
        next[recording] = chord
      }
      updateSettings({ keys: next })
      setRecording(null)
    }
    window.addEventListener("keydown", onKey, true)
    return () => {
      keyCapture.active = false
      window.removeEventListener("keydown", onKey, true)
    }
  }, [recording, keys, commands])

  const reset = (id: string) => {
    const next = { ...keys }
    delete next[id]
    updateSettings({ keys: next })
  }

  return (
    <div className="desk-shortcuts">
      <div className="desk-shortcuts-top">
        <input
          className="desk-input"
          placeholder="Filter commands"
          value={filter}
          spellCheck={false}
          onChange={(e) => setFilter(e.target.value)}
        />
        <button
          type="button"
          className="desk-button"
          disabled={!Object.keys(keys).length}
          onClick={() => {
            updateSettings({ keys: {} })
            setNote("")
          }}
        >
          Reset all
        </button>
      </div>
      <p className="desk-shortcuts-help">
        Click a shortcut, then press the keys. Delete clears it, Escape cancels. Any command can
        have one, including opening an app.
        {desk.shellStatus()?.available
          ? " In full screen with key swallowing on, shortcuts the system would take (⌘Space, ⌘Tab) work here too once they are in the swallow list."
          : ""}
      </p>
      {note && (
        <p className="desk-shortcuts-note" role="status">
          {note}
        </p>
      )}
      {sections.map(([section, list]) => (
        <section key={section} className="desk-shortcuts-section">
          <h3>{section}</h3>
          {list.map((c) => {
            const chord = chordFor(c.id, keys)
            const changed = c.id in keys
            const Icon = c.icon
            return (
              <div key={c.id} className="desk-shortcut" data-recording={recording === c.id}>
                {Icon ? <Icon aria-hidden="true" size={14} strokeWidth={1.6} /> : <span />}
                <span className="desk-shortcut-title">{c.title}</span>
                <button
                  type="button"
                  className="desk-shortcut-chord"
                  data-empty={!chord}
                  onClick={() => {
                    setNote("")
                    setRecording(recording === c.id ? null : c.id)
                  }}
                >
                  {recording === c.id ? "Press keys…" : chord ? prettyChord(chord) : "none"}
                </button>
                <button
                  type="button"
                  className="desk-shortcut-icon"
                  title="Remove shortcut"
                  aria-label={`Remove the shortcut of ${c.title}`}
                  disabled={!chord}
                  onClick={() => updateSettings({ keys: { ...keys, [c.id]: "" } })}
                >
                  <X aria-hidden="true" size={13} strokeWidth={1.6} />
                </button>
                <button
                  type="button"
                  className="desk-shortcut-icon"
                  title={
                    c.keys
                      ? `Back to the default (${prettyChord(c.keys)})`
                      : "Back to the default (none)"
                  }
                  aria-label={`Reset the shortcut of ${c.title}`}
                  disabled={!changed}
                  onClick={() => reset(c.id)}
                >
                  <RotateCcw aria-hidden="true" size={13} strokeWidth={1.6} />
                </button>
              </div>
            )
          })}
        </section>
      ))}
      <section className="desk-shortcuts-section">
        <h3>Native shell (fixed)</h3>
        {NATIVE.map((n) => (
          <div key={n.chord} className="desk-shortcut">
            <span />
            <span className="desk-shortcut-title">{n.title}</span>
            <span className="desk-shortcut-chord" data-fixed="true">
              {prettyChord(n.chord)}
            </span>
            <span />
            <span />
          </div>
        ))}
      </section>
    </div>
  )
}
