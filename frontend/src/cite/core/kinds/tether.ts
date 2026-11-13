import type { RefKind } from '../types'
import { isWordChar, matches } from '../scan-utils'

interface TetherObject {
  id: string
  label: string
  icon: string
  /** Matches one id; the whole match is the canonical id. */
  pattern: RegExp
}

// Hub object ids as agents quote them: catalog plans `cp_` + 24 hex, registry
// agents `agt_` + 10 lowercase alphanumerics.
export const TETHER_OBJECTS: TetherObject[] = [
  { id: 'tether.plan', label: 'Catalog plan', icon: 'plan', pattern: /cp_[0-9a-f]{24}/g },
  { id: 'tether.agent', label: 'Agent', icon: 'agent', pattern: /agt_[a-z0-9]{10}/g },
]

export function createTetherKinds(): RefKind[] {
  return TETHER_OBJECTS.map((o) => ({
    id: o.id,
    label: o.label,
    icon: o.icon,
    resolveOn: 'hover',
    priority: 85,
    scan(text) {
      const hits = []
      for (const m of matches(o.pattern, text)) {
        const start = m.index
        const end = start + m[0].length
        if (!isWordChar(text[start - 1]) && !isWordChar(text[end])) hits.push({ start, end, canonical: m[0] })
      }
      return hits
    },
    // The hub has no page for these; the drawer is the view.
    open: () => ({ type: 'none' }),
  }))
}
