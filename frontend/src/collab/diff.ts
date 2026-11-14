/** A line of a comparison: kept, taken out, or put in, with its numbers on each side. */
export interface DiffLine {
  op: "same" | "del" | "ins"
  text: string
  a?: number
  b?: number
}

/**
 * The lines of `a` against the lines of `b` (longest common subsequence). For very long
 * texts the common start and end are set aside first, so the table stays small.
 */
export function diffLines(a: string, b: string): DiffLine[] {
  const x = a.split("\n")
  const y = b.split("\n")
  let head = 0
  while (head < x.length && head < y.length && x[head] === y[head]) head++
  let tail = 0
  while (
    tail < x.length - head &&
    tail < y.length - head &&
    x[x.length - 1 - tail] === y[y.length - 1 - tail]
  )
    tail++
  const xm = x.slice(head, x.length - tail)
  const ym = y.slice(head, y.length - tail)
  const out: DiffLine[] = []
  for (let i = 0; i < head; i++) out.push({ op: "same", text: x[i], a: i + 1, b: i + 1 })
  if (xm.length * ym.length > 4_000_000) {
    // Too much to line up: show the middle as replaced.
    for (let i = 0; i < xm.length; i++) out.push({ op: "del", text: xm[i], a: head + i + 1 })
    for (let i = 0; i < ym.length; i++) out.push({ op: "ins", text: ym[i], b: head + i + 1 })
  } else {
    const n = xm.length
    const m = ym.length
    const lcs = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1))
    for (let i = n - 1; i >= 0; i--)
      for (let j = m - 1; j >= 0; j--)
        lcs[i][j] = xm[i] === ym[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1])
    let i = 0
    let j = 0
    while (i < n || j < m) {
      if (i < n && j < m && xm[i] === ym[j]) {
        out.push({ op: "same", text: xm[i], a: head + i + 1, b: head + j + 1 })
        i++
        j++
      } else if (j < m && (i === n || lcs[i][j + 1] >= lcs[i + 1][j])) {
        out.push({ op: "ins", text: ym[j], b: head + j + 1 })
        j++
      } else {
        out.push({ op: "del", text: xm[i], a: head + i + 1 })
        i++
      }
    }
  }
  for (let k = 0; k < tail; k++) {
    const ai = x.length - tail + k
    const bi = y.length - tail + k
    out.push({ op: "same", text: x[ai], a: ai + 1, b: bi + 1 })
  }
  return out
}

/** How many lines went in and came out. */
export function diffStat(lines: DiffLine[]) {
  return {
    added: lines.filter((l) => l.op === "ins").length,
    removed: lines.filter((l) => l.op === "del").length,
  }
}

/** Where a quoted passage is in a text now: its offset and line range, or null when gone. */
export function locate(
  text: string,
  anchor: { quote?: string; prefix?: string; line_start?: number },
): { offset: number; lineStart: number; lineEnd: number } | null {
  const q = anchor.quote
  if (!q) return null
  // Every place it occurs; the one nearest the line it was marked on wins.
  const hits: number[] = []
  for (let at = text.indexOf(q); at >= 0 && hits.length < 200; at = text.indexOf(q, at + 1))
    hits.push(at)
  if (!hits.length) return null
  const lineOf = (offset: number) => text.slice(0, offset).split("\n").length
  let best = hits[0]
  if (hits.length > 1) {
    const withPrefix = anchor.prefix
      ? hits.filter((h) => text.slice(0, h).endsWith(anchor.prefix ?? ""))
      : []
    const pool = withPrefix.length ? withPrefix : hits
    const want = anchor.line_start ?? 1
    best = pool.reduce((a, b) => (Math.abs(lineOf(b) - want) < Math.abs(lineOf(a) - want) ? b : a))
  }
  const lineStart = lineOf(best)
  return { offset: best, lineStart, lineEnd: lineStart + q.split("\n").length - 1 }
}
