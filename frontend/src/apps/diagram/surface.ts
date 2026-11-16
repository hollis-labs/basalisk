/**
 * A diagram window as an ADE surface: its nodes and edges are parts the owner and the
 * agent can both point at. Editing a diagram stays with the collab tools (collab_write);
 * this is for looking, pointing and walking through it.
 */
import type { SurfaceAdapter } from "@hollis-labs/ade-core"
import type { CollabDoc } from "../../api/collab"
import type { Diagram } from "./model"

export const diagramSurfaceId = (winId: string) => `diagram:${winId}`

export interface DiagramSurfaceState {
  winId: string
  doc: CollabDoc | null
  dia: Diagram
  dirty: boolean
  selected: { type: string; id: string } | null
  svg: SVGSVGElement | null
  select: (type: "node" | "edge", id: string) => void
}

const esc = (id: string) => (typeof CSS !== "undefined" && CSS.escape ? CSS.escape(id) : id)

export function diagramSurface(s: DiagramSurfaceState): SurfaceAdapter {
  const find = (kind: string, id: string) =>
    s.svg?.querySelector<SVGGElement>(`[data-${kind === "edge" ? "edge" : "node"}="${esc(id)}"]`) ??
    null
  const label = (id: string) => s.dia.nodes.find((n) => n.id === id)?.label || id
  return {
    info: { id: diagramSurfaceId(s.winId), app: "diagram", title: s.doc?.title ?? "Diagram" },
    observe: () => {
      if (!s.doc) return { summary: "No diagram is open in this window.", actions: [] }
      return {
        title: s.doc.title,
        resource: {
          ref: `caster://collab/${s.doc.name}`,
          rev: `${s.doc.rev}${s.dirty ? "+unsaved" : ""}`,
        },
        summary: `The diagram “${s.doc.title}” (${s.doc.name}): ${s.dia.nodes.length} nodes, ${s.dia.edges.length} edges. Its parts are kind "node" and kind "edge", by id. To change the diagram use collab_write; here you look, point and guide.`,
        targets: [
          ...s.dia.nodes.slice(0, 120).map((n) => ({
            kind: "node",
            id: n.id,
            label: n.label,
            state: { ...(n.kind ? { kind: n.kind } : {}), ...(n.group ? { group: n.group } : {}) },
          })),
          ...s.dia.edges.slice(0, 160).map((e) => ({
            kind: "edge",
            id: e.id,
            label: `${label(e.from)} → ${label(e.to)}${e.label ? ` (${e.label})` : ""}`,
          })),
        ],
        actions: [
          {
            name: "select",
            level: "act",
            description:
              "Select a node or an edge of the diagram for the owner, which opens its details beside the canvas. Give node or edge (an id).",
            input_schema: {
              type: "object",
              properties: {
                node: { type: "string", description: "A node's id." },
                edge: { type: "string", description: "An edge's id." },
              },
              additionalProperties: false,
            },
          },
        ],
        checks: [
          {
            name: "selected",
            description: "The owner has this node (or edge) selected.",
            args_schema: {
              type: "object",
              properties: { node: { type: "string" }, edge: { type: "string" } },
              additionalProperties: false,
            },
          },
        ],
      }
    },
    act: (action, input) => {
      if (action !== "select")
        return {
          outcome: "rejected",
          code: "invalid",
          reason: `A diagram does not do "${action}".`,
        }
      const node = typeof input.node === "string" ? input.node : ""
      const edge = typeof input.edge === "string" ? input.edge : ""
      if (node && s.dia.nodes.some((n) => n.id === node)) {
        s.select("node", node)
        return { outcome: "applied" }
      }
      if (edge && s.dia.edges.some((e) => e.id === edge)) {
        s.select("edge", edge)
        return { outcome: "applied" }
      }
      return {
        outcome: "rejected",
        code: "invalid",
        reason: `The diagram has no ${node ? `node "${node}"` : `edge "${edge}"`}. Look at it for its parts.`,
      }
    },
    check: (name, args) =>
      name === "selected" &&
      !!s.selected &&
      ((s.selected.type === "node" && s.selected.id === args.node) ||
        (s.selected.type === "edge" && s.selected.id === args.edge)),
    resolve: (anchor) => {
      const t = anchor.target
      if (!t) return null
      const r = find(t.kind, t.id)?.getBoundingClientRect()
      return r && r.width > 0 ? { x: r.left, y: r.top, w: r.width, h: r.height } : null
    },
    bounds: () => {
      const r = s.svg?.getBoundingClientRect()
      return r ? { x: r.left, y: r.top, w: r.width, h: r.height } : null
    },
    describe: (anchor) =>
      anchor.target?.kind === "node" ? `the node “${label(anchor.target.id)}”` : "",
  }
}
