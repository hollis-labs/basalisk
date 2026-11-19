import { BellRing } from "lucide-react"
import { useDesk } from "../desktop/context"
import { seenNews, useAttention } from "./attention"
import { useGate } from "./gate"

/** The bar's "needs you" count: shown only when something waits on the owner; opens the assistant. */
export function AttentionChip() {
  const desk = useDesk()
  const { on, needs, answers, news } = useAttention()
  const gate = useGate()
  // Approvals from Gatecrasher wait on the owner too, with or without mail sorting.
  const n = needs.length + gate.waiting
  if ((!on && !gate.waiting) || (!n && !answers.length && !news)) return null
  return (
    <button
      type="button"
      className="desk-bar-button"
      data-on="true"
      title={
        n
          ? `${n} ${n === 1 ? "thing needs" : "things need"} you. Open the assistant.`
          : news
            ? "Bas has something to tell you. Open it."
            : "An answer came back to the assistant. Open it."
      }
      onClick={() => {
        seenNews()
        desk.togglePanel("agent", true)
      }}
    >
      <BellRing aria-hidden="true" size={14} strokeWidth={1.6} />
      <span>{n ? `${n} for you` : news ? "from Bas" : "answer"}</span>
    </button>
  )
}
