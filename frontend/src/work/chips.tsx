/** The kind chips a view shows above itself: which kinds it shows, kept in the view item. */
import type { WorkKind } from "../api/work"
import { kindIcon } from "./util"

export function KindChips({
  kinds,
  shown,
  set,
}: {
  kinds: WorkKind[]
  shown: string[]
  set: (kinds: string[]) => void
}) {
  return (
    <>
      {kinds.map((k) => {
        const on = shown.includes(k.kind)
        const Icon = kindIcon(k.icon)
        return (
          <button
            type="button"
            key={k.kind}
            className="work-chip"
            data-on={on}
            data-color={k.color}
            onClick={() =>
              set(
                on
                  ? shown.filter((x) => x !== k.kind)
                  : kinds.map((x) => x.kind).filter((x) => x === k.kind || shown.includes(x)),
              )
            }
            title={on ? `Hide ${k.plural || k.label}` : `Show ${k.plural || k.label}`}
          >
            <Icon size={12} /> {k.plural || k.label}
          </button>
        )
      })}
    </>
  )
}
