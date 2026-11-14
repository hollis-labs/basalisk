/**
 * Looking: the agent sees pixels. desktop_look sends it a picture of a window as it is on
 * screen (marks drawn on it included), so what the owner points at can be seen and not only
 * described. The picture is taken by the desktop app of its own window; in a plain browser
 * there is nothing to take it, and the tool says so.
 */
import type { DeskAPI } from "../desktop/registry"
import { registerAgentTools, type ToolImage, withImages } from "./tools"

/** The longest side of a picture sent to the model, in pixels. */
const MAX_SIDE = 1568

/** Draw a source (or a part of it) onto a canvas no larger than MAX_SIDE and encode it. */
export function encodePicture(
  source: CanvasImageSource,
  crop: { x: number; y: number; w: number; h: number },
  draw?: (ctx: CanvasRenderingContext2D, scale: number) => void,
): ToolImage & { width: number; height: number } {
  const scale = Math.min(1, MAX_SIDE / Math.max(crop.w, crop.h))
  const canvas = document.createElement("canvas")
  canvas.width = Math.max(1, Math.round(crop.w * scale))
  canvas.height = Math.max(1, Math.round(crop.h * scale))
  const ctx = canvas.getContext("2d")
  if (!ctx) throw new Error("A picture could not be drawn.")
  ctx.drawImage(source, crop.x, crop.y, crop.w, crop.h, 0, 0, canvas.width, canvas.height)
  draw?.(ctx, scale)
  // JPEG keeps a screenshot small; quality drops until it fits what the server takes.
  for (const q of [0.82, 0.7, 0.55, 0.4]) {
    const data = canvas.toDataURL("image/jpeg", q).split(",")[1] ?? ""
    if (data.length < 1_400_000 || q === 0.4)
      return { media_type: "image/jpeg", data, width: canvas.width, height: canvas.height }
  }
  throw new Error("The picture is too large to send.")
}

/** A picture of the desktop app's window, or of one window on it. */
async function lookAt(desk: DeskAPI, winId?: string) {
  if (!desk.shellStatus()?.available)
    throw new Error(
      "Looking needs the desktop app: in a browser there is nothing to take the picture. Read the window's text instead (window_context, collab_read).",
    )
  const res = await fetch("/api/shell/snapshot", { credentials: "same-origin" })
  if (!res.ok) throw new Error("The desktop could not take a picture of itself.")
  const shot = await createImageBitmap(await res.blob())
  // The snapshot is in device pixels; windows are placed in CSS pixels.
  const k = shot.width / window.innerWidth
  let crop = { x: 0, y: 0, w: shot.width, h: shot.height }
  let what = "the whole desktop"
  if (winId) {
    const g = desk.geometry(winId)
    const win = desk.wmState().wins.find((w) => w.id === winId)
    if (!g || !win) throw new Error(`There is no window ${winId}.`)
    if (!g.visible) throw new Error("That window is minimized; open it first (desktop_focus).")
    const x = Math.max(0, Math.round(g.rect.x * k))
    const y = Math.max(0, Math.round(g.rect.y * k))
    crop = {
      x,
      y,
      w: Math.min(shot.width - x, Math.round(g.rect.w * k)),
      h: Math.min(shot.height - y, Math.round(g.rect.h * k)),
    }
    what = `the window “${win.title.replace(/^● /, "")}” (${win.app})${g.covered ? "; something is drawn over part of it" : ""}`
  }
  const pic = encodePicture(shot, crop)
  shot.close()
  return { pic, what }
}

registerAgentTools([
  {
    name: "desktop_look",
    level: "read",
    description:
      "See a window as it is on screen: you are sent a picture of it. Use it when the owner points at something visual (a mark drawn on an image, a mock, a video frame, a diagram, a layout problem) or when the text of a window does not tell you what it looks like. Give window (an id from the desktop snapshot) to see one window; leave it out to see the focused window; set all to true for the whole desktop. Marks the owner drew in the Viewer are numbered on the picture, in the order collab_read lists the open marks that have a region. A Browser window's page is not in the picture (it is a separate view; use the browser tools for it). Only the last few looks stay visible to you; look again if you need an older one. Prefer reading text (window_context, collab_read, notes) when text answers the question: a look costs more.",
    input_schema: {
      type: "object",
      properties: {
        window: {
          type: "string",
          description: "The window's id. The focused window when left out.",
        },
        all: { type: "boolean", description: "The whole desktop, not one window." },
      },
      additionalProperties: false,
    },
    describe: (input) => (input.all ? "Look at the whole desktop" : "Look at a window"),
    run: async (input, desk) => {
      const winId = input.all
        ? undefined
        : typeof input.window === "string" && input.window
          ? input.window
          : (desk.wmState().focus ?? undefined)
      const { pic, what } = await lookAt(desk, winId)
      return withImages(`A picture of ${what}, ${pic.width}×${pic.height} pixels.`, [
        { media_type: pic.media_type, data: pic.data },
      ])
    },
  },
])
