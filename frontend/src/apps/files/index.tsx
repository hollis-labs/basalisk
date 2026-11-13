/**
 * Files: the folders /api/files serves, local and over SFTP. Opening a file hands it to the
 * app registered for it (desk.openFile); without one, the preview pane shows it.
 */
import { FileText, Folder, FolderOpen, Server } from "lucide-react"
import { files } from "../../api/files"
import {
  type DeskAPI,
  registerApp,
  registerCommands,
  registerProvider,
} from "../../desktop/registry"
import { FilesBrowser, loadRoots } from "./browser"
import { baseOf, lastLoc, parentOf, recents } from "./model"
import "./agent"
import "./files.css"

registerApp({
  id: "files",
  title: "Files",
  icon: FolderOpen,
  menu: true,
  size: [1080, 660],
  bare: true,
  multi: true,
  render: ({ winId, args }) => <FilesBrowser winId={winId} args={args} />,
})

registerCommands([
  {
    id: "files.open",
    title: "Files",
    section: "Files",
    icon: FolderOpen,
    keywords: "finder browse folders",
    keys: "ctrl+alt+e",
    run: (desk) => desk.open("files", { key: "main" }),
  },
  {
    id: "files.new-window",
    title: "Files: new window",
    section: "Files",
    icon: FolderOpen,
    run: (desk) => desk.open("files"),
  },
])

const match = (q: string, ...hay: (string | undefined)[]) => {
  const words = q.toLowerCase().split(/\s+/).filter(Boolean)
  const text = hay.join(" ").toLowerCase()
  return words.every((w) => text.includes(w))
}

registerProvider({
  id: "files",
  async search(query) {
    const q = query.trim()
    if (!q) return []
    let roots: Awaited<ReturnType<typeof loadRoots>> = []
    try {
      roots = await loadRoots()
    } catch {
      return []
    }
    const label = (id: string) => roots.find((r) => r.id === id)?.label ?? id
    const places = roots
      .filter((r) => match(q, r.label, r.path, r.host, "files"))
      .map((r) => ({
        id: `files:root:${r.id}`,
        title: r.label,
        subtitle: r.kind === "sftp" ? `${r.host}:${r.path}` : r.path,
        section: "Places",
        icon: r.kind === "sftp" ? Server : Folder,
        run: (desk: DeskAPI) =>
          desk.open("files", { args: { root: r.id, path: "" }, key: `root:${r.id}` }),
      }))
    const known = new Set(roots.map((r) => r.id))
    const recent = recents()
      .filter((x) => known.has(x.root) && match(q, x.path, label(x.root)))
      .slice(0, 8)
      .map((x) => ({
        id: `files:recent:${x.root}:${x.path}`,
        title: baseOf(x.path) || label(x.root),
        subtitle: `${label(x.root)} / ${x.path}`,
        section: "Recent files",
        icon: x.dir ? Folder : FileText,
        run: (desk: DeskAPI) => {
          if (!x.dir && desk.openFile({ root: x.root, path: x.path })) return
          desk.open("files", {
            args: x.dir
              ? { root: x.root, path: x.path }
              : { root: x.root, path: parentOf(x.path), select: x.path },
          })
        },
      }))
    return [...places, ...recent]
  },
})

// ⌘K finds a file by name in the place last open in Files (recursive, on the server).
registerProvider({
  id: "files-find",
  async search(query) {
    const q = query.trim()
    if (q.length < 3) return []
    let roots: Awaited<ReturnType<typeof loadRoots>> = []
    try {
      roots = await loadRoots()
    } catch {
      return []
    }
    const last = lastLoc()
    const root = roots.find((r) => r.id === last?.root) ?? roots[0]
    if (!root) return []
    const res = await files.search(root.id, "", q).catch(() => null)
    if (!res) return []
    return res.entries.slice(0, 8).map((e) => ({
      id: `files:file:${root.id}:${e.path}`,
      title: e.name,
      subtitle: `${root.label} / ${parentOf(e.path) || "."}`,
      section: `Files in ${root.label}`,
      icon: e.dir ? Folder : FileText,
      run: (desk: DeskAPI) => {
        if (e.dir) {
          desk.open("files", { args: { root: root.id, path: e.path } })
          return
        }
        if (desk.openFile({ root: root.id, path: e.path })) return
        desk.open("files", { args: { root: root.id, path: parentOf(e.path), select: e.path } })
      },
    }))
  },
})
