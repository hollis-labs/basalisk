import { Inbox } from "lucide-react"
import { useEffect, useState } from "react"
import { INBOX_CHANGED, inbox } from "../../api/inbox"
import { useDesk } from "../../desktop/context"

/** The bar's inbox button: opens the Inbox and shows how many items are unread. */
export function InboxBell() {
  const desk = useDesk()
  // null: this Basalisk has no inbox (no gateway), so no button.
  const [unread, setUnread] = useState<number | null>(null)

  useEffect(() => {
    let live = true
    const load = (fresh = false) =>
      inbox
        .unread(fresh)
        .then((r) => live && setUnread(r.unread))
        .catch(() => undefined)
    void load()
    const timer = window.setInterval(load, 30_000)
    const onChange = () => void load(true)
    const onFocus = () => void load()
    window.addEventListener(INBOX_CHANGED, onChange)
    window.addEventListener("focus", onFocus)
    return () => {
      live = false
      window.clearInterval(timer)
      window.removeEventListener(INBOX_CHANGED, onChange)
      window.removeEventListener("focus", onFocus)
    }
  }, [])

  if (unread === null) return null
  return (
    <button
      type="button"
      className="desk-bar-button inbox-bell"
      data-unread={unread > 0}
      title={unread ? `Inbox: ${unread} unread` : "Inbox"}
      aria-label={unread ? `Inbox, ${unread} unread` : "Inbox"}
      onClick={() => desk.open("inbox")}
    >
      <Inbox aria-hidden="true" size={14} strokeWidth={1.6} />
      {unread > 0 && <span className="inbox-bell-count">{unread > 99 ? "99+" : unread}</span>}
    </button>
  )
}
