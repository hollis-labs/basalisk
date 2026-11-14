/** Text in the drawing: the fonts, and measuring, fitting and wrapping a line. */
export const SANS = '-apple-system, "SF Pro Text", "Inter", "Helvetica Neue", Arial, sans-serif'
export const MONO = '"SF Mono", "JetBrains Mono", "IBM Plex Mono", Menlo, monospace'

let ctx: CanvasRenderingContext2D | null = null
/** The width of text in a font (for fitting labels). */
export function measure(text: string, font: string): number {
  if (typeof document === "undefined") return text.length * 7
  ctx ??= document.createElement("canvas").getContext("2d")
  if (!ctx) return text.length * 7
  ctx.font = font
  return ctx.measureText(text).width
}
export function fit(text: string, max: number, font: string): string {
  if (measure(text, font) <= max) return text
  let t = text
  while (t.length > 1 && measure(`${t}…`, font) > max) t = t.slice(0, -1)
  return `${t}…`
}
export function wrap(text: string, max: number, font: string, lines = 2): string[] {
  const words = text.split(/\s+/)
  const out: string[] = []
  let cur = ""
  for (const w of words) {
    const next = cur ? `${cur} ${w}` : w
    if (measure(next, font) <= max || !cur) cur = next
    else {
      out.push(cur)
      cur = w
    }
  }
  if (cur) out.push(cur)
  if (out.length > lines) {
    const kept = out.slice(0, lines)
    kept[lines - 1] = fit(`${kept[lines - 1]} ${out.slice(lines).join(" ")}`, max, font)
    return kept
  }
  return out
}
