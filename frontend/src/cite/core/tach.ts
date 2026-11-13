import { TESSERACT_ITEM_KIND, TESSERACT_KEY_KIND, parseTesseractKey } from './kinds/tesseract'
import { TORQUE_ENTITIES } from './kinds/torque'
import type { RefIdentity } from './types'

// `tach:<service>/<id-or-key>`: how an agent points at something the console can preview
// without relying on pattern matching. Written as a markdown link so it reads as a normal
// label anywhere the console does not render it:
//   [Handoff: approval execution](tach:tesseract/01M4SCJHDM8FV6MDPSDTMZ0X44)
//   [the executor decision](tach:tesseract/project/example/workspace/decisions#approved_action_executor)
//   [CW-20261019-0028](tach:torque/CW-20261019-0028)
// The service name is normalised here, so aliases cost nothing to add.

const ULID = /^[0-7][0-9A-HJKMNP-TV-Z]{25}$/
const TESSERACT = new Set(['tesseract', 'tess', 'memory'])
const TORQUE = new Set(['torque', 'task', 'tasks'])

/** The reference a `tach:` href names, or null when the service, id or key is not one the console knows. */
export function parseTachHref(href: string | undefined): RefIdentity | null {
  const m = /^tach:\/{0,2}([A-Za-z][A-Za-z0-9_-]{0,31})\/(.+)$/i.exec((href ?? '').trim())
  if (!m) return null
  const service = m[1].toLowerCase()
  let rest: string
  try {
    rest = decodeURIComponent(m[2]).trim()
  } catch {
    return null
  }
  if (!rest) return null
  if (TESSERACT.has(service)) {
    const id = rest.toUpperCase()
    if (ULID.test(id)) return { kind: TESSERACT_ITEM_KIND, canonical: id }
    return parseTesseractKey(rest) ? { kind: TESSERACT_KEY_KIND, canonical: rest } : null
  }
  if (TORQUE.has(service)) {
    const id = rest.toUpperCase()
    const entity = TORQUE_ENTITIES.find((e) => new RegExp(`^${e.prefix}-\\d{8}-\\d{4}$`).test(id))
    return entity ? { kind: entity.id, canonical: id } : null
  }
  return null
}
