import { FilePlus2, GitCompare, MessageSquarePlus, Plus, Save, Scan, Trash2 } from "lucide-react"
import {
  type PointerEvent as ReactPointerEvent,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react"
import {
  COLLAB_CHANGED,
  type CollabAnchor,
  type CollabDoc,
  type CollabMark,
  type CollabRevision,
  collab,
  isConflict,
} from "../../api/collab"
import { collabOpen } from "../../collab/agent"
import { MarkComposer, MarksPanel } from "../../collab/marks"
import { useDesk } from "../../desktop/context"
import type { WindowProps } from "../../desktop/registry"
import {
  type Change,
  compare,
  type DEdge,
  type Diagram,
  type DNode,
  edgePoint,
  empty,
  freshId,
  KINDS,
  NODE_H,
  NODE_W,
  type NodeKind,
  parse,
  serialize,
} from "./model"
import "../../collab/collab.css"
import "./diagram.css"

const ICON = { size: 14, strokeWidth: 1.6, "aria-hidden": true } as const
type Sel = { type: "node" | "edge"; id: string } | null
interface View {
  x: number
  y: number
  k: number
}

/**
 * The Diagram window. Double-click the canvas for a node; drag a node to move it; drag from
 * the dot on a selected node to another node for an edge; the inspector on the right edits
 * what is selected and marks it. ⌘S saves a revision; Compare shows any revision against
 * this one on the canvas (green added, amber changed, red removed).
 */
export function DiagramWindow({ winId, args }: WindowProps) {
  const desk = useDesk()
  const [docs, setDocs] = useState<CollabDoc[]>([])
  const [name, setName] = useState(typeof args.name === "string" ? args.name : "")
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
  const drag = useRef<{
    kind: "pan" | "node"
    id?: string
    sx: number
    sy: number
    ox: number
    oy: number
    moved: boolean
  } | null>(null)
  const nameRef = useRef(name)
  nameRef.current = name
  const saved = doc ? doc.body : ""
  const dirty = !!doc && (serialize(dia) !== serialize(parse(saved)) || title !== doc.title)
  const dirtyRef = useRef(dirty)
  dirtyRef.current = dirty

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
        setDia(parse(r.doc.body))
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
    if (fromRev === doc.rev) return setBefore(parse(doc.body))
    let live = true
    void collab
      .get(doc.name, fromRev)
      .then((r) => live && setBefore(parse(r.doc.body)))
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
      setDia(parse(d.body))
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
  const create = async () => {
    try {
      const d = await collab.create({
        kind: "diagram",
        title: "Untitled diagram",
        body: serialize(empty()),
      })
      await loadList()
      setName(d.name)
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
  const addNode = (x: number, y: number) => {
    const id = freshId("node", (i) => dia.nodes.some((n) => n.id === i))
    setDia((d) => ({
      ...d,
      nodes: [
        ...d.nodes,
        { id, label: "New node", kind: "service", x: x - NODE_W / 2, y: y - NODE_H / 2 },
      ],
    }))
    setSel({ type: "node", id })
  }
  const removeSel = () => {
    if (!sel) return
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
    const target = e.target as Element
    const handle = target.closest("[data-handle]")
    const node = target.closest<SVGGElement>("[data-node]")
    const edge = target.closest<SVGGElement>("[data-edge]")
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
    else if (d.id) patchNode(d.id, { x: d.ox + dx / view.k, y: d.oy + dy / view.k })
  }
  const up = (e: ReactPointerEvent<SVGSVGElement>) => {
    if (wire) {
      // The node under the pointer, if any, is where the edge goes.
      const p = at(e)
      const to = dia.nodes.find(
        (n) =>
          n.id !== wire.from &&
          p.x >= n.x &&
          p.x <= n.x + NODE_W &&
          p.y >= n.y &&
          p.y <= n.y + NODE_H,
      )
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
        const k = Math.min(2.5, Math.max(0.25, v.k * (e.deltaY < 0 ? 1.1 : 1 / 1.1)))
        const px = e.clientX - box.left
        const py = e.clientY - box.top
        return { k, x: px - ((px - v.x) / v.k) * k, y: py - ((py - v.y) / v.k) * k }
      })
    }
    el.addEventListener("wheel", onWheel, { passive: false })
    return () => el.removeEventListener("wheel", onWheel)
  })
  const fit = () => {
    const box = svg.current?.getBoundingClientRect()
    if (!box || !dia.nodes.length) return setView({ x: 40, y: 30, k: 1 })
    const x0 = Math.min(...dia.nodes.map((n) => n.x))
    const y0 = Math.min(...dia.nodes.map((n) => n.y))
    const x1 = Math.max(...dia.nodes.map((n) => n.x + NODE_W))
    const y1 = Math.max(...dia.nodes.map((n) => n.y + NODE_H))
    const k = Math.min(
      1.5,
      Math.max(0.25, Math.min((box.width - 80) / (x1 - x0), (box.height - 80) / (y1 - y0))),
    )
    setView({
      k,
      x: (box.width - (x1 - x0) * k) / 2 - x0 * k,
      y: (box.height - (y1 - y0) * k) / 2 - y0 * k,
    })
  }

  const diff = useMemo(
    () => (comparing && before ? compare(before, dia) : null),
    [comparing, before, dia],
  )
  const shownNodes = useMemo(() => [...(diff?.goneNodes ?? []), ...dia.nodes], [diff, dia.nodes])
  const shownEdges = useMemo(() => [...(diff?.goneEdges ?? []), ...dia.edges], [diff, dia.edges])
  const nodeAt = (id: string) =>
    dia.nodes.find((n) => n.id === id) ?? diff?.goneNodes.find((n) => n.id === id)
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

  const groups = dia.groups
    .map((g) => {
      const members = dia.nodes.filter((n) => n.group === g.id)
      if (!members.length) return null
      const x = Math.min(...members.map((n) => n.x)) - 18
      const y = Math.min(...members.map((n) => n.y)) - 34
      return {
        ...g,
        x,
        y,
        w: Math.max(...members.map((n) => n.x + NODE_W)) + 18 - x,
        h: Math.max(...members.map((n) => n.y + NODE_H)) + 18 - y,
      }
    })
    .filter((g) => g !== null)

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
    // biome-ignore lint/a11y/noStaticElementInteractions: the window's own keys (⌘S, Delete)
    <div
      className="collab"
      onKeyDown={(e) => {
        const t = e.target as HTMLElement
        const typing = t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT"
        if ((e.metaKey || e.ctrlKey) && e.key === "s") {
          e.preventDefault()
          if (dirty) void save()
        } else if (!typing && !comparing && (e.key === "Delete" || e.key === "Backspace") && sel) {
          e.preventDefault()
          removeSel()
        }
      }}
    >
      <nav className="collab-list">
        <header>
          <strong>Diagrams</strong>
          <button
            type="button"
            title="New diagram"
            aria-label="New diagram"
            onClick={() => void create()}
          >
            <FilePlus2 {...ICON} />
          </button>
        </header>
        {docs.map((d) => (
          <button
            key={d.name}
            type="button"
            data-on={d.name === name}
            onClick={() => setName(d.name)}
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
      {doc ? (
        <section className="collab-main">
          <header className="collab-bar">
            <input
              className="collab-title"
              value={title}
              aria-label="Title"
              onChange={(e) => setTitle(e.target.value)}
            />
            <span className="collab-rev" title={doc.name}>
              r{doc.rev} · {dia.nodes.length} nodes · {dia.edges.length} edges
            </span>
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
            <button
              type="button"
              className="viewer-tool"
              title="Fit the diagram in the window"
              onClick={fit}
            >
              <Scan {...ICON} /> Fit
            </button>
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
              className="collab-primary"
              disabled={!dirty}
              title="Save a new revision (⌘S)"
              onClick={() => void save()}
            >
              <Save {...ICON} /> Save
            </button>
          </header>
          {comparing && (
            <p className="collab-status" data-tone="ok">
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
          {status.text && !comparing && (
            <p className="collab-status" data-tone={status.tone} role="status">
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
            onPointerDown={down}
            onPointerMove={move}
            onPointerUp={up}
            onDoubleClick={(e) => {
              if (comparing || (e.target as Element).closest("[data-node],[data-edge]")) return
              const p = at(e)
              addNode(p.x, p.y)
            }}
          >
            <defs>
              <marker
                id={`arrow-${winId}`}
                viewBox="0 0 10 10"
                refX="9"
                refY="5"
                markerWidth="7"
                markerHeight="7"
                orient="auto-start-reverse"
              >
                <path d="M0 0L10 5L0 10z" fill="context-stroke" />
              </marker>
            </defs>
            <g transform={`translate(${view.x} ${view.y}) scale(${view.k})`}>
              {groups.map((g) => (
                <g key={g.id} className="dia-group">
                  <rect x={g.x} y={g.y} width={g.w} height={g.h} rx="12" />
                  <text x={g.x + 12} y={g.y + 20}>
                    {g.label}
                  </text>
                </g>
              ))}
              {shownEdges.map((e) => {
                const a = nodeAt(e.from)
                const b = nodeAt(e.to)
                if (!a || !b) return null
                const p1 = edgePoint(a, b.x + NODE_W / 2, b.y + NODE_H / 2)
                const p2 = edgePoint(b, a.x + NODE_W / 2, a.y + NODE_H / 2)
                const change: Change = diff?.edges.get(e.id) ?? "same"
                const n = marksOn("edge", e.id)
                return (
                  <g
                    key={`${change}:${e.id}`}
                    className="dia-edge"
                    data-edge={e.id}
                    data-change={change}
                    data-on={sel?.type === "edge" && sel.id === e.id}
                  >
                    <line className="dia-hit" x1={p1.x} y1={p1.y} x2={p2.x} y2={p2.y} />
                    <line
                      x1={p1.x}
                      y1={p1.y}
                      x2={p2.x}
                      y2={p2.y}
                      markerEnd={`url(#arrow-${winId})`}
                    />
                    {(e.label || n > 0) && (
                      <text x={(p1.x + p2.x) / 2} y={(p1.y + p2.y) / 2 - 6} textAnchor="middle">
                        {e.label}
                        {n > 0 ? ` ●${n}` : ""}
                      </text>
                    )}
                  </g>
                )
              })}
              {wire &&
                (() => {
                  const a = nodeAt(wire.from)
                  if (!a) return null
                  const p = edgePoint(a, wire.x, wire.y)
                  return <line className="dia-wire" x1={p.x} y1={p.y} x2={wire.x} y2={wire.y} />
                })()}
              {shownNodes.map((n) => {
                const change: Change = diff?.nodes.get(n.id) ?? "same"
                const on = sel?.type === "node" && sel.id === n.id
                const count = marksOn("node", n.id)
                return (
                  <g
                    key={`${change === "removed" ? "gone:" : ""}${n.id}`}
                    className="dia-node"
                    data-node={n.id}
                    data-kind={n.kind ?? "service"}
                    data-change={change}
                    data-on={on}
                    data-active={marks.some((m) => m.id === active && m.anchor.node === n.id)}
                    transform={`translate(${n.x} ${n.y})`}
                  >
                    <rect
                      width={NODE_W}
                      height={NODE_H}
                      rx={n.kind === "store" ? 22 : n.kind === "actor" ? 27 : 8}
                    />
                    <text
                      className="dia-label"
                      x={NODE_W / 2}
                      y={n.kind ? 24 : 32}
                      textAnchor="middle"
                    >
                      {n.label.length > 22 ? `${n.label.slice(0, 21)}…` : n.label}
                    </text>
                    {n.kind && (
                      <text className="dia-kind" x={NODE_W / 2} y={41} textAnchor="middle">
                        {n.kind}
                      </text>
                    )}
                    {count > 0 && (
                      <g className="dia-badge" transform={`translate(${NODE_W - 4} 4)`}>
                        <circle r="9" />
                        <text textAnchor="middle" y="3.5">
                          {count}
                        </text>
                      </g>
                    )}
                    {on && !comparing && (
                      <circle
                        className="dia-handle"
                        data-handle
                        cx={NODE_W}
                        cy={NODE_H / 2}
                        r="7"
                      />
                    )}
                  </g>
                )
              })}
            </g>
          </svg>
          {!dia.nodes.length && (
            <p className="dia-hint">
              Double-click to add a node. Drag from a selected node's dot to another node to connect
              them.
            </p>
          )}
        </section>
      ) : (
        <section className="collab-main collab-empty">
          <p>{name ? "Opening…" : "Pick a diagram, or start one."}</p>
        </section>
      )}
      {doc && (
        <div className="collab-side">
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
                Group
                <input
                  value={dia.groups.find((g) => g.id === node.group)?.label ?? node.group ?? ""}
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
                      nodes: d.nodes.map((n) => (n.id === node.id ? { ...n, group: id } : n)),
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
                  rows={3}
                  value={node.note ?? ""}
                  onChange={(e) => patchNode(node.id, { note: e.target.value || undefined })}
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
                  onChange={(e) => patchEdge(edge.id, { label: e.target.value || undefined })}
                />
              </label>
              <footer>
                <code>
                  {edge.from} → {edge.to}
                </code>
                <button
                  type="button"
                  onClick={() => patchEdge(edge.id, { from: edge.to, to: edge.from })}
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
  )
}
