/**
 * The agent's tools for files, read-only: the places, a folder's entries, a text file's
 * content, search by name and by content, and opening something on screen. What the agent
 * reads goes to the model's provider, so files that look like credentials are never read.
 */
import { registerAgentTools } from "../../agent/tools"
import { ApiError, request } from "../../api/account"
import { type FileEntry, type FileRoot, files } from "../../api/files"
import type { DeskAPI } from "../../desktop/registry"
import { loadRoots } from "./browser"
import { credentialReason } from "./credentials"
import { filesLocations } from "./model"

const READ_CHUNK = 60_000
const READ_MAX_BYTES = 5 << 20

// ---- What must not be read: credentials.ts ----------------------------------------------

const refused = (root: string, path: string, why: string) =>
  new Error(
    `${root}:${path} looks like a credential, by ${why}, so it is not read: what the agent reads goes to the model's provider. If something in it is needed, ask the owner to paste just that part.`,
  )

// ---- Helpers ------------------------------------------------------------------------------

const object = (properties: Record<string, unknown>, required: string[] = []) => ({
  type: "object",
  properties,
  required,
  additionalProperties: false,
})
const str = (description: string) => ({ type: "string", description })
const WHERE = {
  root: str("A place id from files_places."),
  path: str(
    "A path inside the place ('' for its top). Leave root and path out for the folder the focused Files window shows.",
  ),
}
const int = (v: unknown, def: number, lo: number, hi: number) =>
  typeof v === "number" && Number.isFinite(v) ? Math.max(lo, Math.min(hi, Math.floor(v))) : def

async function place(id: string): Promise<FileRoot> {
  const roots = await loadRoots()
  const r = roots.find((x) => x.id === id)
  if (!r)
    throw new Error(
      `There is no place "${id}". Places: ${roots.map((x) => x.id).join(", ") || "none"}.`,
    )
  return r
}

/** The place and path a call means; without them, the folder a Files window shows. */
async function where(
  input: Record<string, unknown>,
  desk: DeskAPI,
  needPath: boolean,
): Promise<{ root: FileRoot; path: string }> {
  const path =
    typeof input.path === "string" ? input.path.trim().replace(/^\/+|\/+$/g, "") : undefined
  if (typeof input.root === "string" && input.root) {
    if (needPath && !path) throw new Error("path is empty: give the file's path inside the place.")
    return { root: await place(input.root), path: path ?? "" }
  }
  if (needPath)
    throw new Error("Give root and path. files_places lists the places; files_list a folder.")
  const st = desk.wmState()
  const wins = st.wins.filter((w) => w.app === "files")
  const w = wins.find((x) => x.id === st.focus) ?? [...wins].sort((a, b) => b.z - a.z)[0]
  if (!w)
    throw new Error("No Files window is open: give root and path (files_places lists the places).")
  // A window just opened says where it is once it has loaded.
  let loc = filesLocations.get(w.id)
  for (let i = 0; !loc && i < 20; i++) {
    await new Promise((r) => setTimeout(r, 100))
    loc = filesLocations.get(w.id)
  }
  if (!loc) throw new Error(`Files window ${w.id} has not loaded a folder yet: give root and path.`)
  return { root: await place(loc.root), path: path ?? loc.path }
}

const apiError = (e: unknown, root: string, path: string) => {
  if (e instanceof ApiError) {
    if (e.status === 404) return new Error(`There is nothing at ${root}:${path}.`)
    if (e.status === 403) return new Error(`${root}:${path} is outside the place.`)
  }
  return e instanceof Error ? e : new Error(String(e))
}

const entryOut = (e: FileEntry) => ({
  name: e.name,
  path: e.path,
  type: e.dir ? "folder" : e.symlink ? "link" : "file",
  ...(e.dir ? {} : { size: e.size }),
  modified: e.mtime,
})

// ---- The tools ------------------------------------------------------------------------------

registerAgentTools([
  {
    name: "files_places",
    level: "read",
    description:
      "List the places files can be in: id, label, and whether it is on this machine or a remote one.",
    input_schema: object({}),
    run: async () => ({
      places: (await loadRoots()).map((r) => ({
        id: r.id,
        label: r.label,
        kind: r.kind === "local" ? "local" : "remote",
      })),
    }),
  },
  {
    name: "files_list",
    level: "read",
    description:
      "List a folder: each entry's name, path, type, size and when it was modified, folders first. Without root and path, the folder the focused Files window shows.",
    input_schema: object({
      ...WHERE,
      max: { type: "integer", description: "At most this many entries (default 200, up to 1000)." },
    }),
    run: async (input, desk) => {
      const { root, path } = await where(input, desk, false)
      const max = int(input.max, 200, 1, 1000)
      let listing: Awaited<ReturnType<typeof files.list>>
      try {
        listing = await files.list(root.id, path)
      } catch (e) {
        throw apiError(e, root.id, path)
      }
      const sorted = [...listing.entries].sort(
        (a, b) => Number(b.dir) - Number(a.dir) || a.name.localeCompare(b.name),
      )
      return {
        root: root.id,
        path,
        total: sorted.length,
        entries: sorted.slice(0, max).map(entryOut),
        ...(sorted.length > max
          ? {
              note: `showing ${max} of ${sorted.length}; raise max (up to 1000) or use files_search`,
            }
          : {}),
      }
    },
  },
  {
    name: "files_read",
    level: "read",
    description: `Read a text file, ${READ_CHUNK} characters at a time (use offset to read on). Binary files and files over 5 MB are refused, and so are files that look like credentials (keys, tokens, .env, ~/.ssh and the like).`,
    input_schema: object(
      {
        root: str("A place id from files_places."),
        path: str("The file's path inside the place."),
        offset: { type: "integer", description: "Start this many characters in." },
      },
      ["root", "path"],
    ),
    run: async (input, desk) => {
      const { root, path } = await where(input, desk, true)
      const why = credentialReason(path, root.path)
      if (why) throw refused(root.id, path, why)
      let st: FileEntry
      try {
        st = await files.stat(root.id, path)
      } catch (e) {
        throw apiError(e, root.id, path)
      }
      if (st.dir) throw new Error(`${root.id}:${path} is a folder; use files_list.`)
      if (st.size > READ_MAX_BYTES)
        throw new Error(
          `${root.id}:${path} is ${Math.round(st.size / 1048576)} MB; files over 5 MB are not read. files_grep can search it.`,
        )
      const r = await fetch(files.contentURL(root.id, path), { credentials: "same-origin" })
      if (!r.ok) throw new Error(`${root.id}:${path} could not be read (${r.status}).`)
      const bytes = new Uint8Array(await r.arrayBuffer())
      if (bytes.subarray(0, 8192).includes(0))
        throw new Error(`${root.id}:${path} is a binary file, not text.`)
      const text = new TextDecoder().decode(bytes)
      const offset = int(input.offset, 0, 0, Number.MAX_SAFE_INTEGER)
      const part = text.slice(offset, offset + READ_CHUNK)
      return {
        root: root.id,
        path,
        length: text.length,
        ...(offset ? { offset } : {}),
        text: part,
        ...(offset + READ_CHUNK < text.length
          ? { note: `cut at ${READ_CHUNK} characters; read on with offset ${offset + READ_CHUNK}` }
          : {}),
      }
    },
  },
  {
    name: "files_search",
    level: "read",
    description:
      "Find files and folders by name under a folder (every word must be in the name, case-insensitive). Without root and path, under the folder the focused Files window shows.",
    input_schema: object({ ...WHERE, query: str("Words of the name.") }, ["query"]),
    run: async (input, desk) => {
      const q = typeof input.query === "string" ? input.query.trim() : ""
      if (!q) throw new Error("query is empty: give words of the name.")
      const { root, path } = await where(input, desk, false)
      let r: Awaited<ReturnType<typeof files.search>>
      try {
        r = await files.search(root.id, path, q)
      } catch (e) {
        throw apiError(e, root.id, path)
      }
      return {
        root: root.id,
        path,
        total: r.entries.length,
        entries: r.entries.map(entryOut),
        ...(r.truncated
          ? { note: "the search stopped early; narrow the folder or the words" }
          : {}),
      }
    },
  },
  {
    name: "files_grep",
    level: "read",
    description:
      "Find lines containing text (literal, case-insensitive) in the files under a folder: path, line number and the line. At most 5 lines a file. Files that look like credentials are left out. Without root and path, under the folder the focused Files window shows.",
    input_schema: object(
      {
        ...WHERE,
        query: str("The text to find."),
        max: { type: "integer", description: "At most this many lines (default 100, up to 200)." },
      },
      ["query"],
    ),
    run: async (input, desk) => {
      const q = typeof input.query === "string" ? input.query.trim() : ""
      if (!q) throw new Error("query is empty: give the text to find.")
      const { root, path } = await where(input, desk, false)
      const max = int(input.max, 100, 1, 200)
      let r: {
        matches: { path: string; line: number; text: string }[]
        truncated: boolean
        engine: string
      }
      try {
        r = await request(
          `files/grep?root=${encodeURIComponent(root.id)}&path=${encodeURIComponent(path)}&q=${encodeURIComponent(q)}`,
        )
      } catch (e) {
        throw apiError(e, root.id, path)
      }
      const kept = r.matches.filter((m) => !credentialReason(m.path, root.path))
      const hidden = new Set(
        r.matches.filter((m) => credentialReason(m.path, root.path)).map((m) => m.path),
      )
      return {
        root: root.id,
        path,
        total: kept.length,
        matches: kept.slice(0, max),
        ...(hidden.size
          ? {
              left_out: `${r.matches.length - kept.length} line(s) in ${hidden.size} file(s) that look like credentials`,
            }
          : {}),
        ...(r.truncated || kept.length > max
          ? { note: "stopped early; narrow the folder or the text" }
          : {}),
      }
    },
  },
  {
    name: "files_open",
    level: "act",
    description:
      "Show a file in the app that opens it (Notes for text, the browser for pages, …) or a folder in a Files window.",
    input_schema: object(
      { root: str("A place id from files_places."), path: str("The path inside the place.") },
      ["root", "path"],
    ),
    describe: (input) => {
      try {
        const p = typeof input.path === "string" ? input.path : ""
        return `Open ${String(input.root ?? "")}:${p.length > 80 ? `…${p.slice(-79)}` : p}`
      } catch {
        return "Open a file"
      }
    },
    run: async (input, desk) => {
      const { root, path } = await where(input, desk, false)
      let st: FileEntry | null = null
      if (path) {
        try {
          st = await files.stat(root.id, path)
        } catch (e) {
          throw apiError(e, root.id, path)
        }
      }
      if (!st || st.dir) {
        desk.open("files", { args: { root: root.id, path } })
        return `Opened ${root.id}:${path || "/"} in Files.`
      }
      if (!desk.openFile({ root: root.id, path }))
        throw new Error(`No app here opens ${st.name}; files_open the folder instead.`)
      return `Opened ${root.id}:${path}.`
    },
  },
])
