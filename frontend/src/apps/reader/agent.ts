/** Reader uses the desktop API, including FE's revision and command checks. */
import { type AgentTool, registerAgentTools } from "../../agent/tools"
import { type ReaderFilter, reader } from "../../api/reader"

const str = (description: string) => ({ type: "string", description })
const bool = { type: "boolean" }
const strings = { type: "array", items: { type: "string" } }
const id = str("ID returned by the corresponding Reader read tool")
const rev = { type: "integer", minimum: 0 }
const object = (properties: Record<string, unknown>, required: string[] = []) => ({
  type: "object",
  properties,
  required,
  additionalProperties: false,
})
const text = (i: Record<string, unknown>, key: string) => {
  if (typeof i[key] !== "string" || !i[key]) throw new Error(`${key} is required.`)
  return i[key] as string
}
const fields = (i: Record<string, unknown>, keys: string[]) =>
  Object.fromEntries(keys.filter((k) => i[k] !== undefined).map((k) => [k, i[k]]))
const tool = (
  name: string,
  level: AgentTool["level"],
  description: string,
  properties: Record<string, unknown>,
  required: string[],
  run: AgentTool["run"],
): AgentTool => ({
  name,
  level,
  description,
  input_schema: object(properties, required),
  run: async (i, desk, ctx) => {
    const result = await run(i, desk, ctx)
    if (level !== "read") window.dispatchEvent(new Event("basalisk:reader-changed"))
    return result
  },
  describe: (i) =>
    `${description.split(".")[0]}: ${String(i.id ?? i.name ?? i.url ?? i.feed ?? "Reader")}`,
})
async function collection(i: Record<string, unknown>) {
  const c = (await reader.collections()).find((c) => c.collection_id === text(i, "id"))
  if (!c) throw new Error("No such collection.")
  if (c.revision !== i.revision)
    throw new Error("Collection changed; read it again before editing.")
  return c
}
const filters = ["scope", "renderer", "state", "later", "starred", "archived", "q", "tag", "cursor"]
registerAgentTools([
  tool(
    "reader_list",
    "read",
    "List saved library items with filters and a continuation cursor.",
    Object.fromEntries(filters.map((k) => [k, str(k)])),
    [],
    (i) => reader.list(fields(i, filters) as ReaderFilter),
  ),
  tool(
    "reader_get",
    "read",
    "Read a library item, its state, available commands, annotations and full text when held.",
    { id },
    ["id"],
    async (i) => {
      const item = await reader.item(text(i, "id"))
      return {
        item,
        content: item.article.full_content_available
          ? (await reader.content(item)).markdown
          : item.article.preview_markdown,
      }
    },
  ),
  tool("reader_collections", "read", "List collections and their revisions.", {}, [], () =>
    reader.collections(),
  ),
  tool(
    "reader_collection_get",
    "read",
    "Read a collection's items; follow next_cursor to read more.",
    { id, cursor: str("Continuation cursor") },
    ["id"],
    (i) => reader.collectionItems(text(i, "id"), i.cursor as string | undefined),
  ),
  tool(
    "reader_collection_create",
    "write",
    "Create a named collection or playlist.",
    { name: str("Name"), kind: str("Kind; collection by default") },
    ["name"],
    (i, _d, { signal }) => {
      signal.throwIfAborted()
      return reader.createCollection(text(i, "name"), i.kind as string | undefined)
    },
  ),
  tool(
    "reader_collection_update",
    "write",
    "Edit collection metadata using the revision just read.",
    { id, revision: rev, name: str("Name"), description: str("Description"), kind: str("Kind") },
    ["id", "revision"],
    async (i, _d, { signal }) => {
      const c = await collection(i)
      signal.throwIfAborted()
      return reader.updateCollection(c, fields(i, ["name", "description", "kind"]))
    },
  ),
  tool(
    "reader_collection_delete",
    "confirm",
    "Delete a collection, retaining its library items. Always asks.",
    { id, revision: rev },
    ["id", "revision"],
    async (i, _d, { signal }) => {
      const c = await collection(i)
      signal.throwIfAborted()
      return reader.deleteCollection(c)
    },
  ),
  ...(["add", "remove"] as const).map((action) =>
    tool(
      `reader_collection_${action}`,
      action === "remove" ? "confirm" : "write",
      `${action === "add" ? "Add an item to" : "Remove an item from"} a collection. The library item is retained.`,
      { id, revision: rev, fragment_id: id },
      ["id", "revision", "fragment_id"],
      async (i, _d, { signal }) => {
        const c = await collection(i)
        signal.throwIfAborted()
        return action === "add"
          ? reader.addToCollection(c, text(i, "fragment_id"))
          : reader.removeFromCollection(c, text(i, "fragment_id"))
      },
    ),
  ),
  tool(
    "reader_collection_order",
    "write",
    "Reorder a collection using every member's ID exactly once.",
    { id, revision: rev, fragment_ids: strings },
    ["id", "revision", "fragment_ids"],
    async (i, _d, { signal }) => {
      const c = await collection(i)
      if (!Array.isArray(i.fragment_ids) || !i.fragment_ids.every((x) => typeof x === "string"))
        throw new Error("fragment_ids must be an array of IDs.")
      signal.throwIfAborted()
      return reader.orderCollection(c, i.fragment_ids)
    },
  ),
  tool(
    "reader_feeds",
    "read",
    "List RSS and Atom subscriptions, folders and refresh status.",
    {},
    [],
    () => reader.feeds(),
  ),
  tool(
    "reader_feed_add",
    "write",
    "Subscribe to an RSS or Atom feed.",
    { url: str("Feed URL"), folder: str("Folder") },
    ["url"],
    (i, _d, { signal }) => {
      signal.throwIfAborted()
      return reader.addFeed(text(i, "url"), i.folder as string | undefined)
    },
  ),
  tool(
    "reader_feed_update",
    "write",
    "Rename a feed or move it to a folder.",
    { id, title: str("Title"), folder: str("Folder") },
    ["id"],
    (i, _d, { signal }) => {
      signal.throwIfAborted()
      return reader.updateFeed(text(i, "id"), fields(i, ["title", "folder"]))
    },
  ),
  tool(
    "reader_feed_delete",
    "confirm",
    "Unsubscribe and delete this feed and its entries. Always asks.",
    { id },
    ["id"],
    (i, _d, { signal }) => {
      signal.throwIfAborted()
      return reader.removeFeed(text(i, "id"))
    },
  ),
  tool(
    "reader_feeds_refresh",
    "write",
    "Fetch new feed entries; omit feed to refresh all subscriptions.",
    { feed: id },
    [],
    (i, _d, { signal }) => {
      signal.throwIfAborted()
      return reader.refreshFeeds(i.feed as string | undefined)
    },
  ),
  tool(
    "reader_entries",
    "read",
    "List feed entries, optionally filtered; use before for older entries.",
    {
      feed: id,
      unread: bool,
      starred: bool,
      q: str("Search"),
      before: str("Pagination timestamp"),
    },
    [],
    (i) => reader.entries(fields(i, ["feed", "unread", "starred", "q", "before"])),
  ),
  tool("reader_entry_get", "read", "Read a complete feed entry.", { id }, ["id"], (i) =>
    reader.entry(text(i, "id")),
  ),
  tool(
    "reader_entry_update",
    "write",
    "Set a feed entry's read or starred state.",
    { id, read: bool, starred: bool },
    ["id"],
    (i, _d, { signal }) => {
      signal.throwIfAborted()
      return reader.markEntry(text(i, "id"), fields(i, ["read", "starred"]))
    },
  ),
  tool(
    "reader_feed_read_all",
    "write",
    "Mark all entries read in one feed, or all feeds when omitted.",
    { feed: id },
    [],
    (i, _d, { signal }) => {
      signal.throwIfAborted()
      return reader.readAll(i.feed as string | undefined)
    },
  ),
])

const stateCommands = ["mark_read", "mark_unread", "set_later", "set_starred", "set_archived"]
const commands = [...stateCommands, "add_tag", "append_capture_note"]
registerAgentTools([
  tool(
    "reader_item_update",
    "write",
    "Change reading state, add a tag, or append an annotation. Requires the item's current revision (reading_state.revision for state commands); only commands advertised by reader_get are accepted.",
    {
      id,
      revision: rev,
      command: { type: "string", enum: commands },
      value: bool,
      tag: str("Tag"),
      text: str("Annotation text"),
      quote: str("Exact passage for a highlight"),
    },
    ["id", "revision", "command"],
    async (i, _d, { signal }) => {
      const command = text(i, "command")
      if (!commands.includes(command)) throw new Error("Unsupported Reader command.")
      const item = await reader.item(text(i, "id"))
      if (!item.actions.some((a) => a.command === command))
        throw new Error("This item does not support that command.")
      if (
        (stateCommands.includes(command) ? item.reading_state.revision : item.revision) !==
        i.revision
      )
        throw new Error("Item changed; read it again before editing.")
      const extra: Record<string, unknown> = {}
      if (command.startsWith("set_")) {
        if (typeof i.value !== "boolean") throw new Error("value must be true or false.")
        extra.value = i.value
      }
      if (command.endsWith("_tag")) extra.tag = text(i, "tag")
      if (command === "append_capture_note") {
        extra.annotation_id = `basalisk-${crypto.randomUUID()}`
        extra.text = text(i, "text")
        if (i.quote !== undefined) {
          const quote = text(i, "quote")
          const content = item.article.full_content_available
            ? (await reader.content(item)).markdown
            : item.article.preview_markdown
          if (!content.includes(quote))
            throw new Error("The exact quote is not in the item's text.")
          extra.selector = { exact: quote }
        }
      }
      signal.throwIfAborted()
      return reader.command(item, command, extra)
    },
  ),
])

registerAgentTools([
  tool(
    "reader_tag_remove",
    "confirm",
    "Remove a tag from a saved item. Always asks.",
    { id, revision: rev, tag: str("Tag to remove") },
    ["id", "revision", "tag"],
    async (i, _d, { signal }) => {
      const item = await reader.item(text(i, "id"))
      if (item.revision !== i.revision)
        throw new Error("Item changed; read it again before editing.")
      if (!item.actions.some((a) => a.command === "remove_tag"))
        throw new Error("This item does not support removing tags.")
      signal.throwIfAborted()
      return reader.command(item, "remove_tag", { tag: text(i, "tag") })
    },
  ),
])
