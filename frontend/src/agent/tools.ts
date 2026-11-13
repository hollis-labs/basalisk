/**
 * The tools the desktop gives the agent. The page declares them with each message and runs
 * them itself when the model calls one (the server only relays: internal/agent). So a tool
 * lives in one place, with the app it belongs to: its description, its level and what it does.
 *
 * Levels: "read" looks and runs at once. "act" changes what is on screen and is undone as
 * easily (open a window); it runs at once too. "write" changes data or reaches outside, and
 * the owner is asked first (once, for this session, or always). "confirm" is asked every
 * time, with no standing answer: for a tool whose calls differ too much for one answer to
 * cover the next (running a command).
 */
import { allApps, allCommands, type DeskAPI } from "../desktop/registry"
import type { DeskSnapshot, SnapshotWindow, ToolSpec } from "./api"

/** What a running call is given besides its input. */
export interface ToolContext {
  /**
   * Aborted when the run has stopped waiting for this call (the owner pressed Stop, or the
   * run gave up). A tool that changes something after a wait (a question to the owner, a
   * slow load) must check `signal.aborted` first and not make the change.
   */
  signal: AbortSignal
}

export interface AgentTool extends ToolSpec {
  /**
   * One plain line saying what this call will do, for the permission question and the
   * call's row ("Click “Send” on example.com"). Without it the raw input is shown.
   */
  describe?: (input: Record<string, unknown>) => string
  /** Returns what the model is told. Throw to report a failure. */
  run: (
    input: Record<string, unknown>,
    desk: DeskAPI,
    ctx: ToolContext,
  ) => unknown | Promise<unknown>
}

const tools = new Map<string, AgentTool>()

/** Offer the agent a tool. Call at import time, like registerCommands. */
export function registerAgentTools(list: AgentTool[]) {
  for (const t of list) tools.set(t.name, t)
}

export const toolSpecs = (): ToolSpec[] =>
  [...tools.values()].map(({ name, description, input_schema, level }) => ({
    name,
    description,
    input_schema,
    level,
  }))

/** The call in a line, when its tool can say (AgentTool.describe). */
export function describeCall(name: string, input: unknown): string {
  try {
    return tools.get(name)?.describe?.((input ?? {}) as Record<string, unknown>) ?? ""
  } catch {
    return ""
  }
}

/** Run a call from the model. The answer is text: JSON for anything that is not a string. */
export async function runTool(
  name: string,
  input: unknown,
  desk: DeskAPI,
  signal: AbortSignal = new AbortController().signal,
): Promise<{ content: string; isError: boolean }> {
  const tool = tools.get(name)
  if (!tool) return { content: `The desktop has no tool named "${name}".`, isError: true }
  try {
    const out = await tool.run((input ?? {}) as Record<string, unknown>, desk, { signal })
    return {
      content: typeof out === "string" ? out : JSON.stringify(out ?? "done"),
      isError: false,
    }
  } catch (err) {
    return { content: err instanceof Error ? err.message : String(err), isError: true }
  }
}

/** The desktop as the agent is shown it with each message: the focused window, then all. */
export async function snapshot(desk: DeskAPI): Promise<DeskSnapshot> {
  const st = desk.wmState()
  const windows: SnapshotWindow[] = st.wins.map((w) => ({
    winId: w.id,
    app: w.app,
    title: w.title,
  }))
  let focused: SnapshotWindow | null = null
  try {
    const ctx = await desk.context()
    if (ctx) {
      focused = {
        winId: ctx.winId,
        app: ctx.app,
        title: ctx.title,
        file: ctx.file,
        url: ctx.url,
        cwd: ctx.cwd,
        selection: ctx.selection,
        extra: ctx.extra,
      }
    }
  } catch {
    // The window could not describe itself: the list still says it is there.
  }
  return { focused, windows }
}

const object = (properties: Record<string, unknown>, required: string[] = []) => ({
  type: "object",
  properties,
  required,
  additionalProperties: false,
})
const str = (description: string) => ({ type: "string", description })

registerAgentTools([
  {
    name: "desktop_windows",
    level: "read",
    description:
      "List the windows open on the desktop: id, app, title, and which has focus. The <desktop> block of the last message may be out of date; this is current.",
    input_schema: object({}),
    run: (_in, desk) => {
      const st = desk.wmState()
      return {
        layout: st.layout,
        focused: st.focus ?? null,
        windows: st.wins.map((w) => ({ id: w.id, app: w.app, title: w.title })),
      }
    },
  },
  {
    name: "window_context",
    level: "read",
    description:
      "What one window is showing, as its app describes it: the file or folder, the page address, the terminal's folder, and the text selected in it. Leave out window_id for the focused window.",
    input_schema: object({ window_id: str("A window id from desktop_windows.") }),
    run: async (input, desk) => {
      const id = typeof input.window_id === "string" ? input.window_id : undefined
      const ctx = await desk.context(id)
      if (!ctx) throw new Error(id ? `There is no window ${id}.` : "No window has focus.")
      return ctx
    },
  },
  {
    name: "desktop_apps",
    level: "read",
    description:
      "List the apps that can be opened and the commands that can be run (id and title). Commands are the same actions the owner has in the command palette.",
    input_schema: object({}),
    run: (_in, desk) => ({
      apps: allApps().map((a) => ({ id: a.id, title: a.title })),
      commands: allCommands()
        .filter((c) => !c.id.startsWith("app.open.") && (!c.when || c.when(desk)))
        .map((c) => ({ id: c.id, title: c.title, section: c.section })),
    }),
  },
  {
    name: "desktop_open",
    level: "act",
    description:
      "Open an app in a window, or bring its window forward. Optionally a file for the app to open (a root id and a path inside it, as window_context reports files) or a web address for the browser.",
    input_schema: object(
      {
        app: str("An app id from desktop_apps, for example notes, files, browser, terminal."),
        file_root: str("With file_path: the root the file is in."),
        file_path: str("With file_root: the file's path inside the root."),
        url: str("For the browser: the address to open."),
      },
      ["app"],
    ),
    run: (input, desk) => {
      const app = String(input.app ?? "")
      if (!allApps().some((a) => a.id === app)) throw new Error(`There is no app "${app}".`)
      if (typeof input.file_root === "string" && typeof input.file_path === "string") {
        const ref = { root: input.file_root, path: input.file_path }
        if (!desk.openFile(ref)) throw new Error("No app here opens that kind of file.")
        return `Opened ${ref.root}:${ref.path}.`
      }
      const args = typeof input.url === "string" ? { url: input.url } : undefined
      desk.open(app, args ? { args } : undefined)
      return `Opened ${app}.`
    },
  },
  {
    name: "desktop_focus",
    level: "act",
    description: "Bring a window to the front and give it focus.",
    input_schema: object({ window_id: str("A window id from desktop_windows.") }, ["window_id"]),
    run: (input, desk) => {
      const id = String(input.window_id ?? "")
      if (!desk.wmState().wins.some((w) => w.id === id))
        throw new Error(`There is no window ${id}.`)
      desk.wm({ type: "focus", id })
      return "Focused."
    },
  },
  {
    name: "desktop_run_command",
    level: "write",
    description:
      "Run a desktop command by id (from desktop_apps), exactly as if the owner chose it in the command palette. It acts on the focused window where that matters.",
    input_schema: object({ command: str("The command id.") }, ["command"]),
    describe: (input) => {
      const cmd = allCommands().find((c) => c.id === input.command)
      return cmd ? `Run “${cmd.title}”` : ""
    },
    run: async (input, desk) => {
      const id = String(input.command ?? "")
      const cmd = allCommands().find((c) => c.id === id)
      if (!cmd) throw new Error(`There is no command "${id}".`)
      if (cmd.when && !cmd.when(desk)) throw new Error(`"${cmd.title}" is not available right now.`)
      await cmd.run(desk)
      return `Ran "${cmd.title}".`
    },
  },
])
