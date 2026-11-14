import { request } from "./account"

/** One message or notification (internal/inbox Item). */
export interface InboxItem {
  id: string
  provider: string
  /** The owner's address it was sent to. */
  account: string
  from: string
  from_name?: string
  from_role?: string
  kind: string
  subject: string
  body: string
  at: string
  read: boolean
  archived: boolean
  thread?: string
  in_reply_to?: string
  ask?: {
    kind: "question" | "decision" | "approval_request"
    re?: string
    options?: { id: string; label: string; why?: string; recommended?: boolean }[]
    default?: string
    due?: string
  }
}

export interface InboxSource {
  id: string
  title: string
  accounts: string[]
  error?: string
}

/** Dispatched on window when read state changed, so the bar's count follows. */
export const INBOX_CHANGED = "basalisk:inbox"

const at = (it: InboxItem, what: string) =>
  `inbox/${encodeURIComponent(it.provider)}/${encodeURIComponent(it.id)}/${what}`
const changed = () => window.dispatchEvent(new Event(INBOX_CHANGED))

export const inbox = {
  list: (opts: { unread?: boolean; archived?: boolean } = {}) =>
    request<{ items: InboxItem[]; sources: InboxSource[]; unread: number }>(
      `inbox?unread=${opts.unread ? 1 : 0}&archived=${opts.archived ? 1 : 0}`,
    ),
  unread: (fresh = false) => request<{ unread: number }>(`inbox/unread${fresh ? "?fresh=1" : ""}`),
  read: (it: InboxItem) =>
    request<void>(at(it, "read"), "POST", { account: it.account }).then(changed),
  archive: (it: InboxItem, archived = true) =>
    request<void>(at(it, "archive"), "POST", { account: it.account, archived }).then(changed),
  reply: (it: InboxItem, body: string, choice = "") =>
    request<void>(at(it, "reply"), "POST", { account: it.account, body, choice }).then(changed),
}
