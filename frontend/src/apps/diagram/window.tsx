import { at as anchorAt } from "@hollis-labs/ade-core"
import { useSurface } from "@hollis-labs/ade-react"
import {
  ChevronLeft,
  ChevronRight,
  Download,
  FilePlus2,
  GitCompare,
  Maximize,
  MessageSquare,
  MessageSquarePlus,
  Minus,
  PanelLeft,
  Play,
  Plus,
  Redo2,
  Save,
  Trash2,
  Undo2,
  X,
} from "lucide-react"
import {
  type PointerEvent as ReactPointerEvent,
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react"
import { hub } from "../../ade"
import {
  COLLAB_CHANGED,
  type CollabAnchor,
  type CollabDoc,
  type CollabMark,
  type CollabRevision,
  collab,
  isConflict,
} from "../../api/collab"
import { type FileRoot, files } from "../../api/files"
import { collabOpen } from "../../collab/agent"
import { MarkComposer, MarksPanel } from "../../collab/marks"
import { useDesk } from "../../desktop/context"
import type { WindowProps } from "../../desktop/registry"
import { buildSvg, saveExport, svgToPng } from "./export"
import { type Direction, fromBody, tidy } from "./layout"
import {
  boxOf,
  compare,
  type DEdge,
  type DGroup,
  type Diagram,
  type DNode,
  type EdgeStyle,
  empty,
  freshId,
  GRIDS,
  type Grid,
  KINDS,
  type NodeKind,
  SIDES,
  type Side,
  STYLE_LABEL,
  STYLES,
  serialize,
  THEMES,
  type Theme,
  TONES,
  type Tone,
} from "./model"
import { Defs, extent, ICON_NAMES, LEGEND_H, laneHeads, legendOf, Scene } from "./render"
import { routeAll } from "./route"
import { diagramSurface, diagramSurfaceId } from "./surface"
import { TEMPLATES, type Template, templateBody } from "./templates"
import { paletteFor } from "./theme"
import "../../collab/collab.css"
import "./diagram.css"

const ICON = { size: 14, strokeWidth: 1.6, "aria-hidden": true } as const
type Sel = { type: "node" | "edge" | "group"; id: string } | null
interface View {
  x: number
  y: number
  k: number
}

/**
 * The Diagram window: the canvas fills it. The list of diagrams folds away (☰); the
 * inspector and the marks open beside the canvas when something is selected, or with Marks.
 * Double-click the canvas for a node; drag a node (or a lane, with its nodes) to move it;
 * drag from the dot on a selected node to another node for an edge. ⌘S saves a revision;
 * Compare shows any revision against this one on the canvas.
 */
export function DiagramWindow({ winId, args }: WindowProps) {
  const desk = useDesk()
  const uid = `d${useId().replace(/[^a-zA-Z0-9]/g, "")}`
  const [docs, setDocs] = useState<CollabDoc[]>([])
  const [name, setName] = useState(typeof args.name === "string" ? args.name : "")
  const [listOpen, setListOpen] = useState(!name)
  const [marksOpen, setMarksOpen] = useState(false)
  const [exporting, setExporting] = useState(false)
  const [templatesOpen, setTemplatesOpen] = useState(false)
  /** A presentation: 0 shows everything, 1… a stage (a step, or a lane) and what came before. */
  const [present, setPresent] = useState<number | null>(null)
  const [doc, setDoc] = useState<CollabDoc | null>(null)
  const [dia, setDia] = useState<Diagram>(empty)
  const [title, setTitle] = useState("")
  const [marks, setMarks] = useState<CollabMark[]>([])
  const [sel, setSel] = useState<Sel>(null)
  const [view, setView] = useState<View>({ x: 40, y: 30, k: 1 })
  const [wire, setWire] = useState<{ from: string; x: number; y: number } | null>(null)
  const [comparing, setComparing] = useState(false)
  const [revs, setRevs] = useState<CollabRevision[]>([])
  const [from, setFrom] = useState(0)
  const [before, setBefore] = useState<Diagram | null>(null)
  const [picked, setPicked] = useState<CollabAnchor | null>(null)
  const [active, setActive] = useState("")
  const [status, setStatus] = useState<{ tone: "ok" | "warn"; text: string; newer?: number }>({
    tone: "ok",
    text: "",
  })
  const svg = useRef<SVGSVGElement>(null)
  const root = useRef<HTMLDivElement>(null)
  const fitted = useRef("")
  const drag = useRef<{
    kind: "pan" | "node" | "group"
    id?: string
    sx: number
    sy: number
    ox: number
    oy: number
    origin?: Map<string, { x: number; y: number }>
    moved: boolean
  } | null>(null)
  const nameRef = useRef(name)
  nameRef.current = name
  const saved = doc ? doc.body : ""
  const dirty = !!doc && (serialize(dia) !== serialize(fromBody(saved)) || title !== doc.title)
  // The diagram as a surface the owner and the agent both point at (surface.ts, ../../ade).
  const surface = useSurface(
    () =>
      diagramSurface({
        winId,
        doc,
        dia,
        dirty,
        selected: sel,
        svg: svg.current,
        select: (type, id) => setSel({ type, id }),
      }),
    hub,
  )
  // biome-ignore lint/correctness/useExhaustiveDependencies: tell the hub when the content changes
  useEffect(() => surface.changed(), [surface, doc, dia])
  // biome-ignore lint/correctness/useExhaustiveDependencies: and when the view moves
  useEffect(() => surface.moved(), [surface, view])
  // What the owner has selected is what "this" means to the agent.
  // biome-ignore lint/correctness/useExhaustiveDependencies: follows the selection
  useEffect(() => {
    const id = diagramSurfaceId(winId)
    hub.attend({
      actor: "user",
      style: "select",
      anchors: sel && sel.type !== "group" ? [anchorAt(id, sel.type, sel.id)] : [],
      slot: id,
    })
    surface.changed() // a guide may be waiting on a selection
  }, [sel, winId, surface])
  const dirtyRef = useRef(dirty)
  dirtyRef.current = dirty
  const theme: Theme = dia.theme ?? "app"
  // The app look follows the desktop theme: draw again when it changes.
  const [deskTheme, setDeskTheme] = useState(() =>
    document.documentElement.getAttribute("data-desk-theme"),
  )
  useEffect(() => {
    const o = new MutationObserver(() =>
      setDeskTheme(document.documentElement.getAttribute("data-desk-theme")),
    )
    o.observe(document.documentElement, { attributes: true, attributeFilter: ["data-desk-theme"] })
    return () => o.disconnect()
  }, [])
  // biome-ignore lint/correctness/useExhaustiveDependencies: a new desktop theme makes a new app palette
  const pal = useMemo(() => paletteFor(theme, dia.background), [theme, dia.background, deskTheme])

  const warn = useCallback(
    (e: unknown) =>
      setStatus({ tone: "warn", text: e instanceof Error ? e.message : "That did not work." }),
    [],
  )
  const loadList = useCallback(() => collab.list("diagram").then(setDocs).catch(warn), [warn])

  const load = useCallback(
    async (which: string, keepEdits = false) => {
      if (!which) return
      try {
        const r = await collab.get(which)
        if (nameRef.current !== which) return
        setMarks(r.marks)
        if (keepEdits && dirtyRef.current) {
          setDoc((old) => {
            if (old && r.doc.rev > old.rev)
              setStatus({
                tone: "warn",
                text: `Revision ${r.doc.rev} was saved elsewhere while you were editing.`,
                newer: r.doc.rev,
              })
            return old
          })
          return
        }
        setDoc(r.doc)
        resetHistory.current = true
        setDia(fromBody(r.doc.body))
        setTitle(r.doc.title)
        setStatus((st) => (st.tone === "warn" ? { tone: "ok", text: "" } : st))
        void collab
          .log(which)
          .then(setRevs)
          .catch(() => setRevs([]))
      } catch (e) {
        warn(e)
      }
    },
    [warn],
  )

  useEffect(() => {
    void loadList()
  }, [loadList])
  useEffect(() => {
    setDoc(null)
    setSel(null)
    setComparing(false)
    setBefore(null)
    setFrom(0)
    void load(name)
  }, [name, load])
  useEffect(() => {
    const onChange = (e: Event) => {
      void loadList()
      if ((e as CustomEvent<string>).detail === nameRef.current) void load(nameRef.current, true)
    }
    window.addEventListener(COLLAB_CHANGED, onChange)
    return () => window.removeEventListener(COLLAB_CHANGED, onChange)
  }, [load, loadList])
  useEffect(() => {
    if (doc)
      collabOpen.set(winId, {
        name: doc.name,
        rev: doc.rev,
        openMarks: marks.filter((m) => m.state === "open").length,
      })
    desk.setTitle(winId, doc ? `${dirty ? "● " : ""}${doc.title}` : "Diagram")
    desk.setDirty(winId, dirty)
    return () => {
      collabOpen.delete(winId)
    }
  }, [doc, marks, dirty, winId, desk])

  // The comparison's other side.
  const fromRev = from || (doc ? Math.max(1, doc.rev - (dirty ? 0 : 1)) : 0)
  useEffect(() => {
    if (!comparing || !doc) return
    if (fromRev === doc.rev) return setBefore(fromBody(doc.body))
    let live = true
    void collab
      .get(doc.name, fromRev)
      .then((r) => live && setBefore(fromBody(r.doc.body)))
      .catch(warn)
    return () => {
      live = false
    }
  }, [comparing, fromRev, doc, warn])

  const save = async (force = false) => {
    if (!doc) return
    try {
      const d = await collab.save(
        doc.name,
        serialize(dia),
        force ? 0 : doc.rev,
        title !== doc.title ? title : undefined,
      )
      setDoc(d)
      setDia(fromBody(d.body))
      setStatus({ tone: "ok", text: `Saved as revision ${d.rev}.` })
      void collab
        .log(d.name)
        .then(setRevs)
        .catch(() => undefined)
      void loadList()
    } catch (e) {
      if (isConflict(e))
        setStatus({
          tone: "warn",
          text: "It changed elsewhere since you opened it.",
          newer: doc.rev + 1,
        })
      else warn(e)
    }
  }
  const create = async (t?: Template) => {
    try {
      const d = await collab.create({
        kind: "diagram",
        title: !t || t.id === "blank" ? "Untitled diagram" : t.label,
        body: t ? templateBody(t) : serialize(empty()),
      })
      setTemplatesOpen(false)
      await loadList()
      setName(d.name)
      setListOpen(false)
    } catch (e) {
      warn(e)
    }
  }

  // Canvas coordinates of a pointer.
  const at = (e: { clientX: number; clientY: number }) => {
    const box = svg.current?.getBoundingClientRect()
    return {
      x: (e.clientX - (box?.left ?? 0) - view.x) / view.k,
      y: (e.clientY - (box?.top ?? 0) - view.y) / view.k,
    }
  }
  const patchNode = (id: string, change: Partial<DNode>) =>
    setDia((d) => ({ ...d, nodes: d.nodes.map((n) => (n.id === id ? { ...n, ...change } : n)) }))
  const patchEdge = (id: string, change: Partial<DEdge>) =>
    setDia((d) => ({ ...d, edges: d.edges.map((e) => (e.id === id ? { ...e, ...change } : e)) }))
  const patchGroup = (id: string, change: Partial<DGroup>) =>
    setDia((d) => ({ ...d, groups: d.groups.map((g) => (g.id === id ? { ...g, ...change } : g)) }))
  const addNode = (x: number, y: number) => {
    const id = freshId("node", (i) => dia.nodes.some((n) => n.id === i))
    setDia((d) => ({
      ...d,
      nodes: [...d.nodes, { id, label: "New node", kind: "service", x: x - 100, y: y - 24 }],
    }))
    setSel({ type: "node", id })
  }
  const removeSel = () => {
    if (!sel || sel.type === "group") return
    setDia((d) =>
      sel.type === "node"
        ? {
            ...d,
            nodes: d.nodes.filter((n) => n.id !== sel.id),
            edges: d.edges.filter((e) => e.from !== sel.id && e.to !== sel.id),
          }
        : { ...d, edges: d.edges.filter((e) => e.id !== sel.id) },
    )
    setSel(null)
  }

  const down = (e: ReactPointerEvent<SVGSVGElement>) => {
    if (e.button !== 0) return
    // The window's keys (Esc, Delete, ⌘S) need focus in it; the canvas itself takes none.
    root.current?.focus({ preventScroll: true })
    const target = e.target as Element
    const handle = target.closest("[data-handle]")
    const node = target.closest<SVGGElement>("[data-node]")
    const edge = target.closest<SVGGElement>("[data-edge]")
    const lane = target.closest<SVGGElement>("[data-group]")
    e.currentTarget.setPointerCapture(e.pointerId)
    if (handle && node && !comparing) {
      const p = at(e)
      setWire({ from: node.dataset.node ?? "", x: p.x, y: p.y })
      return
    }
    if (node) {
      const id = node.dataset.node ?? ""
      const n = dia.nodes.find((x) => x.id === id)
      setSel({ type: "node", id })
      if (n && !comparing)
        drag.current = {
          kind: "node",
          id,
          sx: e.clientX,
          sy: e.clientY,
          ox: n.x,
          oy: n.y,
          moved: false,
        }
      return
    }
    if (edge) return setSel({ type: "edge", id: edge.dataset.edge ?? "" })
    if (lane && !comparing) {
      // A lane moves with its nodes.
      const id = lane.dataset.group ?? ""
      setSel({ type: "group", id })
      drag.current = {
        kind: "group",
        id,
        sx: e.clientX,
        sy: e.clientY,
        ox: 0,
        oy: 0,
        origin: new Map(
          dia.nodes.filter((n) => n.group === id).map((n) => [n.id, { x: n.x, y: n.y }]),
        ),
        moved: false,
      }
      return
    }
    setSel(null)
    drag.current = {
      kind: "pan",
      sx: e.clientX,
      sy: e.clientY,
      ox: view.x,
      oy: view.y,
      moved: false,
    }
  }
  const move = (e: ReactPointerEvent<SVGSVGElement>) => {
    if (wire) {
      const p = at(e)
      return setWire({ ...wire, x: p.x, y: p.y })
    }
    const d = drag.current
    if (!d) return
    const dx = e.clientX - d.sx
    const dy = e.clientY - d.sy
    if (Math.abs(dx) + Math.abs(dy) > 2) d.moved = true
    if (d.kind === "pan") setView((v) => ({ ...v, x: d.ox + dx, y: d.oy + dy }))
    else if (d.kind === "node" && d.id)
      patchNode(d.id, { x: d.ox + dx / view.k, y: d.oy + dy / view.k })
    else if (d.kind === "group" && d.origin && d.moved) {
      const o = d.origin
      setDia((dd) => ({
        ...dd,
        nodes: dd.nodes.map((n) => {
          const p = o.get(n.id)
          return p ? { ...n, x: p.x + dx / view.k, y: p.y + dy / view.k } : n
        }),
      }))
    }
  }
  const up = (e: ReactPointerEvent<SVGSVGElement>) => {
    if (wire) {
      // The node under the pointer, if any, is where the edge goes.
      const p = at(e)
      const to = dia.nodes.find((n) => {
        const b = boxOf(n)
        return (
          n.id !== wire.from && p.x >= b.x && p.x <= b.x + b.w && p.y >= b.y && p.y <= b.y + b.h
        )
      })
      if (to && !dia.edges.some((x) => x.from === wire.from && x.to === to.id)) {
        const id = freshId(`${wire.from}-${to.id}`, (i) => dia.edges.some((x) => x.id === i))
        setDia((d) => ({ ...d, edges: [...d.edges, { id, from: wire.from, to: to.id }] }))
        setSel({ type: "edge", id })
      }
      setWire(null)
    }
    drag.current = null
  }
  // The wheel zooms around the pointer (a native listener: React's is passive).
  useEffect(() => {
    const el = svg.current
    if (!el) return
    const onWheel = (e: WheelEvent) => {
      e.preventDefault()
      const box = el.getBoundingClientRect()
      setView((v) => {
        if (
          !e.ctrlKey &&
          !e.metaKey &&
          Math.abs(e.deltaX) + Math.abs(e.deltaY) < 40 &&
          e.deltaMode === 0 &&
          !Number.isInteger(e.deltaY)
        ) {
          return { ...v, x: v.x - e.deltaX, y: v.y - e.deltaY } // a trackpad's two-finger move pans
        }
        const k = Math.min(2.5, Math.max(0.2, v.k * (e.deltaY < 0 ? 1.1 : 1 / 1.1)))
        const px = e.clientX - box.left
        const py = e.clientY - box.top
        return { k, x: px - ((px - v.x) / v.k) * k, y: py - ((py - v.y) / v.k) * k }
      })
    }
    el.addEventListener("wheel", onWheel, { passive: false })
    return () => el.removeEventListener("wheel", onWheel)
  })
  const zoomBy = (f: number) => {
    const box = svg.current?.getBoundingClientRect()
    const px = (box?.width ?? 800) / 2
    const py = (box?.height ?? 600) / 2
    setView((v) => {
      const k = Math.min(2.5, Math.max(0.2, v.k * f))
      return { k, x: px - ((px - v.x) / v.k) * k, y: py - ((py - v.y) / v.k) * k }
    })
  }
  const fit = useCallback(() => {
    const box = svg.current?.getBoundingClientRect()
    if (!box || !dia.nodes.length) return setView({ x: 40, y: 30, k: 1 })
    const ext = extent(dia)
    const h = ext.h + (legendOf(dia).length ? LEGEND_H + 34 : 0)
    const k = Math.min(
      1.4,
      Math.max(0.2, Math.min((box.width - 80) / ext.w, (box.height - 80) / h)),
    )
    setView({
      k,
      x: (box.width - ext.w * k) / 2 - ext.x * k,
      y: (box.height - h * k) / 2 - ext.y * k,
    })
  }, [dia])
  const fitRef = useRef(fit)
  fitRef.current = fit
  // Fit a diagram when it is first shown.
  useEffect(() => {
    if (doc && fitted.current !== doc.name) {
      fitted.current = doc.name
      requestAnimationFrame(() => fit())
    }
  }, [doc, fit])

  const diff = useMemo(
    () => (comparing && before ? compare(before, dia) : null),
    [comparing, before, dia],
  )
  const routes = useMemo(
    () =>
      routeAll(
        [...(diff?.goneNodes ?? []), ...dia.nodes],
        [...(diff?.goneEdges ?? []), ...dia.edges],
        laneHeads(dia),
      ),
    [dia, diff],
  )
  const counts = useMemo(() => {
    const c = { added: 0, removed: 0, changed: 0, moved: 0 }
    if (diff)
      for (const v of [...diff.nodes.values(), ...diff.edges.values()]) if (v !== "same") c[v]++
    return c
  }, [diff])
  const open = marks.filter((m) => m.state === "open")
  const marksOn = (type: "node" | "edge", id: string) =>
    open.filter((m) => m.anchor[type] === id).length
  const node = sel?.type === "node" ? dia.nodes.find((n) => n.id === sel.id) : undefined
  const edge = sel?.type === "edge" ? dia.edges.find((e) => e.id === sel.id) : undefined
  const group = sel?.type === "group" ? dia.groups.find((g) => g.id === sel.id) : undefined
  // ---- Undo and redo: snapshots of the diagram, a burst of changes (a drag, typing) as one ----
  const past = useRef<string[]>([])
  const future = useRef<string[]>([])
  const snap = useRef("")
  const restoring = useRef(false)
  const resetHistory = useRef(true)
  const [histTick, setHistTick] = useState(0)
  const pending = useRef<number | null>(null)
  const record = useCallback(() => {
    pending.current = null
    const cur = JSON.stringify(dia)
    if (cur === snap.current) return
    if (snap.current) past.current = [...past.current.slice(-99), snap.current]
    future.current = []
    snap.current = cur
    setHistTick((t) => t + 1)
  }, [dia])
  useEffect(() => {
    if (resetHistory.current) {
      resetHistory.current = false
      past.current = []
      future.current = []
      snap.current = JSON.stringify(dia)
      setHistTick((t) => t + 1)
      return
    }
    if (restoring.current) {
      restoring.current = false
      snap.current = JSON.stringify(dia)
      return
    }
    if (pending.current) window.clearTimeout(pending.current)
    pending.current = window.setTimeout(record, 400)
    return () => {
      if (pending.current) window.clearTimeout(pending.current)
    }
  }, [dia, record])
  const flush = () => {
    if (pending.current) {
      window.clearTimeout(pending.current)
      record()
    }
  }
  const undo = () => {
    flush()
    const prev = past.current.pop()
    if (!prev) return
    future.current.push(snap.current)
    snap.current = prev
    restoring.current = true
    setDia(JSON.parse(prev) as Diagram)
    setHistTick((t) => t + 1)
  }
  const redo = () => {
    flush()
    const next = future.current.pop()
    if (!next) return
    past.current.push(snap.current)
    snap.current = next
    restoring.current = true
    setDia(JSON.parse(next) as Diagram)
    setHistTick((t) => t + 1)
  }
  void histTick
  const canUndo =
    past.current.length > 0 || (!!pending.current && JSON.stringify(dia) !== snap.current)
  const canRedo = future.current.length > 0

  // ---- Presenting: the stages are the step numbers, or else the lanes, in order ----
  const stages = useMemo(() => {
    const near = (ids: Set<string>) => {
      // Nodes with no step (or no lane) join when something shown connects to them.
      const out = new Set(ids)
      for (const e of dia.edges) {
        const a = dia.nodes.find((n) => n.id === e.from)
        const b = dia.nodes.find((n) => n.id === e.to)
        if (ids.has(e.from) && b && !b.step && !b.group) out.add(e.to)
        if (ids.has(e.to) && a && !a.step && !a.group) out.add(e.from)
      }
      return out
    }
    const steps = [...new Set(dia.nodes.map((n) => n.step).filter((x): x is number => !!x))].sort(
      (a, b) => a - b,
    )
    if (steps.length)
      return steps.map((st) => {
        const ids = new Set(dia.nodes.filter((n) => n.step && n.step <= st).map((n) => n.id))
        // Unnumbered nodes come in with the first step they touch.
        for (const n of dia.nodes)
          if (!n.step) {
            const touch = dia.edges.some(
              (e) => (e.from === n.id && ids.has(e.to)) || (e.to === n.id && ids.has(e.from)),
            )
            if (touch) ids.add(n.id)
          }
        return {
          label: dia.nodes
            .filter((n) => n.step === st)
            .map((n) => n.label)
            .join(", "),
          ids,
        }
      })
    const lanes = dia.groups.filter((g) => dia.nodes.some((n) => n.group === g.id))
    return lanes.map((g, i) => ({
      label: g.label,
      ids: near(
        new Set(
          dia.nodes
            .filter((n) => lanes.slice(0, i + 1).some((l) => l.id === n.group))
            .map((n) => n.id),
        ),
      ),
    }))
  }, [dia])
  const startPresenting = () => {
    setSel(null)
    setPicked(null)
    setMarksOpen(false)
    setExporting(false)
    setListOpen(false)
    setComparing(false)
    setPresent(0)
    const w = desk.wmState().wins.find((x) => x.id === winId)
    if (w && !w.maximized) desk.wm({ type: "maximize", id: winId })
    window.setTimeout(() => fitRef.current(), 250)
    root.current?.focus({ preventScroll: true })
  }
  const focus = present ? stages[present - 1]?.ids : undefined

  // ---- Export ----
  const [ex, setEx] = useState({
    format: "png" as "png" | "svg",
    scale: 2,
    title: true,
    background: true,
    grid: false,
    legend: true,
    root: "",
    folder: "Diagrams",
    name: "",
  })
  const [roots, setRoots] = useState<FileRoot[]>([])
  const [exported, setExported] = useState<{ root: string; path: string } | null>(null)
  useEffect(() => {
    if (!exporting || roots.length) return
    files
      .roots()
      .then((r) => {
        setRoots(r.roots)
        setEx((x) => ({
          ...x,
          root: x.root || (r.roots.find((t) => t.id === "documents") ?? r.roots[0])?.id || "",
        }))
      })
      .catch(warn)
  }, [exporting, roots.length, warn])
  const exportNow = async () => {
    try {
      const built = buildSvg(dia, pal, {
        title: ex.title ? title : undefined,
        background: ex.background,
        grid: ex.grid,
        legend: ex.legend,
      })
      const stem =
        (ex.name.trim() || title)
          .toLowerCase()
          .replace(/[^a-z0-9]+/g, "-")
          .replace(/^-+|-+$/g, "") || "diagram"
      const body =
        ex.format === "svg" ? built.svg : await svgToPng(built.svg, built.w, built.h, ex.scale)
      const path = await saveExport(ex.root, ex.folder, `${stem}.${ex.format}`, body)
      setExported({ root: ex.root, path })
      setStatus({ tone: "ok", text: `Exported to ${path}.` })
    } catch (e) {
      warn(e)
    }
  }

  const sideShown = !!doc && present === null && (marksOpen || exporting || !!sel || !!picked)
  const activeMark = marks.find((m) => m.id === active)

  const addMark = async (m: { kind: CollabMark["kind"]; text: string }) => {
    if (!doc || !picked) return
    try {
      setMarks(
        await collab.mark(doc.name, "add", {
          rev: doc.rev,
          kind: m.kind,
          text: m.text,
          anchor: picked,
        }),
      )
      setPicked(null)
    } catch (e) {
      warn(e)
    }
  }
  const setState = (m: CollabMark, state: CollabMark["state"]) =>
    doc && void collab.mark(doc.name, "update", { id: m.id, state }).then(setMarks).catch(warn)
  const removeMark = (m: CollabMark) =>
    doc && void collab.mark(doc.name, "remove", { id: m.id }).then(setMarks).catch(warn)

  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: the window's own keys (⌘S, Delete, Esc)
    <div
      ref={root}
      className="collab dia"
      tabIndex={-1}
      data-theme={theme}
      onKeyDown={(e) => {
        const t = e.target as HTMLElement
        const typing = t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT"
        if (present !== null) {
          if (e.key === "ArrowRight" || e.key === " " || e.key === "PageDown") {
            e.preventDefault()
            setPresent((p) => Math.min(stages.length, (p ?? 0) + 1))
          } else if (e.key === "ArrowLeft" || e.key === "PageUp") {
            e.preventDefault()
            setPresent((p) => Math.max(0, (p ?? 0) - 1))
          } else if (e.key === "Escape") {
            e.preventDefault()
            setPresent(null)
          } else if (e.key === "f") fit()
          return
        }
        if ((e.metaKey || e.ctrlKey) && e.key === "s") {
          e.preventDefault()
          if (dirty) void save()
        } else if (!typing && (e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "z") {
          e.preventDefault()
          if (e.shiftKey) redo()
          else undo()
        } else if (!typing && (e.metaKey || e.ctrlKey) && e.key === "y") {
          e.preventDefault()
          redo()
        } else if (!typing && !comparing && (e.key === "Delete" || e.key === "Backspace") && sel) {
          e.preventDefault()
          removeSel()
        } else if (!typing && e.key === "Escape") {
          setSel(null)
          setPicked(null)
          setMarksOpen(false)
          setExporting(false)
          setListOpen(false)
        }
      }}
    >
      {listOpen && (
        <nav className="collab-list dia-drawer">
          <header>
            <strong>Diagrams</strong>
            <span>
              <button
                type="button"
                className="dia-iconbtn"
                title="New diagram"
                aria-label="New diagram"
                onClick={() => setTemplatesOpen((o) => !o)}
              >
                <FilePlus2 {...ICON} />
              </button>
              {doc && (
                <button
                  type="button"
                  className="dia-iconbtn"
                  title="Hide the list"
                  aria-label="Hide the list"
                  onClick={() => setListOpen(false)}
                >
                  <X {...ICON} />
                </button>
              )}
            </span>
          </header>
          {templatesOpen && (
            <div className="dia-templates">
              <p>Start from</p>
              {TEMPLATES.map((t) => (
                <button key={t.id} type="button" onClick={() => void create(t)} title={t.about}>
                  <strong>{t.label}</strong>
                  <span>{t.about}</span>
                </button>
              ))}
            </div>
          )}
          {docs.map((d) => (
            <button
              key={d.name}
              type="button"
              data-on={d.name === name}
              onClick={() => {
                setName(d.name)
                setListOpen(false)
              }}
            >
              <span>{d.title || d.name}</span>
              <em>r{d.rev}</em>
            </button>
          ))}
          {!docs.length && (
            <p className="collab-none">
              No diagrams yet. Start one here, or ask the agent to draw how something fits together.
            </p>
          )}
        </nav>
      )}
      {doc ? (
        <section className="collab-main">
          <div className="dia-stage">
            {present === null && (
              <div className="dia-overlay">
                <div className="dia-float dia-float-left">
                  <button
                    type="button"
                    className="dia-iconbtn"
                    data-on={listOpen}
                    title="Diagrams"
                    aria-label="Diagrams"
                    onClick={() => setListOpen((o) => !o)}
                  >
                    <PanelLeft {...ICON} />
                  </button>
                  <input
                    className="collab-title"
                    value={title}
                    size={Math.max(8, title.length + 1)}
                    aria-label="Title"
                    onChange={(e) => setTitle(e.target.value)}
                  />
                  <span className="collab-rev" title={doc.name}>
                    r{doc.rev} · {dia.nodes.length} nodes · {dia.edges.length} edges
                  </span>
                </div>
                <div className="dia-right">
                  <div className="dia-float dia-float-right">
                    <fieldset className="dia-seg" aria-label="Look">
                      {THEMES.map((t) => (
                        <button
                          key={t}
                          type="button"
                          data-on={theme === t}
                          onClick={() =>
                            setDia((d) => ({ ...d, theme: t === "app" ? undefined : t }))
                          }
                          title={
                            t === "app"
                              ? "The desktop theme's colours (the default)"
                              : `The ${t} look (kept with the diagram)`
                          }
                        >
                          {t}
                        </button>
                      ))}
                    </fieldset>
                    {!comparing && (
                      <button
                        type="button"
                        className="viewer-tool"
                        title="Add a node (or double-click the canvas)"
                        onClick={() => {
                          const box = svg.current?.getBoundingClientRect()
                          addNode(
                            ((box?.width ?? 600) / 2 - view.x) / view.k,
                            ((box?.height ?? 400) / 2 - view.y) / view.k,
                          )
                        }}
                      >
                        <Plus {...ICON} /> Node
                      </button>
                    )}
                    {!comparing && (
                      <fieldset className="dia-seg" aria-label="Tidy">
                        {(["LR", "TB"] as Direction[]).map((d) => (
                          <button
                            key={d}
                            type="button"
                            title={
                              d === "LR"
                                ? "Tidy: lay it out left to right"
                                : "Tidy: lay it out top to bottom"
                            }
                            onClick={() => {
                              setDia((x) => tidy(x, d))
                              requestAnimationFrame(() => fitRef.current())
                            }}
                          >
                            {d === "LR" ? "Tidy →" : "Tidy ↓"}
                          </button>
                        ))}
                      </fieldset>
                    )}
                    {!comparing && (
                      <span className="dia-seg dia-undo">
                        <button
                          type="button"
                          title="Undo (⌘Z)"
                          aria-label="Undo"
                          disabled={!canUndo}
                          onClick={undo}
                        >
                          <Undo2 {...ICON} />
                        </button>
                        <button
                          type="button"
                          title="Redo (⇧⌘Z)"
                          aria-label="Redo"
                          disabled={!canRedo}
                          onClick={redo}
                        >
                          <Redo2 {...ICON} />
                        </button>
                      </span>
                    )}
                    <button
                      type="button"
                      className="viewer-tool"
                      data-on={comparing}
                      title="Compare with another revision"
                      onClick={() => setComparing((c) => !c)}
                    >
                      <GitCompare {...ICON} /> Compare
                    </button>
                    <button
                      type="button"
                      className="viewer-tool"
                      data-on={exporting}
                      title="Export as SVG or PNG"
                      onClick={() => {
                        setSel(null)
                        setExporting((x) => !x)
                      }}
                    >
                      <Download {...ICON} /> Export
                    </button>
                    <button
                      type="button"
                      className="viewer-tool"
                      title="Present: the diagram alone, step by step (← →, Esc)"
                      onClick={startPresenting}
                    >
                      <Play {...ICON} /> Present
                    </button>
                    <button
                      type="button"
                      className="viewer-tool"
                      data-on={marksOpen}
                      title="Marks and notes"
                      onClick={() => setMarksOpen((o) => !o)}
                    >
                      <MessageSquare {...ICON} /> {open.length || ""}
                    </button>
                    <button
                      type="button"
                      className="collab-primary"
                      disabled={!dirty}
                      title="Save a new revision (⌘S)"
                      onClick={() => void save()}
                    >
                      <Save {...ICON} /> Save
                    </button>
                  </div>
                  {sideShown && (
                    <div className="collab-side dia-side">
                      <header className="dia-side-head">
                        <strong>
                          {node
                            ? "Node"
                            : edge
                              ? "Edge"
                              : group
                                ? "Lane"
                                : picked
                                  ? "New mark"
                                  : "Marks"}
                        </strong>
                        <button
                          type="button"
                          className="dia-iconbtn"
                          aria-label="Close"
                          title="Close (Esc)"
                          onClick={() => {
                            setSel(null)
                            setPicked(null)
                            setMarksOpen(false)
                          }}
                        >
                          <X {...ICON} />
                        </button>
                      </header>
                      {node && !comparing && (
                        <div className="dia-inspect">
                          <label>
                            Label
                            <input
                              value={node.label}
                              onChange={(e) => patchNode(node.id, { label: e.target.value })}
                            />
                          </label>
                          <label>
                            Subtitle
                            <input
                              value={node.subtitle ?? ""}
                              placeholder="a second line"
                              onChange={(e) =>
                                patchNode(node.id, { subtitle: e.target.value || undefined })
                              }
                            />
                          </label>
                          <div className="dia-row">
                            <label>
                              Kind
                              <select
                                value={node.kind ?? ""}
                                onChange={(e) =>
                                  patchNode(node.id, {
                                    kind: (e.target.value || undefined) as NodeKind | undefined,
                                  })
                                }
                              >
                                <option value="">(none)</option>
                                {KINDS.map((k) => (
                                  <option key={k}>{k}</option>
                                ))}
                              </select>
                            </label>
                            <label>
                              Icon
                              <select
                                value={node.icon ?? ""}
                                onChange={(e) =>
                                  patchNode(node.id, { icon: e.target.value || undefined })
                                }
                              >
                                <option value="">(by kind)</option>
                                {ICON_NAMES.map((k) => (
                                  <option key={k}>{k}</option>
                                ))}
                              </select>
                            </label>
                          </div>
                          <fieldset className="dia-tones" aria-label="Tone">
                            <button
                              type="button"
                              data-on={!node.tone}
                              onClick={() => patchNode(node.id, { tone: undefined })}
                              title="By kind"
                            >
                              ∅
                            </button>
                            {TONES.map((t) => (
                              <button
                                key={t}
                                type="button"
                                data-on={node.tone === t}
                                style={{ background: pal.tones[t] }}
                                title={t}
                                aria-label={t}
                                onClick={() => patchNode(node.id, { tone: t as Tone })}
                              />
                            ))}
                          </fieldset>
                          <div className="dia-row">
                            <label>
                              Step
                              <input
                                type="number"
                                min={0}
                                value={node.step ?? ""}
                                onChange={(e) =>
                                  patchNode(node.id, { step: Number(e.target.value) || undefined })
                                }
                              />
                            </label>
                            <label>
                              Tag
                              <input
                                value={node.tag ?? ""}
                                placeholder="RETRY"
                                onChange={(e) =>
                                  patchNode(node.id, { tag: e.target.value || undefined })
                                }
                              />
                            </label>
                          </div>
                          <label>
                            Rows (one a line)
                            <textarea
                              rows={2}
                              value={(node.items ?? []).join("\n")}
                              onChange={(e) => {
                                const items = e.target.value
                                  .split("\n")
                                  .filter((x, i, a) => x.trim() || i < a.length - 1)
                                patchNode(node.id, { items: items.length ? items : undefined })
                              }}
                            />
                          </label>
                          <label className="dia-check">
                            <input
                              type="checkbox"
                              checked={!!node.highlight}
                              onChange={(e) =>
                                patchNode(node.id, { highlight: e.target.checked || undefined })
                              }
                            />
                            Highlight
                          </label>
                          <label>
                            Lane
                            <input
                              value={
                                dia.groups.find((g) => g.id === node.group)?.label ??
                                node.group ??
                                ""
                              }
                              list={`groups-${winId}`}
                              placeholder="none"
                              onChange={(e) => {
                                const label = e.target.value
                                const id = label.trim()
                                  ? (dia.groups.find((g) => g.label === label)?.id ??
                                    freshId(label, (i) => dia.groups.some((g) => g.id === i)))
                                  : undefined
                                setDia((d) => ({
                                  ...d,
                                  groups:
                                    id && !d.groups.some((g) => g.id === id)
                                      ? [...d.groups, { id, label }]
                                      : d.groups,
                                  nodes: d.nodes.map((n) =>
                                    n.id === node.id ? { ...n, group: id } : n,
                                  ),
                                }))
                              }}
                            />
                            <datalist id={`groups-${winId}`}>
                              {dia.groups.map((g) => (
                                <option key={g.id} value={g.label} />
                              ))}
                            </datalist>
                          </label>
                          <label>
                            Note
                            <textarea
                              rows={2}
                              value={node.note ?? ""}
                              onChange={(e) =>
                                patchNode(node.id, { note: e.target.value || undefined })
                              }
                            />
                          </label>
                          <footer>
                            <code>{node.id}</code>
                            <button type="button" onClick={() => setPicked({ node: node.id })}>
                              <MessageSquarePlus {...ICON} /> Mark
                            </button>
                            <button type="button" aria-label="Delete the node" onClick={removeSel}>
                              <Trash2 {...ICON} />
                            </button>
                          </footer>
                        </div>
                      )}
                      {edge && !comparing && (
                        <div className="dia-inspect">
                          <label>
                            Label
                            <input
                              value={edge.label ?? ""}
                              placeholder="what flows, or how"
                              onChange={(e) =>
                                patchEdge(edge.id, { label: e.target.value || undefined })
                              }
                            />
                          </label>
                          <label>
                            Style
                            <select
                              value={edge.style ?? "flow"}
                              onChange={(e) =>
                                patchEdge(edge.id, {
                                  style:
                                    e.target.value === "flow"
                                      ? undefined
                                      : (e.target.value as EdgeStyle),
                                })
                              }
                            >
                              {STYLES.map((s) => (
                                <option key={s} value={s}>
                                  {s} ({STYLE_LABEL[s]})
                                </option>
                              ))}
                            </select>
                          </label>
                          <div className="dia-row">
                            <label>
                              Leaves by
                              <select
                                value={edge.fromSide ?? ""}
                                onChange={(e) =>
                                  patchEdge(edge.id, {
                                    fromSide: (e.target.value || undefined) as Side | undefined,
                                  })
                                }
                              >
                                <option value="">auto</option>
                                {SIDES.map((s) => (
                                  <option key={s}>{s}</option>
                                ))}
                              </select>
                            </label>
                            <label>
                              Enters by
                              <select
                                value={edge.toSide ?? ""}
                                onChange={(e) =>
                                  patchEdge(edge.id, {
                                    toSide: (e.target.value || undefined) as Side | undefined,
                                  })
                                }
                              >
                                <option value="">auto</option>
                                {SIDES.map((s) => (
                                  <option key={s}>{s}</option>
                                ))}
                              </select>
                            </label>
                          </div>
                          <label className="dia-check">
                            <input
                              type="checkbox"
                              checked={!!edge.both}
                              onChange={(e) =>
                                patchEdge(edge.id, { both: e.target.checked || undefined })
                              }
                            />
                            Both ways
                          </label>
                          <footer>
                            <code>
                              {edge.from} → {edge.to}
                            </code>
                            <button
                              type="button"
                              onClick={() =>
                                patchEdge(edge.id, {
                                  from: edge.to,
                                  to: edge.from,
                                  fromSide: edge.toSide,
                                  toSide: edge.fromSide,
                                })
                              }
                            >
                              Reverse
                            </button>
                            <button type="button" onClick={() => setPicked({ edge: edge.id })}>
                              <MessageSquarePlus {...ICON} /> Mark
                            </button>
                            <button type="button" aria-label="Delete the edge" onClick={removeSel}>
                              <Trash2 {...ICON} />
                            </button>
                          </footer>
                        </div>
                      )}
                      {group && !comparing && (
                        <div className="dia-inspect">
                          <label>
                            Title
                            <input
                              value={group.label}
                              onChange={(e) => patchGroup(group.id, { label: e.target.value })}
                            />
                          </label>
                          <label>
                            Subtitle
                            <input
                              value={group.subtitle ?? ""}
                              placeholder="what happens here"
                              onChange={(e) =>
                                patchGroup(group.id, { subtitle: e.target.value || undefined })
                              }
                            />
                          </label>
                          <fieldset className="dia-tones" aria-label="Tone">
                            <button
                              type="button"
                              data-on={!group.tone}
                              onClick={() => patchGroup(group.id, { tone: undefined })}
                              title="Plain"
                            >
                              ∅
                            </button>
                            {TONES.map((t) => (
                              <button
                                key={t}
                                type="button"
                                data-on={group.tone === t}
                                style={{ background: pal.tones[t] }}
                                title={t}
                                aria-label={t}
                                onClick={() => patchGroup(group.id, { tone: t as Tone })}
                              />
                            ))}
                          </fieldset>
                          <footer>
                            <code>{group.id}</code>
                          </footer>
                        </div>
                      )}
                      {exporting && !sel && !picked && (
                        <div className="dia-inspect dia-export">
                          <fieldset className="dia-seg" aria-label="Format">
                            {(["png", "svg"] as const).map((f) => (
                              <button
                                key={f}
                                type="button"
                                data-on={ex.format === f}
                                onClick={() => setEx((x) => ({ ...x, format: f }))}
                              >
                                {f.toUpperCase()}
                              </button>
                            ))}
                          </fieldset>
                          {ex.format === "png" && (
                            <fieldset className="dia-seg" aria-label="Scale">
                              {[1, 2, 3].map((k) => (
                                <button
                                  key={k}
                                  type="button"
                                  data-on={ex.scale === k}
                                  onClick={() => setEx((x) => ({ ...x, scale: k }))}
                                >
                                  {k}×
                                </button>
                              ))}
                            </fieldset>
                          )}
                          <label className="dia-check">
                            <input
                              type="checkbox"
                              checked={ex.title}
                              onChange={(e) => setEx((x) => ({ ...x, title: e.target.checked }))}
                            />
                            Title
                          </label>
                          <label className="dia-check">
                            <input
                              type="checkbox"
                              checked={ex.background}
                              onChange={(e) =>
                                setEx((x) => ({ ...x, background: e.target.checked }))
                              }
                            />
                            Background (off: transparent)
                          </label>
                          <label className="dia-check">
                            <input
                              type="checkbox"
                              checked={ex.grid}
                              disabled={!ex.background}
                              onChange={(e) => setEx((x) => ({ ...x, grid: e.target.checked }))}
                            />
                            Grid
                          </label>
                          <label className="dia-check">
                            <input
                              type="checkbox"
                              checked={ex.legend}
                              onChange={(e) => setEx((x) => ({ ...x, legend: e.target.checked }))}
                            />
                            Legend
                          </label>
                          <div className="dia-row">
                            <label>
                              Place
                              <select
                                value={ex.root}
                                onChange={(e) => setEx((x) => ({ ...x, root: e.target.value }))}
                              >
                                {roots.map((r) => (
                                  <option key={r.id} value={r.id}>
                                    {r.label}
                                  </option>
                                ))}
                              </select>
                            </label>
                            <label>
                              Folder
                              <input
                                value={ex.folder}
                                onChange={(e) => setEx((x) => ({ ...x, folder: e.target.value }))}
                              />
                            </label>
                          </div>
                          <label>
                            Name
                            <input
                              value={ex.name}
                              placeholder={
                                title
                                  .toLowerCase()
                                  .replace(/[^a-z0-9]+/g, "-")
                                  .replace(/^-+|-+$/g, "") || "diagram"
                              }
                              onChange={(e) => setEx((x) => ({ ...x, name: e.target.value }))}
                            />
                          </label>
                          <footer>
                            <button
                              type="button"
                              className="collab-primary"
                              disabled={!ex.root}
                              onClick={() => void exportNow()}
                            >
                              <Download {...ICON} /> Export {ex.format.toUpperCase()}
                            </button>
                            {exported && (
                              <button
                                type="button"
                                onClick={() => {
                                  const slash = exported.path.lastIndexOf("/")
                                  desk.open("files", {
                                    args: {
                                      root: exported.root,
                                      path: slash > 0 ? exported.path.slice(0, slash) : "",
                                      select: exported.path.slice(slash + 1),
                                    },
                                  })
                                }}
                              >
                                Show in Files
                              </button>
                            )}
                          </footer>
                        </div>
                      )}
                      {!sel && !picked && !comparing && !exporting && (
                        <div className="dia-inspect">
                          <div className="dia-row">
                            <label>
                              Grid
                              <select
                                value={dia.grid ?? "lines"}
                                onChange={(e) =>
                                  setDia((d) => ({
                                    ...d,
                                    grid:
                                      e.target.value === "lines"
                                        ? undefined
                                        : (e.target.value as Grid),
                                  }))
                                }
                              >
                                {GRIDS.map((g) => (
                                  <option key={g} value={g}>
                                    {g}
                                  </option>
                                ))}
                              </select>
                            </label>
                            <label>
                              Background
                              <span className="dia-bg">
                                <input
                                  type="color"
                                  value={dia.background ?? pal.bg}
                                  onChange={(e) =>
                                    setDia((d) => ({ ...d, background: e.target.value }))
                                  }
                                  aria-label="Background colour"
                                />
                                {dia.background && (
                                  <button
                                    type="button"
                                    onClick={() => setDia((d) => ({ ...d, background: undefined }))}
                                  >
                                    theme
                                  </button>
                                )}
                              </span>
                            </label>
                          </div>
                        </div>
                      )}
                      {!sel && !picked && (
                        <button type="button" className="dia-note" onClick={() => setPicked({})}>
                          <MessageSquarePlus {...ICON} /> Note on the whole diagram
                        </button>
                      )}
                      {picked && (
                        <MarkComposer
                          what={
                            picked.node
                              ? `node ${picked.node}`
                              : picked.edge
                                ? `edge ${picked.edge}`
                                : "the whole diagram"
                          }
                          canSuggest={false}
                          onAdd={(m) => void addMark(m)}
                          onCancel={() => setPicked(null)}
                        />
                      )}
                      <MarksPanel
                        marks={marks}
                        active={active}
                        detached={(m) =>
                          (!!m.anchor.node && !dia.nodes.some((n) => n.id === m.anchor.node)) ||
                          (!!m.anchor.edge && !dia.edges.some((e) => e.id === m.anchor.edge))
                        }
                        onPick={(m) => {
                          setActive(m.id)
                          if (m.anchor.node) setSel({ type: "node", id: m.anchor.node })
                          else if (m.anchor.edge) setSel({ type: "edge", id: m.anchor.edge })
                        }}
                        onState={setState}
                        onRemove={removeMark}
                      />
                    </div>
                  )}
                </div>
              </div>
            )}
            {comparing && present === null && (
              <p className="collab-status dia-banner" data-tone="ok">
                <select
                  value={fromRev}
                  aria-label="Compare from revision"
                  onChange={(e) => setFrom(Number(e.target.value))}
                >
                  {revs.map((r) => (
                    <option key={r.rev} value={r.rev}>
                      revision {r.rev} ·{" "}
                      {new Date(r.at).toLocaleString([], {
                        month: "short",
                        day: "numeric",
                        hour: "2-digit",
                        minute: "2-digit",
                      })}
                    </option>
                  ))}
                  {!revs.some((r) => r.rev === fromRev) && (
                    <option value={fromRev}>revision {fromRev}</option>
                  )}
                </select>
                → {dirty ? "what is on the canvas (unsaved)" : `revision ${doc.rev}`}
                <span className="dia-legend">
                  <b data-c="added">{counts.added} added</b>
                  <b data-c="changed">{counts.changed} changed</b>
                  <b data-c="removed">{counts.removed} removed</b>
                  <b data-c="moved">{counts.moved} moved</b>
                </span>
              </p>
            )}
            {status.text && !comparing && present === null && (
              <p className="collab-status dia-banner" data-tone={status.tone} role="status">
                {status.text}
                {status.newer && (
                  <>
                    <button type="button" onClick={() => void load(doc.name)}>
                      Load it (drops your edits)
                    </button>
                    <button type="button" onClick={() => void save(true)}>
                      Save mine over it
                    </button>
                  </>
                )}
              </p>
            )}
            <svg
              ref={svg}
              className="dia-canvas"
              data-comparing={comparing}
              role="img"
              aria-label={`Diagram: ${title}`}
              style={{ background: pal.bg }}
              onPointerDown={down}
              onPointerMove={move}
              onPointerUp={up}
              onDoubleClick={(e) => {
                if (comparing || (e.target as Element).closest("[data-node],[data-edge]")) return
                const p = at(e)
                addNode(p.x, p.y)
              }}
            >
              <Defs uid={uid} pal={pal} grid={dia.grid ?? "lines"} />
              <g transform={`translate(${view.x} ${view.y}) scale(${view.k})`}>
                {(dia.grid ?? "lines") !== "none" && (
                  <rect
                    x={-50000}
                    y={-50000}
                    width={100000}
                    height={100000}
                    fill={`url(#${uid}-grid)`}
                  />
                )}
                <Scene
                  dia={dia}
                  pal={pal}
                  uid={uid}
                  routes={routes}
                  gone={diff ? { nodes: diff.goneNodes, edges: diff.goneEdges } : undefined}
                  changes={diff ?? undefined}
                  sel={sel}
                  marksOn={marksOn}
                  active={activeMark?.anchor.node}
                  handleOn={
                    sel?.type === "node" && !comparing && present === null ? sel.id : undefined
                  }
                  focus={focus}
                />
                {wire &&
                  (() => {
                    const a = dia.nodes.find((n) => n.id === wire.from)
                    if (!a) return null
                    const b = boxOf(a)
                    return (
                      <line
                        className="dia-wire"
                        x1={b.x + b.w}
                        y1={b.y + b.h / 2}
                        x2={wire.x}
                        y2={wire.y}
                        stroke={pal.select}
                        strokeWidth={2}
                        strokeDasharray="5 4"
                      />
                    )
                  })()}
              </g>
            </svg>
            {!dia.nodes.length && (
              <p className="dia-hint">
                Double-click to add a node. Drag from a selected node's dot to another node to
                connect them.
              </p>
            )}
            {present !== null && (
              <div className="dia-present">
                <button
                  type="button"
                  aria-label="Back"
                  disabled={present === 0}
                  onClick={() => setPresent((x) => Math.max(0, (x ?? 0) - 1))}
                >
                  <ChevronLeft {...ICON} />
                </button>
                <span>
                  {present === 0
                    ? stages.length
                      ? `Overview · ${stages.length} ${dia.nodes.some((n) => n.step) ? "steps" : "lanes"}`
                      : title
                    : `${present} / ${stages.length} · ${stages[present - 1]?.label ?? ""}`}
                </span>
                <button
                  type="button"
                  aria-label="Next"
                  disabled={present >= stages.length}
                  onClick={() => setPresent((x) => Math.min(stages.length, (x ?? 0) + 1))}
                >
                  <ChevronRight {...ICON} />
                </button>
                <button
                  type="button"
                  aria-label="Stop presenting"
                  title="Stop (Esc)"
                  onClick={() => setPresent(null)}
                >
                  <X {...ICON} />
                </button>
              </div>
            )}
            {present === null && (
              <div className="dia-zoom">
                <button
                  type="button"
                  title="Zoom out"
                  aria-label="Zoom out"
                  onClick={() => zoomBy(1 / 1.2)}
                >
                  <Minus {...ICON} />
                </button>
                <button
                  type="button"
                  className="dia-pct"
                  title="Back to 100%"
                  onClick={() => setView((v) => ({ ...v, k: 1 }))}
                >
                  {Math.round(view.k * 100)}%
                </button>
                <button
                  type="button"
                  title="Zoom in"
                  aria-label="Zoom in"
                  onClick={() => zoomBy(1.2)}
                >
                  <Plus {...ICON} />
                </button>
                <button
                  type="button"
                  title="Fit the diagram in the window"
                  aria-label="Fit"
                  onClick={fit}
                >
                  <Maximize {...ICON} />
                </button>
              </div>
            )}
          </div>
        </section>
      ) : (
        <section className="collab-main collab-empty">
          <p>{name ? "Opening…" : "Pick a diagram, or start one."}</p>
        </section>
      )}
    </div>
  )
}
