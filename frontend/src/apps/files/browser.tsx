import {
  ArrowLeft,
  ArrowRight,
  ArrowUp,
  ChevronRight,
  Code,
  Download,
  Eye,
  EyeOff,
  FileText,
  FolderKanban,
  HardDrive,
  House,
  Link2Off,
  type LucideIcon,
  PanelRight,
  RefreshCw,
  Search,
  Server,
  SquareTerminal,
} from "lucide-react"
import {
  type KeyboardEvent as ReactKeyboardEvent,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react"
import { type FileEntry, type FileRoot, files } from "../../api/files"
import { useDesk } from "../../desktop/context"
import { adoptTerminal } from "../../terminal/deck"
import * as act from "./actions"
import { ContextMenu, type MenuItem, NameEditor } from "./menu"
import {
  baseOf,
  cacheKey,
  filesLocations,
  formatSize,
  formatTime,
  iconOf,
  type Loc,
  lastLoc,
  listingCache,
  parentOf,
  prefs,
  remember,
  setLastLoc,
} from "./model"
import { CHANGED_EVENT, CLIP_EVENT, DRAG_TYPE, dragItems, getClip } from "./ops"
import { Preview } from "./preview"

const ROW_H = 26
const OVERSCAN = 12

const ROOT_ICONS: Record<string, LucideIcon> = {
  home: House,
  dev: Code,
  downloads: Download,
  documents: FileText,
  projects: FolderKanban,
}
const rootIcon = (r: FileRoot) => (r.kind === "sftp" ? Server : (ROOT_ICONS[r.id] ?? HardDrive))

let rootsPromise: Promise<FileRoot[]> | null = null
/** The roots, fetched once per page. */
export function loadRoots(): Promise<FileRoot[]> {
  rootsPromise ??= files
    .roots()
    .then((r) => r.roots)
    .catch((e) => {
      rootsPromise = null
      throw e
    })
  return rootsPromise
}

type Listing =
  | { state: "loading"; entries: FileEntry[] }
  | { state: "ready"; entries: FileEntry[] }
  | { state: "error"; entries: FileEntry[]; message: string; status: number }

function errorOf(e: unknown): { message: string; status: number } {
  const status = typeof e === "object" && e && "status" in e ? Number(e.status) : 0
  return { message: e instanceof Error ? e.message : "Could not list this folder.", status }
}

export function FilesBrowser({ winId, args }: { winId: string; args: Record<string, unknown> }) {
  const desk = useDesk()
  const [roots, setRoots] = useState<FileRoot[] | null>(null)
  const [rootsError, setRootsError] = useState("")
  const [loc, setLoc] = useState<Loc | null>(null)
  const [back, setBack] = useState<Loc[]>([])
  const [fwd, setFwd] = useState<Loc[]>([])
  const [listing, setListing] = useState<Listing>({ state: "loading", entries: [] })
  const [slow, setSlow] = useState(false)
  const [filter, setFilter] = useState("")
  const [hidden, setHidden] = useState(prefs.hidden)
  const [preview, setPreview] = useState(prefs.preview)
  const [sel, setSel] = useState<string | null>(null)
  // Multi-select: the picked paths (empty means just the cursor, sel), and the shift anchor.
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const anchor = useRef<string | null>(null)
  // A name being typed: a rename of a row, or a new folder or file.
  const [editing, setEditing] = useState<{
    mode: "rename" | "folder" | "file"
    path?: string
  } | null>(null)
  const [menu, setMenu] = useState<{ x: number; y: number; on: FileEntry | null } | null>(null)
  const [dropOn, setDropOn] = useState<string | null>(null)
  const [, setClipTick] = useState(0)
  const [notice, setNotice] = useState("")
  const [reload, setReload] = useState(0)
  const [hasTerminal, setHasTerminal] = useState(false)
  const listRef = useRef<HTMLDivElement>(null)
  const crumbsRef = useRef<HTMLDivElement>(null)
  const rootRef = useRef<HTMLDivElement>(null)
  const [scrollTop, setScrollTop] = useState(0)
  const [viewH, setViewH] = useState(400)
  // A selection to make once the next listing arrives (the folder we came up from).
  const pendingSel = useRef<string | null>(null)

  // ---- Roots, and where to start. ----
  useEffect(() => {
    let live = true
    loadRoots()
      .then((rs) => {
        if (!live) return
        setRoots(rs)
        const want = args as Partial<Loc> & { select?: string }
        const last = lastLoc()
        const start =
          (want.root &&
            rs.find((r) => r.id === want.root) && { root: want.root, path: want.path ?? "" }) ||
          (last && rs.find((r) => r.id === last.root) && last) ||
          (rs[0] && { root: rs[0].id, path: "" })
        if (start) {
          // select is a name in the folder ("notes.md") or a root-relative path.
          if (want.select)
            pendingSel.current = want.select.includes("/")
              ? want.select
              : [start.path, want.select].filter(Boolean).join("/")
          setLoc(start)
        }
      })
      .catch((e) => live && setRootsError(errorOf(e).message))
    void files
      .state()
      .then((s) => live && setHasTerminal(!!s.terminal))
      .catch(() => undefined)
    return () => {
      live = false
    }
  }, [args])

  const root = useMemo(() => roots?.find((r) => r.id === loc?.root) ?? null, [roots, loc])

  // ---- Listing: the cached copy at once, then a fresh one. ----
  // biome-ignore lint/correctness/useExhaustiveDependencies: reload is the "fetch again" trigger
  useEffect(() => {
    if (!loc) return
    let live = true
    const cached = listingCache.get(cacheKey(loc))
    setListing({ state: cached ? "ready" : "loading", entries: cached?.entries ?? [] })
    setSlow(false)
    const slowTimer = window.setTimeout(() => live && setSlow(true), 250)
    files
      .list(loc.root, loc.path)
      .then((l) => {
        if (!live) return
        listingCache.set(cacheKey(loc), l)
        setListing({ state: "ready", entries: l.entries })
      })
      .catch((e) => {
        if (live) setListing({ state: "error", entries: cached?.entries ?? [], ...errorOf(e) })
      })
      .finally(() => window.clearTimeout(slowTimer))
    setLastLoc(loc)
    filesLocations.set(winId, loc)
    return () => {
      live = false
      window.clearTimeout(slowTimer)
    }
  }, [loc, reload, winId])

  useEffect(() => () => void filesLocations.delete(winId), [winId])

  // ---- Title follows the location. ----
  useEffect(() => {
    if (!loc || !root) return
    desk.setTitle(winId, loc.path ? `${baseOf(loc.path)} · ${root.label}` : root.label)
  }, [desk, winId, loc, root])

  // ---- What is shown. ----
  const shown = useMemo(() => {
    const f = filter.toLowerCase()
    return listing.entries.filter(
      (e) =>
        (hidden || !e.name.startsWith(".") || f.startsWith(".")) &&
        (!f || e.name.toLowerCase().includes(f)),
    )
  }, [listing.entries, hidden, filter])

  // Keep a selection: the pending one, the current one if still shown, else the first.
  useEffect(() => {
    if (listing.state === "loading" && !listing.entries.length) return
    setSel((cur) => {
      const want = pendingSel.current
      if (want && shown.some((e) => e.path === want)) {
        pendingSel.current = null
        return want
      }
      if (cur && shown.some((e) => e.path === cur)) return cur
      return shown[0]?.path ?? null
    })
  }, [shown, listing])

  const index = shown.findIndex((e) => e.path === sel)
  const selected = index >= 0 ? shown[index] : null
  // What an operation acts on: the picked rows, else the cursor's.
  const targets = (): FileEntry[] => {
    const many = shown.filter((e) => picked.has(e.path))
    return many.length ? many : selected ? [selected] : []
  }

  // Another window (or this one) changed a folder this window shows: read it again.
  useEffect(() => {
    const on = (e: Event) => {
      const d = (e as CustomEvent<{ root: string; folders: string[] }>).detail
      if (loc && d.root === loc.root && d.folders.includes(loc.path)) setReload((r) => r + 1)
    }
    const clip = () => setClipTick((t) => t + 1)
    window.addEventListener(CHANGED_EVENT, on)
    window.addEventListener(CLIP_EVENT, clip)
    return () => {
      window.removeEventListener(CHANGED_EVENT, on)
      window.removeEventListener(CLIP_EVENT, clip)
    }
  }, [loc])
  // A new folder clears the picks.
  // biome-ignore lint/correctness/useExhaustiveDependencies: on a change of folder only
  useEffect(() => {
    setPicked(new Set())
    anchor.current = null
    setEditing(null)
  }, [loc])

  // ---- Scrolling: keep the selection in view; render only the visible rows. ----
  useLayoutEffect(() => {
    const el = listRef.current
    if (!el) return
    const ro = new ResizeObserver(() => setViewH(el.clientHeight))
    ro.observe(el)
    setViewH(el.clientHeight)
    return () => ro.disconnect()
  }, [])
  useLayoutEffect(() => {
    const el = listRef.current
    if (!el || index < 0) return
    const top = index * ROW_H
    if (top < el.scrollTop) el.scrollTop = top
    else if (top + ROW_H > el.scrollTop + el.clientHeight)
      el.scrollTop = top + ROW_H - el.clientHeight
  }, [index])
  const first = Math.max(0, Math.floor(scrollTop / ROW_H) - OVERSCAN)
  const last = Math.min(shown.length, Math.ceil((scrollTop + viewH) / ROW_H) + OVERSCAN)

  // A long path keeps its end in view.
  useLayoutEffect(() => {
    const el = crumbsRef.current
    if (!el) return
    el.scrollLeft = el.scrollWidth
    el.dataset.overflow = String(el.scrollWidth > el.clientWidth)
  })

  // ---- Focus: the list takes the keyboard when the window is focused. ----
  // biome-ignore lint/correctness/useExhaustiveDependencies: the list exists once roots load
  useEffect(() => {
    const win = rootRef.current?.closest<HTMLElement>("[data-win]")
    if (!win) return
    const onFocus = (e: FocusEvent) => {
      if (e.target === win) listRef.current?.focus({ preventScroll: true })
    }
    win.addEventListener("focus", onFocus)
    // The list renders once the roots arrive; by then the desktop may already have focused
    // the window, so take it now.
    if (document.activeElement === win || document.activeElement === document.body)
      listRef.current?.focus({ preventScroll: true })
    return () => win.removeEventListener("focus", onFocus)
  }, [roots])

  // ---- Navigation. ----
  const go = useCallback(
    (next: Loc, opts: { select?: string; history?: boolean } = {}) => {
      if (loc && next.root === loc.root && next.path === loc.path) return
      if (opts.history !== false && loc) {
        setBack((b) => [...b, loc].slice(-100))
        setFwd([])
      }
      pendingSel.current = opts.select ?? null
      setFilter("")
      setLoc(next)
    },
    [loc],
  )
  const goBack = useCallback(() => {
    if (!back.length || !loc) return
    const prev = back[back.length - 1]
    setBack((b) => b.slice(0, -1))
    setFwd((f) => [loc, ...f])
    pendingSel.current =
      loc.root === prev.root && parentOf(loc.path) === prev.path ? loc.path : null
    setFilter("")
    setLoc(prev)
  }, [back, loc])
  const goFwd = useCallback(() => {
    if (!fwd.length || !loc) return
    const next = fwd[0]
    setFwd((f) => f.slice(1))
    setBack((b) => [...b, loc])
    setFilter("")
    setLoc(next)
  }, [fwd, loc])
  const goUp = useCallback(() => {
    if (!loc?.path) return
    go({ root: loc.root, path: parentOf(loc.path) }, { select: loc.path })
  }, [go, loc])

  const flash = useCallback((msg: string) => {
    setNotice(msg)
    window.setTimeout(() => setNotice((n) => (n === msg ? "" : n)), 2600)
  }, [])

  const open = useCallback(
    (e: FileEntry) => {
      if (!loc) return
      if (e.broken) return flash("That link leads outside this root.")
      if (e.dir) return go({ root: loc.root, path: e.path })
      remember({ root: loc.root, path: e.path, dir: false })
      if (desk.openFile({ root: loc.root, path: e.path })) return
      // No app handles it yet: the preview is the viewer.
      setSel(e.path)
      if (!preview) {
        setPreview(true)
        prefs.preview = true
      }
    },
    [desk, flash, go, loc, preview],
  )

  const terminalHere = useCallback(async () => {
    if (!loc || !root) return
    if (root.kind !== "local") return flash("A terminal opens only in a folder on this machine.")
    try {
      const target = selected?.dir ? selected.path : loc.path
      const s = await files.terminal(loc.root, target)
      adoptTerminal(s)
      desk.open("terminal")
    } catch (e) {
      flash(errorOf(e).message)
    }
  }, [desk, flash, loc, root, selected])

  const copyPath = useCallback(() => {
    if (!root || !loc) return
    const rel = selected?.path ?? loc.path
    const full = rel ? `${root.path.replace(/\/$/, "")}/${rel}` : root.path
    const text = root.kind === "sftp" ? `${root.host}:${full}` : full
    void navigator.clipboard
      ?.writeText(text)
      .then(() => flash(`Copied ${text}`))
      .catch(() => flash("Could not copy."))
  }, [flash, loc, root, selected])

  const ctx = (): act.Ctx | null =>
    root && loc
      ? {
          desk,
          root,
          folder: loc.path,
          flash,
          select: (p: string) => {
            pendingSel.current = p
            setPicked(new Set())
          },
          refocus: () => listRef.current?.focus({ preventScroll: true }),
        }
      : null
  const run = (f: (c: act.Ctx) => void | Promise<void>) => {
    const c = ctx()
    if (c) void f(c)
  }
  const pick = (e: FileEntry, ev: { metaKey: boolean; shiftKey: boolean }) => {
    if (ev.shiftKey && anchor.current) {
      const a = shown.findIndex((x) => x.path === anchor.current)
      const b = shown.findIndex((x) => x.path === e.path)
      if (a >= 0 && b >= 0)
        setPicked(new Set(shown.slice(Math.min(a, b), Math.max(a, b) + 1).map((x) => x.path)))
    } else if (ev.metaKey) {
      setPicked((cur) => {
        const next = new Set(cur.size ? cur : sel ? [sel] : [])
        if (next.has(e.path)) next.delete(e.path)
        else next.add(e.path)
        return next
      })
      anchor.current = e.path
    } else {
      setPicked(new Set())
      anchor.current = e.path
    }
    setSel(e.path)
  }
  const menuItems = (on: FileEntry | null): (MenuItem | "-")[] => {
    const items = on ? targets() : []
    const one = items.length === 1 ? items[0] : null
    const clip = getClip()
    const pasteItem: MenuItem = {
      label: clip
        ? `Paste ${clip.items.length} item${clip.items.length === 1 ? "" : "s"}`
        : "Paste",
      keys: "⌘V",
      disabled: !clip,
      run: () => run(act.paste),
    }
    if (!on)
      return [
        { label: "New folder", keys: "⌘⇧N", run: () => setEditing({ mode: "folder" }) },
        { label: "New file", keys: "⌘N", run: () => setEditing({ mode: "file" }) },
        "-",
        pasteItem,
      ]
    return [
      { label: "Open", keys: "⌘O", disabled: !one, run: () => one && open(one) },
      {
        label: "Rename",
        keys: "↵",
        disabled: !one,
        run: () => one && setEditing({ mode: "rename", path: one.path }),
      },
      { label: "Duplicate", keys: "⌘D", run: () => run((c) => act.duplicate(c, items)) },
      "-",
      { label: "Copy", keys: "⌘C", run: () => root && act.copyItems(root.id, items, "copy") },
      { label: "Cut", keys: "⌘X", run: () => root && act.copyItems(root.id, items, "cut") },
      pasteItem,
      { label: "Copy path", keys: "⌘⌥C", disabled: !one, run: copyPath },
      "-",
      {
        label: root?.kind === "local" ? "Move to Trash" : "Delete…",
        keys: "⌘⌫",
        danger: true,
        run: () => run((c) => act.remove(c, items)),
      },
    ]
  }
  const dropTo = async (raw: string, dest: { root: string; path: string }, copy: boolean) => {
    let refs: { root: string; path: string; name: string; dir?: boolean }[]
    try {
      refs = JSON.parse(raw)
    } catch {
      return
    }
    // A folder is not dropped into itself.
    refs = refs.filter(
      (r) =>
        !(r.root === dest.root && (dest.path === r.path || dest.path.startsWith(`${r.path}/`))),
    )
    if (refs.length)
      await act.transfer(
        { desk, flash, refocus: () => listRef.current?.focus({ preventScroll: true }) },
        refs,
        dest,
        copy ? "copy" : "move",
      )
  }

  const toggleHidden = useCallback(() => {
    setHidden((h) => {
      prefs.hidden = !h
      return !h
    })
  }, [])
  const togglePreview = useCallback(() => {
    setPreview((p) => {
      prefs.preview = !p
      return !p
    })
  }, [])

  // ---- Keys. Typing filters; Escape clears; the rest moves and opens. ----
  const onKey = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    const mod = e.metaKey || e.ctrlKey
    const move = (to: number) => {
      if (!shown.length) return
      const i = Math.max(0, Math.min(shown.length - 1, to))
      setPicked(new Set())
      anchor.current = shown[i].path
      setSel(shown[i].path)
    }
    const page = Math.max(1, Math.floor(viewH / ROW_H) - 1)
    let handled = true
    const shift = (to: number) => {
      if (!shown.length) return
      const i = Math.max(0, Math.min(shown.length - 1, to))
      anchor.current ??= sel
      const a = shown.findIndex((x) => x.path === anchor.current)
      setPicked(new Set(shown.slice(Math.min(a, i), Math.max(a, i) + 1).map((x) => x.path)))
      setSel(shown[i].path)
    }
    const one = targets().length === 1 ? targets()[0] : null
    if (e.metaKey && e.key === "Backspace") run((c) => act.remove(c, targets()))
    else if (e.metaKey && e.shiftKey && e.code === "KeyN") setEditing({ mode: "folder" })
    else if (e.metaKey && !e.shiftKey && e.code === "KeyN") setEditing({ mode: "file" })
    else if (e.metaKey && e.code === "KeyD") run((c) => act.duplicate(c, targets()))
    else if (e.metaKey && !e.altKey && e.code === "KeyC" && root)
      act.copyItems(root.id, targets(), "copy")
    else if (e.metaKey && e.code === "KeyX" && root) act.copyItems(root.id, targets(), "cut")
    else if (e.metaKey && e.code === "KeyV") run(act.paste)
    else if (e.metaKey && e.code === "KeyA") setPicked(new Set(shown.map((x) => x.path)))
    else if (e.metaKey && e.code === "KeyO") selected && open(selected)
    else if ((e.key === "Enter" && !mod) || e.key === "F2") {
      if (one) setEditing({ mode: "rename", path: one.path })
    } else if (e.shiftKey && e.key === "ArrowDown") shift(index + 1)
    else if (e.shiftKey && e.key === "ArrowUp") shift(index - 1)
    else if (e.metaKey && e.key === "ArrowUp") goUp()
    else if (e.metaKey && e.key === "ArrowDown") selected && open(selected)
    else if ((e.metaKey && e.key === "[") || (e.altKey && e.key === "ArrowLeft")) goBack()
    else if ((e.metaKey && e.key === "]") || (e.altKey && e.key === "ArrowRight")) goFwd()
    else if (e.metaKey && e.shiftKey && (e.key === "." || e.key === ">")) toggleHidden()
    else if (e.metaKey && e.altKey && e.code === "KeyC") copyPath()
    else if (e.ctrlKey && !e.metaKey && e.code === "KeyT") void terminalHere()
    else if (e.metaKey && e.code === "KeyI") togglePreview()
    else if (e.key === "F5" || (e.metaKey && e.code === "KeyR")) setReload((r) => r + 1)
    else if (mod || e.altKey) handled = false
    else if (e.key === "ArrowDown") move(index + 1)
    else if (e.key === "ArrowUp") move(index - 1)
    else if (e.key === "PageDown") move(index + page)
    else if (e.key === "PageUp") move(index - page)
    else if (e.key === "Home") move(0)
    else if (e.key === "End") move(shown.length - 1)
    else if (e.key === "ArrowRight") selected?.dir && open(selected)
    else if (e.key === "ArrowLeft") goUp()
    else if (e.key === "Backspace") filter ? setFilter((f) => f.slice(0, -1)) : goUp()
    else if (e.key === "Escape") {
      if (filter) setFilter("")
      else if (picked.size) setPicked(new Set())
      else handled = false
    } else if (e.key === " " && !filter) togglePreview()
    else if (e.key.length === 1) setFilter((f) => f + e.key)
    else handled = false
    if (handled) {
      e.preventDefault()
      e.stopPropagation()
    }
  }

  // ---- Render. ----
  if (rootsError)
    return <p className="desk-note">The file browser is not available: {rootsError}</p>
  if (!roots) return <p className="desk-note">Opening Files…</p>
  if (!roots.length) return <p className="desk-note">No folders are configured.</p>

  const crumbs = loc?.path ? loc.path.split("/") : []
  const local = roots.filter((r) => r.kind === "local")
  const remote = roots.filter((r) => r.kind !== "local")

  return (
    <div className="files" ref={rootRef} data-preview={preview}>
      <nav className="files-side" aria-label="Places">
        {[
          ["Local", local],
          ["Remote", remote],
        ].map(([label, list]) =>
          (list as FileRoot[]).length ? (
            <div key={label as string} className="files-side-group">
              <div className="files-side-label">{label as string}</div>
              {(list as FileRoot[]).map((r) => {
                const Icon = rootIcon(r)
                return (
                  <button
                    key={r.id}
                    type="button"
                    tabIndex={-1}
                    data-active={r.id === loc?.root}
                    title={r.kind === "sftp" ? `${r.host}:${r.path}` : r.path}
                    data-drop={dropOn === `place:${r.id}`}
                    onDragOver={(ev) => {
                      if (ev.dataTransfer.types.includes(DRAG_TYPE)) {
                        ev.preventDefault()
                        ev.dataTransfer.dropEffect = ev.altKey ? "copy" : "move"
                        setDropOn(`place:${r.id}`)
                      }
                    }}
                    onDragLeave={() => setDropOn(null)}
                    onDrop={(ev) => {
                      setDropOn(null)
                      ev.preventDefault()
                      void dropTo(
                        ev.dataTransfer.getData(DRAG_TYPE),
                        { root: r.id, path: "" },
                        ev.altKey,
                      )
                    }}
                    onClick={() => {
                      go({ root: r.id, path: "" })
                      listRef.current?.focus()
                    }}
                  >
                    <Icon aria-hidden="true" size={15} strokeWidth={1.6} />
                    <span>{r.label}</span>
                  </button>
                )
              })}
            </div>
          ) : null,
        )}
      </nav>

      <section className="files-main">
        <div className="files-bar">
          <button
            type="button"
            tabIndex={-1}
            className="files-icon"
            title="Back (⌘[)"
            disabled={!back.length}
            onClick={goBack}
          >
            <ArrowLeft size={15} strokeWidth={1.6} aria-label="Back" />
          </button>
          <button
            type="button"
            tabIndex={-1}
            className="files-icon"
            title="Forward (⌘])"
            disabled={!fwd.length}
            onClick={goFwd}
          >
            <ArrowRight size={15} strokeWidth={1.6} aria-label="Forward" />
          </button>
          <button
            type="button"
            tabIndex={-1}
            className="files-icon"
            title="Up (⌘↑)"
            disabled={!loc?.path}
            onClick={goUp}
          >
            <ArrowUp size={15} strokeWidth={1.6} aria-label="Up" />
          </button>
          <div className="files-crumbs" ref={crumbsRef}>
            <button
              type="button"
              tabIndex={-1}
              onClick={() => loc && go({ root: loc.root, path: "" })}
            >
              {root?.label}
            </button>
            {crumbs.map((c, i) => {
              const p = crumbs.slice(0, i + 1).join("/")
              return (
                <span key={p} className="files-crumb">
                  <ChevronRight aria-hidden="true" size={12} strokeWidth={1.6} />
                  <button
                    type="button"
                    tabIndex={-1}
                    onClick={() => loc && go({ root: loc.root, path: p })}
                  >
                    {c}
                  </button>
                </span>
              )
            })}
          </div>
          {filter && (
            <span className="files-filter">
              <Search aria-hidden="true" size={12} strokeWidth={1.6} />
              {filter}
            </span>
          )}
          <button
            type="button"
            tabIndex={-1}
            className="files-icon"
            title="Hidden files (⌘⇧.)"
            data-on={hidden}
            onClick={toggleHidden}
          >
            {hidden ? (
              <Eye size={15} strokeWidth={1.6} aria-label="Hide hidden files" />
            ) : (
              <EyeOff size={15} strokeWidth={1.6} aria-label="Show hidden files" />
            )}
          </button>
          {hasTerminal && root?.kind === "local" && (
            <button
              type="button"
              tabIndex={-1}
              className="files-icon"
              title="Terminal here (⌃T)"
              onClick={() => void terminalHere()}
            >
              <SquareTerminal size={15} strokeWidth={1.6} aria-label="Open a terminal here" />
            </button>
          )}
          <button
            type="button"
            tabIndex={-1}
            className="files-icon"
            title="Reload (⌘R)"
            onClick={() => setReload((r) => r + 1)}
          >
            <RefreshCw size={15} strokeWidth={1.6} aria-label="Reload" />
          </button>
          <button
            type="button"
            tabIndex={-1}
            className="files-icon"
            title="Preview (Space, ⌘I)"
            data-on={preview}
            onClick={togglePreview}
          >
            <PanelRight size={15} strokeWidth={1.6} aria-label="Toggle preview" />
          </button>
        </div>

        <div className="files-head" aria-hidden="true">
          <span>Name</span>
          <span>Size</span>
          <span>Modified</span>
        </div>
        <div
          ref={listRef}
          className="files-list"
          role="listbox"
          tabIndex={0}
          aria-label={loc?.path || root?.label || "Files"}
          aria-activedescendant={selected ? `${winId}-row-${index}` : undefined}
          onKeyDown={onKey}
          onScroll={(e) => setScrollTop(e.currentTarget.scrollTop)}
          onContextMenu={(ev) => {
            ev.preventDefault()
            setMenu({ x: ev.clientX, y: ev.clientY, on: null })
          }}
          onDragOver={(ev) => {
            if (ev.dataTransfer.types.includes(DRAG_TYPE) && !dropOn) {
              ev.preventDefault()
              ev.dataTransfer.dropEffect = ev.altKey ? "copy" : "move"
            }
          }}
          onDrop={(ev) => {
            if (!loc || !ev.dataTransfer.types.includes(DRAG_TYPE)) return
            ev.preventDefault()
            void dropTo(
              ev.dataTransfer.getData(DRAG_TYPE),
              { root: loc.root, path: loc.path },
              ev.altKey,
            )
          }}
        >
          {(editing?.mode === "folder" || editing?.mode === "file") && (
            <div className="files-new" style={{ top: 0 }}>
              <span className="files-name">
                {editing.mode === "folder" ? "New folder:" : "New file:"}
                <NameEditor
                  initial={editing.mode === "folder" ? "untitled folder" : "untitled.md"}
                  onCommit={(n) => {
                    const kind = editing.mode === "folder" ? "folder" : "file"
                    setEditing(null)
                    run((c) => act.newItem(c, kind, n))
                    listRef.current?.focus({ preventScroll: true })
                  }}
                  onCancel={() => {
                    setEditing(null)
                    listRef.current?.focus({ preventScroll: true })
                  }}
                />
              </span>
            </div>
          )}
          <div style={{ height: shown.length * ROW_H, position: "relative" }}>
            {shown.slice(first, last).map((e, k) => {
              const i = first + k
              const Icon = e.broken ? Link2Off : iconOf(e)
              return (
                // biome-ignore lint/a11y/useKeyWithClickEvents: the listbox handles the keyboard
                <div
                  key={e.path}
                  id={`${winId}-row-${i}`}
                  role="option"
                  aria-selected={e.path === sel}
                  tabIndex={-1}
                  className="files-row"
                  data-selected={e.path === sel}
                  data-picked={picked.has(e.path)}
                  data-drop={dropOn === e.path}
                  data-cut={
                    getClip()?.mode === "cut" &&
                    getClip()?.items.some((x) => x.root === loc?.root && x.path === e.path)
                  }
                  draggable={!editing}
                  onDragStart={(ev) => {
                    const items = picked.has(e.path) ? targets() : [e]
                    ev.dataTransfer.setData(DRAG_TYPE, dragItems(items, loc?.root ?? ""))
                    ev.dataTransfer.effectAllowed = "copyMove"
                  }}
                  onDragOver={(ev) => {
                    if (e.dir && ev.dataTransfer.types.includes(DRAG_TYPE)) {
                      ev.preventDefault()
                      ev.dataTransfer.dropEffect = ev.altKey ? "copy" : "move"
                      setDropOn(e.path)
                    }
                  }}
                  onDragLeave={() => setDropOn((d) => (d === e.path ? null : d))}
                  onDrop={(ev) => {
                    setDropOn(null)
                    if (!e.dir || !loc) return
                    ev.preventDefault()
                    void dropTo(
                      ev.dataTransfer.getData(DRAG_TYPE),
                      { root: loc.root, path: e.path },
                      ev.altKey,
                    )
                  }}
                  onContextMenu={(ev) => {
                    ev.preventDefault()
                    ev.stopPropagation()
                    if (!picked.has(e.path)) pick(e, { metaKey: false, shiftKey: false })
                    setMenu({ x: ev.clientX, y: ev.clientY, on: e })
                  }}
                  data-dir={e.dir}
                  data-dim={e.name.startsWith(".") || e.broken}
                  style={{ top: i * ROW_H }}
                  onClick={(ev) => {
                    pick(e, ev)
                    listRef.current?.focus({ preventScroll: true })
                  }}
                  onDoubleClick={() => open(e)}
                >
                  <span className="files-name">
                    <Icon aria-hidden="true" size={15} strokeWidth={1.6} />
                    {editing?.mode === "rename" && editing.path === e.path ? (
                      <NameEditor
                        initial={e.name}
                        onCommit={(n) => {
                          setEditing(null)
                          run((c) => act.rename(c, e, n))
                          listRef.current?.focus({ preventScroll: true })
                        }}
                        onCancel={() => {
                          setEditing(null)
                          listRef.current?.focus({ preventScroll: true })
                        }}
                      />
                    ) : (
                      <span>{e.name}</span>
                    )}
                    {e.symlink && <span className="files-link">→</span>}
                  </span>
                  <span className="files-size">{e.dir ? "" : formatSize(e.size)}</span>
                  <span className="files-time">{formatTime(e.mtime)}</span>
                </div>
              )
            })}
          </div>
          {listing.state === "loading" && !listing.entries.length && slow && (
            <div className="files-state">
              {root?.kind === "sftp" ? `Connecting to ${root.host}…` : "Reading…"}
            </div>
          )}
          {listing.state === "error" && (
            <div className="files-state">
              <p>{listing.message}</p>
              <button type="button" className="desk-button" onClick={() => setReload((r) => r + 1)}>
                Try again
              </button>
            </div>
          )}
          {listing.state === "ready" && !shown.length && (
            <div className="files-state">
              {filter ? `Nothing matches “${filter}”.` : "This folder is empty."}
            </div>
          )}
        </div>
        <footer className="files-foot">
          <span>
            {notice ||
              `${shown.length} item${shown.length === 1 ? "" : "s"}${
                listing.entries.length !== shown.length
                  ? ` (${listing.entries.length - shown.length} hidden)`
                  : ""
              }`}
          </span>
          <span className="files-hint">
            type to filter · ⌘O open · ↵ rename · ⌘⌫ delete · right-click for more
          </span>
        </footer>
      </section>

      {menu && (
        <ContextMenu
          x={menu.x}
          y={menu.y}
          items={menuItems(menu.on)}
          onClose={() => {
            setMenu(null)
            listRef.current?.focus({ preventScroll: true })
          }}
        />
      )}
      {preview && root && (
        <Preview root={root} entry={selected} count={shown.length} onOpen={open} />
      )}
    </div>
  )
}
