/**
 * Inbox: messages and notifications addressed to the owner (internal/inbox). Tether
 * messaging is the first provider. The bar shows how many are unread (bell.tsx). The
 * compose box (compose.tsx) is the desktop's way to send a message from anywhere
 * (desktop/message.ts): a clicked address, a forwarded item, what a window is showing.
 */
import { Forward, Inbox, MailPlus } from "lucide-react"
import {
  composeMessage,
  draftFromWindow,
  MESSAGE_EVENT,
  MESSAGE_OVERLAY,
  type MessageDraft,
} from "../../desktop/message"
import {
  registerApp,
  registerCommands,
  registerOverlay,
  registerStartup,
} from "../../desktop/registry"
import { Compose, setIncoming } from "./compose"
import { InboxWindow } from "./window"
import "./inbox.css"

registerApp({
  id: "inbox",
  title: "Inbox",
  icon: Inbox,
  menu: true,
  size: [980, 640],
  bare: true,
  render: () => <InboxWindow />,
})

registerOverlay({ id: MESSAGE_OVERLAY, render: ({ close }) => <Compose close={close} /> })

// composeMessage() from anywhere opens the box with what it was given.
registerStartup((desk) => {
  window.addEventListener(MESSAGE_EVENT, (e) => {
    setIncoming((e as CustomEvent<MessageDraft>).detail ?? {})
    if (desk.overlayOpen(MESSAGE_OVERLAY)) desk.toggleOverlay(MESSAGE_OVERLAY, false)
    desk.toggleOverlay(MESSAGE_OVERLAY, true)
  })
})

registerCommands([
  {
    id: "message.new",
    title: "Message: new message",
    section: "Messages",
    icon: MailPlus,
    keywords: "mail send write compose tether inbox",
    keys: "alt+shift+m",
    run: () => composeMessage(),
  },
  {
    id: "message.share",
    title: "Message: send what this window shows",
    section: "Messages",
    icon: Forward,
    keywords: "forward share selection page file send mail",
    when: (desk) => !!desk.wmState().focus,
    run: async (desk) => composeMessage(await draftFromWindow(desk)),
  },
])
