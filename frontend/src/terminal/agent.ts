/**
 * The agent's tools for the terminal: list the shells, read one's recent output, open one,
 * run one command line (asked every time). Output is read from the xterm of a mounted tab, as
 * plain text. There is no tool for raw keys or for answering a program's prompts.
 */
import type { Terminal } from "@xterm/xterm"
import { registerAgentTools, type ToolContext } from "../agent/tools"
import { request } from "../api/account"
import { terminal as api, type TerminalSession } from "../api/shell"
import type { DeskAPI } from "../desktop/registry"
import { adoptTerminal } from "./deck"
import { openTerminalWindow } from "./solo"
import { liveTerminals } from "./xterm"

const ACTIVE_KEY = "basalisk.terminal.active"
const TEXT_LIMIT = 30_000
const QUIET_MS = 1500
const RUN_LIMIT_MS = 30_000

interface Status {
  id: string
  running: boolean
  busy: boolean
  program?: string
  folder?: string
}

const object = (properties: Record<string, unknown>, required: string[] = []) => ({
  type: "object",
  properties,
  required,
  additionalProperties: false,
})
const str = (description: string) => ({ type: "string", description })
const SHELL = str("A shell id from terminal_list. Leave out for the active shell.")

const activeId = () => {
  try {
    return window.localStorage.getItem(ACTIVE_KEY) ?? ""
  } catch {
    return ""
  }
}

async function sessions(): Promise<TerminalSession[]> {
  const st = await api.state()
  if (!st.enabled)
    throw new Error(
      "This Basalisk has no terminal (it runs in the desktop app, or with BASALISK_TERMINAL=1).",
    )
  return st.sessions
}

const status = (id: string) =>
  request<Status>(`terminal/sessions/${encodeURIComponent(id)}/status`).then((s) => {
    if (s.folder) folders.set(id, s.folder)
    return s
  })
// The last folder seen per shell, for describe() (which cannot wait).
const folders = new Map<string, string>()

/** The shell a call means: the one named, else the active one, else the newest running one. */
async function pick(input: Record<string, unknown>): Promise<TerminalSession> {
  const all = await sessions()
  const named = typeof input.shell_id === "string" ? input.shell_id : ""
  if (named) {
    const s = all.find((x) => x.id === named)
    if (!s) throw new Error(`There is no shell ${named}. terminal_list lists them.`)
    if (s.exited) throw new Error(`Shell ${named} has exited.`)
    return s
  }
  const running = all.filter((s) => !s.exited)
  const s = running.find((x) => x.id === activeId()) ?? running[running.length - 1]
  if (!s) throw new Error("No shell is open. Open one with terminal_open.")
  return s
}

/** Where the terminal is shown: a Terminal window, the drop-down, or nowhere. */
function placement(desk: DeskAPI): {
  shown: "window" | "drop-down" | "hidden"
  window_id?: string
} {
  const root = document.querySelector(".terminal-root")
  if (root?.closest(".terminal-overlay")) return { shown: "drop-down" }
  const win = root?.closest<HTMLElement>("[data-win]")?.dataset.win
  if (win) return { shown: "window", window_id: win }
  const tw = desk.wmState().wins.find((w) => w.app === "terminal")
  return tw ? { shown: "window", window_id: tw.id } : { shown: "hidden" }
}

/** Makes sure the terminal is on screen (a Terminal window, if neither it nor the drop-down is). */
function show(desk: DeskAPI) {
  if (placement(desk).shown === "hidden") desk.open("terminal")
}

async function mounted(id: string, ms = 5000): Promise<Terminal | null> {
  const until = Date.now() + ms
  for (;;) {
    const t = liveTerminals.get(id)
    if (t) return t
    if (Date.now() > until) return null
    await new Promise((r) => setTimeout(r, 100))
  }
}

/** Buffer lines [from, to) as text, joining wrapped lines; trailing blank lines dropped. */
function linesOf(t: Terminal, from: number, to: number): string[] {
  const buf = t.buffer.active
  const out: string[] = []
  for (let y = Math.max(0, from); y < Math.min(to, buf.length); y++) {
    const line = buf.getLine(y)
    if (!line) continue
    const text = line.translateToString(true)
    if (line.isWrapped && out.length) out[out.length - 1] += text
    else out.push(text)
  }
  while (out.length && !out[out.length - 1].trim()) out.pop()
  return out
}

/** Keeps the end of the text (the newest output) within the limit. */
function bounded(lines: string[]) {
  const text = lines.join("\n")
  if (text.length <= TEXT_LIMIT) return { text }
  return {
    text: text.slice(text.length - TEXT_LIMIT),
    note: `cut to the last ${TEXT_LIMIT} of ${text.length} characters`,
  }
}

const notMounted = (id: string) =>
  new Error(
    `Shell ${id}'s output cannot be read: its tab is not on screen. Open the terminal (desktop_open with app terminal) and try again.`,
  )

const STOPPED = "The run was stopped; nothing was typed."

registerAgentTools([
  {
    name: "terminal_list",
    level: "read",
    description:
      "List the terminal's shells: id, title, the folder it is in now, whether it is running, busy (a program holds it) and which one is active, and where the terminal is shown.",
    input_schema: object({}),
    run: async (_input, desk) => {
      const all = await sessions()
      const active = activeId()
      const shells = await Promise.all(
        all.map(async (s) => {
          const st = await status(s.id).catch(() => null)
          return {
            id: s.id,
            title: s.title,
            folder: st?.folder ?? s.cwd,
            state: s.exited ? "exited" : st?.running ? "running" : "not started",
            ...(st?.busy ? { busy: true, program: st.program } : {}),
            active: s.id === active,
            readable: liveTerminals.has(s.id),
          }
        }),
      )
      return { ...placement(desk), shells }
    },
  },
  {
    name: "terminal_read",
    level: "read",
    description: `Read the recent output of a shell as plain text: the last lines (default 200, up to 2000), cut to the last ${TEXT_LIMIT} characters.`,
    input_schema: object({
      shell_id: SHELL,
      lines: {
        type: "integer",
        description: "How many of the last lines (default 200, up to 2000).",
      },
    }),
    run: async (input) => {
      const s = await pick(input)
      const t = liveTerminals.get(s.id)
      if (!t) throw notMounted(s.id)
      const n =
        typeof input.lines === "number" ? Math.max(1, Math.min(2000, Math.floor(input.lines))) : 200
      const buf = t.buffer.active
      // Up to the cursor's line: the rows below it are empty screen, not output.
      const end = buf.baseY + buf.cursorY + 1
      const lines = linesOf(t, end - n, end)
      const out = bounded(lines)
      return { shell_id: s.id, title: s.title, lines: lines.length, ...out }
    },
  },
  {
    name: "terminal_open",
    level: "act",
    description:
      "Open a new shell, optionally in a folder (an absolute path, or ~/…), and make it the active tab; shows the terminal if it is hidden. With own_window it gets a terminal window to itself instead of a tab, so it can sit beside other windows. Returns its id.",
    input_schema: object({
      folder: str("The folder to start in. Leave out for the home folder."),
      own_window: {
        type: "boolean",
        description: "Give the shell a window of its own instead of a tab in the terminal.",
      },
    }),
    run: async (input, desk) => {
      await sessions()
      const folder = typeof input.folder === "string" ? input.folder.trim() : ""
      let s: TerminalSession
      try {
        s = await api.create(folder)
      } catch (e) {
        throw new Error(
          `The shell could not be opened${folder ? ` in ${folder}` : ""}: ${e instanceof Error ? e.message : e}`,
        )
      }
      if (s.cwd) folders.set(s.id, s.cwd)
      if (input.own_window === true) openTerminalWindow(desk, { session: s.id, title: s.title })
      else {
        adoptTerminal(s)
        show(desk)
      }
      const ready = await mounted(s.id)
      return {
        shell_id: s.id,
        title: s.title,
        folder: s.cwd,
        ...(ready
          ? {}
          : { note: "the terminal is not on screen yet; the shell starts when it is shown" }),
      }
    },
  },
  {
    name: "terminal_run",
    level: "confirm",
    description:
      "Type one command line into a shell and press Enter (the owner is asked every time). Waits until the output has been quiet for 1.5 seconds, or 30 seconds at most, and returns the output that followed. Refused if a program is already running in the shell. One line only; nothing is typed into a running program.",
    input_schema: object(
      { command: str("The command line, as you would type it. No newlines."), shell_id: SHELL },
      ["command"],
    ),
    describe: (input) => {
      try {
        const cmd = typeof input.command === "string" ? input.command : ""
        const id = typeof input.shell_id === "string" ? input.shell_id : activeId()
        if (/[\r\n]/.test(cmd)) return "Refused when run: the command has more than one line"
        const named = typeof input.shell_id === "string" && input.shell_id
        const where = folders.get(id)
        const place = where ? ` in ${where}` : named ? ` in shell ${id}` : " in the active shell"
        return `Run \`${cmd.length > 80 ? `${cmd.slice(0, 79)}…` : cmd}\`${place}`
      } catch {
        return "Run a command in the terminal"
      }
    },
    run: async (input, desk, ctx: ToolContext) => {
      const command = typeof input.command === "string" ? input.command : ""
      if (!command.trim()) throw new Error("command is empty.")
      if (/[\r\n]/.test(command))
        throw new Error("command has a newline. Send one line at a time (join steps with && or ;).")
      if ([...command].some((c) => c.charCodeAt(0) < 32 || c.charCodeAt(0) === 127))
        throw new Error("command has control characters; only plain text can be typed.")
      const s = await pick(input)
      const st = await status(s.id)
      if (st.busy)
        throw new Error(
          `Shell ${s.id} is busy: ${st.program || "a program"} is running in it. Nothing was typed. Wait, use another shell, or open one with terminal_open.`,
        )
      show(desk)
      const t = await mounted(s.id)
      if (!t) throw notMounted(s.id)
      if (ctx.signal.aborted) throw new Error(STOPPED)
      const buf = t.buffer.active
      const start = buf.baseY + buf.cursorY
      let last = Date.now()
      const sub = t.onWriteParsed(() => {
        last = Date.now()
      })
      const began = Date.now()
      t.input(`${command}\r`, true)
      let ended: "quiet" | "time" | "stopped" = "time"
      try {
        while (Date.now() - began < RUN_LIMIT_MS) {
          await new Promise((r) => setTimeout(r, 100))
          if (ctx.signal.aborted) {
            ended = "stopped"
            break
          }
          if (Date.now() - last >= QUIET_MS && Date.now() - began >= QUIET_MS) {
            ended = "quiet"
            break
          }
        }
      } finally {
        sub.dispose()
      }
      // The output after the command line (its echo, and wrapped parts of it, skipped).
      let from = start + 1
      while (from < buf.length && buf.getLine(from)?.isWrapped) from++
      const after = await status(s.id).catch(() => null)
      // Back at the prompt: the cursor's line is the new prompt, not output.
      const to = after && !after.busy ? buf.baseY + buf.cursorY : buf.length
      const out = bounded(linesOf(t, from, to))
      return {
        shell_id: s.id,
        command,
        ended_by:
          ended === "quiet"
            ? "output quiet for 1.5 s"
            : ended === "time"
              ? "30 s limit (it may still be running)"
              : "the run was stopped",
        ...(after?.busy ? { still_running: after.program || true } : {}),
        ...(after?.folder ? { folder: after.folder } : {}),
        output: out.text,
        ...(out.note ? { note: out.note } : {}),
      }
    },
  },
])
