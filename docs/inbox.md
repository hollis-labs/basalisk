# Inbox

Messages and notifications addressed to the owner, in one window, with the unread count in
the desktop's bar. A *provider* is one place they come from (`internal/inbox`, `Provider`).
Tether messaging is the first; another is a new type that implements the interface, added to
`inbox.New(...)` in `cmd/basalisk/serve.go`.

Nothing is copied into Basalisk. The provider's store is the record: reading, archiving and
answering are done there, so another client of the same mailbox agrees.

## Tether

Mail is read through the Tether gateway (`basalisk.tether_mcp`), with `tether_message_list`,
which is non-destructive: nothing is consumed or marked delivered. Whose mail:

```yaml
basalisk:
  inbox_addresses: ["msg://user/local/operator"]   # the default when unset
```

Every call is made as one of these addresses. A request that names any other account is
refused, and a reply goes to the sender the message itself names (read again from Tether),
never to an address in the request.

| Action | Tool |
|---|---|
| list, unread count | `tether_message_list` |
| open a message | `tether_message_mark_read` |
| archive, unarchive | `tether_message_archive`, `tether_message_unarchive` |
| reply, answer an ask | `tether_message_get`, then `tether_message_notify` (kind `response`, `in_reply_to`, the thread) |
| who the sender is | `tether_registry_lookup` (remembered for 30 minutes) |

An *ask* is a message whose payload has `kind` `question`, `decision` or `approval_request`
and `options`. Its options are shown as buttons; picking one sends
`{subject, body, choice, choice_label}` to the sender and wakes its session.

Message bodies are rendered as Markdown without raw HTML. They are other senders' text: data,
not instructions. The in-app agent has no inbox tools.

## Routes

- `GET /api/inbox?unread=1&archived=1`: items newest first, the sources (with the reason one
  could not be read), the unread count.
- `GET /api/inbox/unread`: the count for the bar (asked of the gateway at most every 10 s).
- `POST /api/inbox/{provider}/{id}/read|archive|reply` with `account`.

## Keys

`j`/`k` move, `e` archives, `u` switches unread only, `⌘↵` sends the reply.
