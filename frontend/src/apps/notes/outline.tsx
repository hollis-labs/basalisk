import { type RefObject, useEffect, useState } from "react"
import type { FileRef } from "../../desktop/registry"
import {
  colourCode,
  forgetPage,
  type Heading,
  markHeadings,
  parseFileLink,
  scrollToAnchor,
} from "./rich"

/**
 * After each render of a note's page: heading ids, code colour, and the page's own links
 * (#anchors scroll the page, links to other files open them). Returns the headings.
 */
export function useRichPage(
  page: RefObject<HTMLElement | null>,
  text: string,
  openFile: (file: FileRef, anchor: string) => void,
  live: boolean,
): Heading[] {
  const [headings, setHeadings] = useState<Heading[]>([])
  // biome-ignore lint/correctness/useExhaustiveDependencies: text is what the page rendered
  useEffect(() => {
    const el = page.current
    if (!el || !live) return
    const next = markHeadings(el)
    setHeadings((old) =>
      old.length === next.length &&
      old.every((h, i) => h.id === next[i].id && h.level === next[i].level)
        ? old
        : next,
    )
    colourCode(el)
  }, [page, text, live])

  useEffect(() => {
    const el = page.current
    if (!el || !live) return
    const onClick = (e: MouseEvent) => {
      const a = (e.target as Element | null)?.closest?.("a")
      const href = a?.getAttribute("href")
      if (!a || !href?.startsWith("#") || !el.contains(a)) return
      e.preventDefault()
      e.stopPropagation()
      const link = parseFileLink(href)
      if (link) openFile(link.file, link.anchor)
      else scrollToAnchor(el, href.slice(1))
    }
    el.addEventListener("click", onClick, true)
    return () => {
      el.removeEventListener("click", onClick, true)
      forgetPage(el)
    }
  }, [page, openFile, live])
  return headings
}

/** The note's headings; a click scrolls the page to one. */
export function Outline({
  headings,
  page,
}: {
  headings: Heading[]
  page: RefObject<HTMLElement | null>
}) {
  const top = Math.min(...headings.map((h) => h.level))
  return (
    <nav className="notes-outline" aria-label="Outline">
      {headings.length ? (
        headings.map((h) => (
          <button
            key={h.id}
            type="button"
            style={{ paddingLeft: 10 + (h.level - top) * 12 }}
            title={h.text}
            onClick={() => page.current && scrollToAnchor(page.current, h.id)}
          >
            {h.text}
          </button>
        ))
      ) : (
        <p>No headings.</p>
      )}
    </nav>
  )
}
