/**
 * Markdown editing in a textarea: wrap the selection (⌘B, ⌘I), make a link (⌘K), continue a
 * list on Enter, indent and outdent with Tab and ⇧Tab. Edits go through insertText, so the
 * browser's undo (⌘Z) keeps working.
 */

/** Replaces [start, end) with text, then selects [a, b), keeping undo. */
export function replace(
  el: HTMLTextAreaElement,
  start: number,
  end: number,
  text: string,
  a: number,
  b: number,
) {
  el.focus()
  el.setSelectionRange(start, end)
  // insertText is deprecated but is still the only way to edit a textarea with undo kept.
  const ok = document.execCommand?.("insertText", false, text)
  if (!ok) {
    el.setRangeText(text, start, end, "end")
    el.dispatchEvent(new Event("input", { bubbles: true }))
  }
  el.setSelectionRange(a, b)
}

/** ⌘B / ⌘I: wrap the selection in marks, or unwrap it if it already is. */
export function wrap(el: HTMLTextAreaElement, mark: string) {
  const { selectionStart: s, selectionEnd: e, value } = el
  const sel = value.slice(s, e)
  const n = mark.length
  if (value.slice(s - n, s) === mark && value.slice(e, e + n) === mark) {
    replace(el, s - n, e + n, sel, s - n, e - n)
    return
  }
  if (sel.startsWith(mark) && sel.endsWith(mark) && sel.length >= 2 * n) {
    const inner = sel.slice(n, -n)
    replace(el, s, e, inner, s, s + inner.length)
    return
  }
  replace(el, s, e, `${mark}${sel}${mark}`, s + n, s + n + sel.length)
}

/** ⌘K: [selection](url) with "url" selected to type over. */
export function link(el: HTMLTextAreaElement) {
  const { selectionStart: s, selectionEnd: e, value } = el
  const text = value.slice(s, e) || "link"
  const out = `[${text}](url)`
  const at = s + text.length + 3
  replace(el, s, e, out, at, at + 3)
}

const LIST = /^(\s*)([-*+]|\d+[.)])(\s+)(\[[ xX]\]\s+)?/

function lineAt(value: string, pos: number) {
  const start = value.lastIndexOf("\n", pos - 1) + 1
  const endIdx = value.indexOf("\n", pos)
  return {
    start,
    end: endIdx < 0 ? value.length : endIdx,
    text: value.slice(start, endIdx < 0 ? value.length : endIdx),
  }
}

/** Enter in a list item: the next item, or ending the list on an empty one. True if handled. */
export function continueList(el: HTMLTextAreaElement): boolean {
  const { selectionStart: s, selectionEnd: e, value } = el
  if (s !== e) return false
  const line = lineAt(value, s)
  const m = LIST.exec(line.text)
  if (!m || s < line.start + m[0].length) return false
  if (line.text.slice(m[0].length).trim() === "" && s === line.end) {
    // An empty item ends the list.
    replace(el, line.start, line.end, "", line.start, line.start)
    return true
  }
  const [, indent, marker, gap, box] = m
  const num = /^\d+/.exec(marker)
  const next = num ? `${Number(num[0]) + 1}${marker.slice(num[0].length)}` : marker
  const insert = `\n${indent}${next}${gap}${box ? "[ ] " : ""}`
  replace(el, s, s, insert, s + insert.length, s + insert.length)
  return true
}

/** Tab / ⇧Tab: indent or outdent the selected lines (two spaces); in a plain line, Tab inserts two spaces. */
export function indent(el: HTMLTextAreaElement, out: boolean) {
  const { selectionStart: s, selectionEnd: e, value } = el
  const first = lineAt(value, s)
  const multi = value.slice(s, e).includes("\n")
  if (!out && !multi && !LIST.test(first.text)) {
    replace(el, s, e, "  ", s + 2, s + 2)
    return
  }
  const last = lineAt(value, Math.max(s, e - (e > s && value[e - 1] === "\n" ? 1 : 0)))
  const block = value.slice(first.start, last.end)
  const lines = block.split("\n")
  let removedFirst = 0
  const changed = lines.map((l, i) => {
    if (!out) return `  ${l}`
    const cut = l.startsWith("  ") ? 2 : l.startsWith(" ") || l.startsWith("\t") ? 1 : 0
    if (i === 0) removedFirst = cut
    return l.slice(cut)
  })
  const text = changed.join("\n")
  const delta = text.length - block.length
  const a = out ? Math.max(first.start, s - removedFirst) : s + 2
  replace(el, first.start, last.end, text, a, Math.max(a, e + delta))
}

/** The chords the editor keeps from the desktop (data-keys-own). */
export const EDITOR_KEYS = "cmd+b cmd+i cmd+k cmd+f cmd+s"
