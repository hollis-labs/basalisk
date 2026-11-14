import { BellRing } from "lucide-react"
import { useDesk } from "../desktop/context"
import { useAttention } from "./attention"

/** The bar's "needs you" count: shown only when something waits on the owner; opens the assistant. */
export function AttentionChip() {
  const desk = useDesk()
  const { on, needs, answers } = useAttention()
  const n = needs.length
  if (!on || (!n && !answers.length)) return null
  return (
    <button
      type="button"
      className="desk-bar-button"
      data-on="true"
      title={
        n
          ? `${n} ${n === 1 ? "thing needs" : "things need"} you. Open the assistant.`
          : "An answer came back to the assistant. Open it."
      }
      onClick={() => desk.togglePanel("agent", true)}
    >
      <BellRing aria-hidden="true" size={14} strokeWidth={1.6} />
      <span>{n ? `${n} for you` : "answer"}</span>
    </button>
  )
}
