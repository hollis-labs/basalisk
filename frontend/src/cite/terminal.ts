import type { ILink, ILinkProvider, Terminal } from "@xterm/xterm"
import { hideCard, openCite, refsIn, showCard } from "./index"

/**
 * References in terminal output: a task id an agent printed is a link. Hovering shows the
 * preview card, activating opens the record. Only kinds with a record behind them are
 * linked; URLs are the web-links addon's, and paths would underline every listing.
 */
export function citeLinkProvider(term: Terminal): ILinkProvider {
  return {
    provideLinks(y, callback) {
      const line = term.buffer.active.getLine(y - 1)
      if (!line) {
        callback(undefined)
        return
      }
      const text = line.translateToString(true)
      const links: ILink[] = refsIn(text, true).map((r) => {
        const ref = { kind: r.kind, canonical: r.canonical, meta: r.meta }
        return {
          range: { start: { x: r.start + 1, y }, end: { x: r.end, y } },
          text: r.raw,
          activate: () => openCite(ref),
          hover: (event: MouseEvent) =>
            showCard(ref, {
              left: event.clientX,
              right: event.clientX,
              top: event.clientY - 10,
              bottom: event.clientY + 10,
            }),
          leave: () => hideCard(),
        }
      })
      callback(links.length ? links : undefined)
    },
  }
}
