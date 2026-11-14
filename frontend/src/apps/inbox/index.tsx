/**
 * Inbox: messages and notifications addressed to the owner (internal/inbox). Tether
 * messaging is the first provider. The bar shows how many are unread (bell.tsx).
 */
import { Inbox } from "lucide-react"
import { registerApp } from "../../desktop/registry"
import { InboxWindow } from "./window"

registerApp({
  id: "inbox",
  title: "Inbox",
  icon: Inbox,
  menu: true,
  size: [980, 640],
  bare: true,
  render: () => <InboxWindow />,
})
