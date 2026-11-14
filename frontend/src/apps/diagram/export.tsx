/**
 * Export: the diagram as a standalone SVG (the same drawing as on screen, its colours written
 * in) or as a PNG drawn from that SVG at 1, 2 or 3 times. Framed around what is drawn, with
 * room for an optional title and the legend; the background and the grid can be left out
 * (a transparent background for slides and documents).
 */
import { renderToStaticMarkup } from "react-dom/server"
import type { Diagram } from "./model"
import { Defs, extent, LEGEND_H, laneHeads, legendOf, Scene } from "./render"
import { routeAll } from "./route"
import { SANS } from "./text"
import type { Palette } from "./theme"

export interface ExportOptions {
  title?: string
  background: boolean
  grid: boolean
  legend: boolean
  /** Room around the drawing, in plane units. */
  pad?: number
}

/** The drawing as SVG text, and its size in plane units. */
export function buildSvg(
  dia: Diagram,
  pal: Palette,
  o: ExportOptions,
): { svg: string; w: number; h: number } {
  const pad = o.pad ?? 56
  const ext = extent(dia)
  const legend = o.legend && legendOf(dia).length > 0
  const titleH = o.title ? 74 : 0
  const x = ext.x - pad
  const y = ext.y - pad - titleH
  const w = ext.w + pad * 2
  const h = ext.h + pad * 2 + titleH + (legend ? LEGEND_H + 44 : 0)
  const uid = "x"
  const routes = routeAll(dia.nodes, dia.edges, laneHeads(dia))
  const body = renderToStaticMarkup(
    <svg
      xmlns="http://www.w3.org/2000/svg"
      xmlnsXlink="http://www.w3.org/1999/xlink"
      width={w}
      height={h}
      viewBox={`${x} ${y} ${w} ${h}`}
    >
      <title>{o.title || "Diagram"}</title>
      <Defs uid={uid} pal={pal} grid={dia.grid ?? "lines"} />
      {o.background && <rect x={x} y={y} width={w} height={h} fill={pal.bg} />}
      {o.background && o.grid && (dia.grid ?? "lines") !== "none" && (
        <rect x={x} y={y} width={w} height={h} fill={`url(#${uid}-grid)`} />
      )}
      {o.title && (
        <text
          x={ext.x}
          y={ext.y - 40}
          fontFamily={SANS}
          fontSize={26}
          fontWeight={700}
          fill={pal.text}
        >
          {o.title}
        </text>
      )}
      <Scene dia={dia} pal={pal} uid={uid} routes={routes} legend={legend} />
    </svg>,
  )
  return { svg: `<?xml version="1.0" encoding="UTF-8"?>\n${body}\n`, w, h }
}

/** A PNG of the SVG at a scale (drawn by the browser from the SVG text). */
export function svgToPng(svg: string, w: number, h: number, scale: number): Promise<Blob> {
  return new Promise((resolve, reject) => {
    const img = new Image()
    img.onload = () => {
      const c = document.createElement("canvas")
      c.width = Math.round(w * scale)
      c.height = Math.round(h * scale)
      const g = c.getContext("2d")
      if (!g) return reject(new Error("No canvas to draw on."))
      g.scale(scale, scale)
      g.drawImage(img, 0, 0, w, h)
      c.toBlob(
        (b) => (b ? resolve(b) : reject(new Error("The picture could not be made."))),
        "image/png",
      )
    }
    img.onerror = () => reject(new Error("The drawing could not be read as a picture."))
    img.src = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`
  })
}

/** Write bytes to a file in a Files place, making the folder if needed; never over a file. */
export async function saveExport(
  root: string,
  folder: string,
  name: string,
  body: Blob | string,
): Promise<string> {
  const headers = { "X-Requested-With": "XMLHttpRequest", "Content-Type": "application/json" }
  const dir = folder.replace(/^\/+|\/+$/g, "")
  if (dir) {
    const r = await fetch("/api/files/mkdir", {
      method: "POST",
      credentials: "same-origin",
      headers,
      body: JSON.stringify({ root, path: dir }),
    })
    if (!r.ok && r.status !== 409) throw new Error(`The folder could not be made (${r.status}).`)
  }
  const dot = name.lastIndexOf(".")
  const stem = dot > 0 ? name.slice(0, dot) : name
  const ext = dot > 0 ? name.slice(dot) : ""
  let path = ""
  for (let n = 1; n < 100; n++) {
    const file = n === 1 ? `${stem}${ext}` : `${stem}-${n}${ext}`
    path = dir ? `${dir}/${file}` : file
    const r = await fetch("/api/files/create", {
      method: "POST",
      credentials: "same-origin",
      headers,
      body: JSON.stringify({ root, path }),
    })
    if (r.ok) break
    if (r.status !== 409) throw new Error(`The file could not be made (${r.status}).`)
  }
  const w = await fetch(
    `/api/files/content?root=${encodeURIComponent(root)}&path=${encodeURIComponent(path)}`,
    {
      method: "PUT",
      credentials: "same-origin",
      headers: { "X-Requested-With": "XMLHttpRequest" },
      body,
    },
  )
  if (!w.ok) throw new Error(`The file could not be written (${w.status}).`)
  return path
}
