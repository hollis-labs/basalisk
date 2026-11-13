/**
 * Fragments: a saved Fragments Engine fragment as a reference. The server resolves it
 * (internal/api/frag_cite.go); this finds it in text. Ids are 64 hex characters, so only
 * explicit forms are taken, never a bare id (it could be any digest):
 *
 *   tach:fragment/<id>                  the reference form, also as a Markdown link target
 *   https://<fe>/sysop/reader/<id>      a link to the fragment in FE's reader
 */
import { isWordChar, matches, trimTrailing } from "./core/scan-utils"
import type { RefHit, RefIdentity, RefKind } from "./core/types"

export const FRAGMENT_KIND = "fragments.fragment"

const ID = "[0-9a-f]{64}"
const TACH = new RegExp(`tach:/{0,2}(?:fragment|frag|fe)/(${ID})`, "gi")
const READER = new RegExp(`https?://[^\\s/]+(?::\\d+)?/sysop/reader/(${ID})`, "gi")

export function createFragmentKind(): RefKind {
  return {
    id: FRAGMENT_KIND,
    label: "Fragment",
    icon: "fragment",
    // Above URLs, so a reader link becomes the fragment rather than a bare link.
    priority: 110,
    chipLabel: (ref) => `fragment ${ref.canonical.slice(0, 8)}`,
    // Opening shows the record in the viewer (the desktop's cite opener).
    open: () => ({ type: "none" }),
    scan(text) {
      const hits: RefHit[] = []
      for (const re of [TACH, READER]) {
        for (const m of matches(re, text)) {
          if (isWordChar(text[m.index - 1])) continue
          const end = trimTrailing(text, m.index, m.index + m[0].length)
          hits.push({ start: m.index, end, canonical: m[1].toLowerCase() })
        }
      }
      return hits
    },
  }
}

/** The fragment a `tach:` href names (for Markdown links), or null. */
export function parseFragmentHref(href: string | undefined): RefIdentity | null {
  const m = new RegExp(`^tach:/{0,2}(?:fragment|frag|fe)/(${ID})$`, "i").exec((href ?? "").trim())
  return m ? { kind: FRAGMENT_KIND, canonical: m[1].toLowerCase() } : null
}

/** The reference form for a saved fragment. */
export const fragmentRef = (id: string) => `tach:fragment/${id}`
