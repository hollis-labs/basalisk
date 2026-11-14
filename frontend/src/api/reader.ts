import { request } from "./account"

/** A file FE holds for a media asset (fe.reader.item.v1). */
export interface ReaderVariant {
  asset_variant_id: string
  kind: string
  acquisition_state: "pending" | "available" | "reference_only" | "failed"
  mime_type?: string
  width?: number
  height?: number
  duration_seconds?: number
  source_url?: string
}

export interface ReaderMedia {
  attachment: {
    attachment_id: string
    fragment_revision_id: string
    role: string
    position: number
    caption?: string
  }
  media_asset_id: string
  kind: string
  alt_text?: string
  variants: ReaderVariant[]
}

export type ReaderPosition =
  | { kind: "none" }
  | { kind: "article"; progress: number }
  | { kind: "video" | "audio"; elapsed_seconds: number; duration_seconds?: number }
  | { kind: "gallery"; attachment_id: string; index: number }
  | { kind: "document"; page: number; progress?: number }

/** One item of the library, as Fragments Engine projects it. */
export interface ReaderItem {
  fragment_id: string
  fragment_revision_id: string
  revision: number
  renderer: string
  source: { canonical_url?: string; submitted_url?: string; provider?: string }
  display: {
    title: { value: string }
    summary: { value: string }
    description?: { value: string }
    byline?: { value: string }
    published_at?: string
    featured_media_id?: string
    image_count?: number
  }
  article: { preview_markdown: string; full_content_available: boolean }
  media: ReaderMedia[]
  playback?: {
    kind: string
    provider?: string
    provider_item_id?: string
    asset_variant_id?: string
    start_seconds?: number
  }
  tags: { combined: string[] }
  annotations: {
    annotation_id?: string
    text?: string
    selector?: { exact?: string }
    created_at?: string
  }[]
  reading_state: {
    state: "unread" | "in_progress" | "read"
    position: ReaderPosition
    revision: number
    later?: boolean
    starred?: boolean
    archived?: boolean
    last_opened_at?: string
  }
  actions: { command: string }[]
  /** The owner's collections this item is in (absent when in none). */
  collections?: { collection_id: string; name: string; kind: string }[]
}

/** A named, ordered set of library items: a playlist, a reading list, research for one piece. */
export interface ReaderCollection {
  collection_id: string
  name: string
  description: string
  kind: string
  item_count: number
  revision: number
}

export interface ReaderPage {
  items: ReaderItem[]
  next_cursor?: string | null
}

export interface Feed {
  id: string
  url: string
  title: string
  site?: string
  folder?: string
  fetched_at: string
  error?: string
  unread: number
}

export interface FeedEntry {
  id: string
  feed_id: string
  feed?: string
  url: string
  title: string
  author?: string
  published: string
  text: string
  image?: string
  read: boolean
  starred: boolean
  saved: boolean
}

/** What a library listing is narrowed by (FE's reader list parameters). */
export type ReaderFilter = Partial<
  Record<
    "scope" | "renderer" | "state" | "later" | "starred" | "archived" | "q" | "tag" | "cursor",
    string
  >
>

const nonce = () =>
  globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`

/** Commands that carry the reading state's revision; the rest carry the item's. */
const STATE_COMMANDS = new Set([
  "set_reading_progress",
  "mark_read",
  "mark_unread",
  "set_later",
  "set_archived",
  "set_starred",
])

export const reader = {
  list: (f: ReaderFilter) => {
    const q = new URLSearchParams()
    for (const [k, v] of Object.entries(f)) if (v) q.set(k, v)
    return request<ReaderPage>(`reader/items?${q.toString()}`)
  },
  item: (id: string) => request<ReaderItem>(`reader/items/${id}`),
  content: (it: ReaderItem) =>
    request<{ markdown: string }>(
      `reader/items/${it.fragment_id}/content?revision_id=${it.fragment_revision_id}`,
    ),
  /** Run one reader command and answer the item as it is afterwards. */
  async command(it: ReaderItem, command: string, extra: Record<string, unknown> = {}) {
    const id = nonce()
    await request<unknown>(`reader/items/${it.fragment_id}/commands`, "POST", {
      schema_version: "fe.reader.command.v1",
      command,
      command_id: `basalisk-${id}`,
      idempotency_key: `basalisk-${id}`,
      expected_revision: STATE_COMMANDS.has(command) ? it.reading_state.revision : it.revision,
      ...extra,
    })
    return reader.item(it.fragment_id)
  },
  collections: () =>
    request<{ collections: ReaderCollection[] }>("reader/collections").then(
      (r) => r.collections ?? [],
    ),
  createCollection: (name: string, kind = "collection") =>
    request<ReaderCollection>("reader/collections", "POST", { name, kind }),
  updateCollection: (
    c: ReaderCollection,
    patch: { name?: string; description?: string; kind?: string },
  ) =>
    request<ReaderCollection>(`reader/collections/${c.collection_id}`, "PUT", {
      expected_revision: c.revision,
      ...patch,
    }),
  deleteCollection: (c: ReaderCollection) =>
    request<void>(
      `reader/collections/${c.collection_id}?expected_revision=${c.revision}`,
      "DELETE",
    ),
  collectionItems: (id: string, cursor = "") =>
    request<{ collection: ReaderCollection; items: ReaderItem[]; next_cursor?: string }>(
      `reader/collections/${id}/items${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ""}`,
    ),
  /** Add an item at the end. Accepted from any revision at or behind the current one. */
  addToCollection: (c: ReaderCollection, fragmentId: string) =>
    request<ReaderCollection>(`reader/collections/${c.collection_id}/items`, "POST", {
      expected_revision: c.revision,
      fragment_id: fragmentId,
    }),
  removeFromCollection: (c: ReaderCollection, fragmentId: string) =>
    request<ReaderCollection>(
      `reader/collections/${c.collection_id}/items/${fragmentId}?expected_revision=${c.revision}`,
      "DELETE",
    ),
  /** Replace the order: every member's id, each once. */
  orderCollection: (c: ReaderCollection, fragmentIds: string[]) =>
    request<ReaderCollection>(`reader/collections/${c.collection_id}/order`, "PUT", {
      expected_revision: c.revision,
      fragment_ids: fragmentIds,
    }),
  feeds: () => request<{ feeds: Feed[]; refreshing: boolean }>("reader/feeds"),
  addFeed: (url: string, folder = "") => request<Feed>("reader/feeds", "POST", { url, folder }),
  updateFeed: (id: string, patch: { title?: string; folder?: string }) =>
    request<void>(`reader/feeds/${id}`, "PUT", patch),
  removeFeed: (id: string) => request<void>(`reader/feeds/${id}`, "DELETE"),
  refreshFeeds: (feed = "") => request<void>("reader/feeds/refresh", "POST", { feed }),
  readAll: (feed = "") => request<void>("reader/feeds/read", "POST", { feed }),
  entries: (f: {
    feed?: string
    unread?: boolean
    starred?: boolean
    q?: string
    before?: string
  }) => {
    const q = new URLSearchParams()
    if (f.feed) q.set("feed", f.feed)
    if (f.unread) q.set("unread", "1")
    if (f.starred) q.set("starred", "1")
    if (f.q) q.set("q", f.q)
    if (f.before) q.set("before", f.before)
    return request<{ entries: FeedEntry[] }>(`reader/entries?${q.toString()}`)
  },
  entry: (id: string) => request<FeedEntry>(`reader/entries/${id}`),
  markEntry: (id: string, patch: { read?: boolean; starred?: boolean; saved?: boolean }) =>
    request<void>(`reader/entries/${id}`, "PUT", patch),
}

/** The address of an FE-held file, through this Basalisk. */
export const mediaURL = (it: ReaderItem, m: ReaderMedia, v: ReaderVariant) =>
  `/api/reader/media/${v.asset_variant_id}?fragment_id=${it.fragment_id}&revision_id=${m.attachment.fragment_revision_id}`

/** The best held image of a media asset (a poster for a video), or undefined. */
export function imageOf(
  it: ReaderItem,
  m: ReaderMedia | undefined,
  small = false,
): string | undefined {
  if (!m) return undefined
  const held = m.variants.filter(
    (v) => v.acquisition_state === "available" && (v.mime_type ?? "").startsWith("image/"),
  )
  if (!held.length) return undefined
  const order = small
    ? ["thumbnail", "poster", "preview", "original"]
    : ["original", "preview", "poster", "thumbnail"]
  held.sort((a, b) => order.indexOf(a.kind) - order.indexOf(b.kind))
  return mediaURL(it, m, held[0])
}

/** The item's lead picture: its featured image, else a video's poster. */
export function leadImage(it: ReaderItem, small = false): string | undefined {
  const featured = it.media.find((m) => m.media_asset_id === it.display.featured_media_id)
  return (
    imageOf(it, featured, small) ??
    imageOf(
      it,
      it.media.find((m) => m.kind === "video"),
      small,
    )
  )
}

export function hostOf(url?: string): string {
  try {
    return url ? new URL(url).host.replace(/^www\./, "") : ""
  } catch {
    return ""
  }
}
