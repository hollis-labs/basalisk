import { Button } from "@hollis-labs/design-components"
import { useCallback, useEffect, useRef, useState } from "react"
import {
  SHELL_EVENT,
  type ShellEvent,
  type ShellStatus,
  type ShellWindow,
  shell,
} from "../api/shell"
import { Notice } from "../components/forms"

const DEFAULT_COMBOS = [
  "cmd+q",
  "cmd+h",
  "cmd+m",
  "cmd+tab",
  "cmd+shift+tab",
  "cmd+space",
  "ctrl+up",
  "ctrl+down",
  "ctrl+left",
  "ctrl+right",
].join(", ")
const COMBOS_KEY = "basalisk.shell.combos"

const pretty = (chord: string) =>
  chord
    .split("+")
    .map(
      (p) => ({ ctrl: "⌃", alt: "⌥", shift: "⇧", cmd: "⌘", escape: "Esc" })[p] ?? p.toUpperCase(),
    )
    .join(" ")

function Flag({ on, yes, no }: { on: boolean; yes: string; no: string }) {
  return <span className={on ? "text-success" : "text-fg-muted"}>{on ? yes : no}</span>
}

/**
 * The Mac shell page: the controls and readouts of the native layer. It is the bench for the
 * shell spike (kiosk presentation, key ownership, window restore) and reports what the system
 * actually did, so each step can be checked by pressing the keys.
 */
export function ShellPage() {
  const [status, setStatus] = useState<ShellStatus | null>(null)
  const [events, setEvents] = useState<ShellEvent[]>([])
  const [windows, setWindows] = useState<ShellWindow[] | null>(null)
  const [error, setError] = useState("")
  const [blockSwitching, setBlockSwitching] = useState(false)
  const [combos, setCombos] = useState(
    () => window.localStorage.getItem(COMBOS_KEY) ?? DEFAULT_COMBOS,
  )
  const [keys, setKeys] = useState<string[]>([])
  const seq = useRef(0)

  const run = useCallback(async (fn: () => Promise<ShellStatus>) => {
    try {
      setStatus(await fn())
      setError("")
    } catch (e) {
      setError(e instanceof Error ? e.message : "The shell refused.")
    }
  }, [])

  const refresh = useCallback(() => run(shell.status), [run])

  const loadWindows = useCallback(async () => {
    try {
      setWindows((await shell.windows()).windows)
    } catch (e) {
      setWindows(null)
      setError(e instanceof Error ? e.message : "Could not list windows.")
    }
  }, [])

  useEffect(() => {
    let live = true
    const pull = async () => {
      try {
        const next = await shell.events(seq.current)
        if (!live || !next.events.length) return
        seq.current = next.events[next.events.length - 1].seq
        setEvents((list) => [...next.events.reverse(), ...list].slice(0, 80))
      } catch {
        // Not the desktop app: there is no event log.
      }
    }
    void refresh().then(pull)
    // The desktop app announces every shell event; the timer covers anything missed.
    const onShell = () => {
      void refresh()
      void pull()
    }
    window.addEventListener(SHELL_EVENT, onShell)
    const timer = window.setInterval(onShell, 4000)
    return () => {
      live = false
      window.removeEventListener(SHELL_EVENT, onShell)
      window.clearInterval(timer)
    }
  }, [refresh])

  // What the page itself receives. A swallowed shortcut never shows up here; it shows in the
  // event log instead.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const mods = [e.ctrlKey && "⌃", e.altKey && "⌥", e.shiftKey && "⇧", e.metaKey && "⌘"]
        .filter(Boolean)
        .join("")
      const stamp = new Date().toLocaleTimeString([], { hour12: false })
      setKeys((list) =>
        [`${stamp}  ${mods}${mods ? " " : ""}${e.key === " " ? "Space" : e.key}`, ...list].slice(
          0,
          8,
        ),
      )
    }
    window.addEventListener("keydown", onKey, true)
    return () => window.removeEventListener("keydown", onKey, true)
  }, [])

  if (!status) return <p className="text-fg-muted">Asking the shell…</p>
  if (!status.available)
    return (
      <div className="panel">
        <h2>The native shell is not here</h2>
        <p className="text-fg-muted">
          Kiosk mode, key ownership and window control belong to the macOS desktop app. This page is
          being served to a browser, or by a server deployment. Open Basalisk.app to use them.
        </p>
      </div>
    )

  const comboList = combos
    .split(/[\s,]+/)
    .map((c) => c.trim())
    .filter(Boolean)
  const p = status.permissions

  return (
    <div className="shell-page">
      <Notice error={error} />

      <section className="panel shell-section">
        <h2>Full screen</h2>
        <p className="text-fg-muted">
          Kiosk makes the window borderless and screen sized, and hides the menu bar and Dock. It
          applies only while Basalisk is in front. <strong>{pretty(status.chords.escape)}</strong>{" "}
          always leaves kiosk and stops key swallowing.
        </p>
        <dl className="shell-facts">
          <dt>Kiosk</dt>
          <dd>
            <Flag on={status.kiosk} yes="on" no="off" />
          </dd>
          <dt>Menu bar</dt>
          <dd>
            <Flag on={!status.menu_bar_visible} yes="hidden" no="visible" />
          </dd>
          <dt>Cmd-Tab</dt>
          <dd>
            <Flag on={status.block_switching} yes="blocked" no="allowed" />
          </dd>
          <dt>Frontmost</dt>
          <dd>
            <Flag on={status.active} yes="yes" no="no" />
          </dd>
          <dt>Display</dt>
          <dd>
            {status.screen.width} × {status.screen.height}
            {status.screen.safe_top > 0 ? `, camera strip ${status.screen.safe_top}pt` : ""}
            {status.screen.count > 1 ? `, ${status.screen.count} displays` : ""}
          </dd>
        </dl>
        <label className="shell-check">
          <input
            type="checkbox"
            checked={blockSwitching}
            onChange={(e) => setBlockSwitching(e.target.checked)}
          />
          Also block Cmd-Tab while in kiosk
        </label>
        <div className="actions">
          <Button onClick={() => void run(() => shell.kiosk(!status.kiosk, blockSwitching))}>
            {status.kiosk ? "Leave kiosk" : "Enter kiosk"}
          </Button>
          <Button
            variant="secondary"
            disabled={!status.kiosk}
            onClick={() => void run(() => shell.menuBar(!status.menu_bar_visible))}
          >
            {status.menu_bar_visible ? "Hide menu bar" : "Show menu bar"}
          </Button>
        </div>
        <p className="shell-chords text-fg-muted">
          {pretty(status.chords.toggle_kiosk)} toggles kiosk ·{" "}
          {pretty(status.chords.toggle_menu_bar)} shows or hides the menu bar · both work from any
          app
        </p>
      </section>

      <section className="panel shell-section">
        <h2>Own the keyboard</h2>
        <p className="text-fg-muted">
          While Basalisk is in front, these shortcuts are taken before any app or the system sees
          them, and logged below. Needs the Accessibility permission. Force Quit (⌘ ⌥ Esc) is never
          taken.
        </p>
        <textarea
          className="shell-combos"
          rows={2}
          spellCheck={false}
          value={combos}
          onChange={(e) => {
            setCombos(e.target.value)
            window.localStorage.setItem(COMBOS_KEY, e.target.value)
          }}
        />
        <dl className="shell-facts">
          <dt>Swallowing</dt>
          <dd>
            <Flag on={status.swallow} yes={`on: ${status.combos.join(", ")}`} no="off" />
          </dd>
          <dt>Event tap</dt>
          <dd>
            <Flag on={status.tap_installed} yes="installed" no="not installed" />
          </dd>
        </dl>
        <div className="actions">
          <Button onClick={() => void run(() => shell.swallow(!status.swallow, comboList))}>
            {status.swallow ? "Stop swallowing" : "Start swallowing"}
          </Button>
        </div>
        <div className="shell-keys">
          <h3>Keys this page received</h3>
          {keys.length ? (
            <ol>
              {keys.map((k, i) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: a rolling log with repeats
                <li key={i}>{k}</li>
              ))}
            </ol>
          ) : (
            <p className="text-fg-muted">
              Press a key. Typing here proves keys still reach the page in kiosk.
            </p>
          )}
        </div>
      </section>

      <section className="panel shell-section">
        <h2>Permissions</h2>
        <p className="text-fg-muted">
          macOS grants these to <code>{status.identity.bundle_id || "an unbundled binary"}</code>
          {status.identity.bundled ? "" : " (run from the app bundle so the grant sticks)"}. After
          granting in System Settings, quit and reopen Basalisk.
        </p>
        <dl className="shell-facts">
          <dt>Accessibility</dt>
          <dd>
            <Flag on={p.accessibility} yes="granted" no="not granted" />{" "}
            {!p.accessibility && (
              <button
                type="button"
                className="shell-link"
                onClick={() => void run(() => shell.requestPermission("accessibility"))}
              >
                Ask
              </button>
            )}
          </dd>
          <dt>Input Monitoring</dt>
          <dd>
            <Flag on={p.input_monitoring} yes="granted" no="not granted" />{" "}
            {!p.input_monitoring && (
              <button
                type="button"
                className="shell-link"
                onClick={() => void run(() => shell.requestPermission("input-monitoring"))}
              >
                Ask
              </button>
            )}
          </dd>
        </dl>
      </section>

      <section className="panel shell-section">
        <h2>Other apps' windows</h2>
        <p className="text-fg-muted">
          Read through the Accessibility API. Cmd-Tab to an app whose windows are all minimized
          brings nothing back; the switch below restores one whenever that happens.
        </p>
        <label className="shell-check">
          <input
            type="checkbox"
            checked={status.fix_minimized}
            onChange={(e) => void run(() => shell.fixMinimized(e.target.checked))}
          />
          Restore a minimized window when its app is activated
        </label>
        <div className="actions">
          <Button variant="secondary" onClick={() => void loadWindows()}>
            {windows ? "Refresh windows" : "List windows"}
          </Button>
        </div>
        {windows && (
          <ul className="shell-windows">
            {windows.map((w) => (
              <li key={`${w.pid}-${w.index}`} data-minimized={w.minimized}>
                <span className="shell-window-app">{w.app}</span>
                <span className="shell-window-title">{w.title || "(untitled)"}</span>
                {w.minimized ? (
                  <button
                    type="button"
                    className="shell-link"
                    onClick={() =>
                      void run(() => shell.unminimize(w.pid, w.index)).then(loadWindows)
                    }
                  >
                    Restore
                  </button>
                ) : (
                  <span className="text-fg-muted">{w.app_hidden ? "hidden" : "open"}</span>
                )}
              </li>
            ))}
            {!windows.length && <li className="text-fg-muted">No windows on this Space.</li>}
          </ul>
        )}
      </section>

      <section className="panel shell-section">
        <h2>Shell log</h2>
        {events.length ? (
          <ol className="shell-log">
            {events.map((e) => (
              <li key={e.seq}>
                <time>{new Date(e.at).toLocaleTimeString([], { hour12: false })}</time>
                <span className="shell-log-kind">{e.kind}</span>
                <span>{e.detail}</span>
              </li>
            ))}
          </ol>
        ) : (
          <p className="text-fg-muted">Nothing yet.</p>
        )}
      </section>
    </div>
  )
}
