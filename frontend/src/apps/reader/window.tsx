import {
  Archive,
  BookOpen,
  Clock,
  FileUp,
  FolderPlus,
  Inbox,
  Library,
  ListVideo,
  MonitorPlay,
  Pencil,
  Plus,
  RefreshCw,
  Rss,
  Search,
  Star,
  Trash2,
} from "lucide-react"
import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  type Feed,
  type FeedEntry,
  hostOf,
  leadImage,
  type ReaderCollection,
  type ReaderFilter,
  type ReaderItem,
  reader,
} from "../../api/reader"
import { useDesk } from "../../desktop/context"
import { EntryDetail, ItemDetail } from "./detail"
import "./reader.css"

/** A place in the sidebar: a library view, or a feed view. */
type View =
  | { kind: "library"; id: string; label: string; icon: ReactNode; filter: ReaderFilter }
  | { kind: "feeds"; id: string; label: string; feed?: string; unread?: boolean; starred?: boolean }
  | { kind: "collection"; id: string; label: string; collection: string }

const ICON = { size: 14, strokeWidth: 1.6, "aria-hidden": true } as const

const LIBRARY: View[] = [
  {
    kind: "library",
    id: "unread",
    label: "Unread",
    icon: <BookOpen {...ICON} />,
    filter: { scope: "library", state: "unread" },
  },
  {
    kind: "library",
    id: "inbox",
    label: "Inbox",
    icon: <Inbox {...ICON} />,
    filter: { scope: "inbox" },
  },
  {
    kind: "library",
    id: "later",
    label: "Later",
    icon: <Clock {...ICON} />,
    filter: { scope: "library", later: "true" },
  },
  {
    kind: "library",
    id: "starred",
    label: "Starred",
    icon: <Star {...ICON} />,
    filter: { scope: "library", starred: "true" },
  },
  {
    kind: "library",
    id: "videos",
    label: "Videos",
    icon: <MonitorPlay {...ICON} />,
    filter: { scope: "library", renderer: "video" },
  },
  {
    kind: "library",
    id: "all",
    label: "Everything",
    icon: <Library {...ICON} />,
    filter: { scope: "library" },
  },
  {
    kind: "library",
    id: "archived",
    label: "Archived",
    icon: <Archive {...ICON} />,
    filter: { scope: "all", archived: "true" },
  },
]

function ago(iso?: string): string {
  if (!iso) return ""
  const t = new Date(iso).getTime()
  if (!t) return ""
  const m = Math.round((Date.now() - t) / 60000)
  if (m < 60) return `${Math.max(m, 1)}m`
  if (m < 60 * 24) return `${Math.round(m / 60)}h`
  if (m < 60 * 24 * 30) return `${Math.round(m / 1440)}d`
  return new Date(iso).toLocaleDateString([], { month: "short", day: "numeric", year: "2-digit" })
}

/**
 * The Reader window: where to look on the left, the list in the middle, the open item on the
 * right. j/k move, Enter opens the source, s stars, l is later, e archives, m marks read.
 */
export function ReaderWindow({ start }: { start: string }) {
  const desk = useDesk()
  const [view, setView] = useState<View>(() => LIBRARY.find((v) => v.id === start) ?? LIBRARY[0])
  const [query, setQuery] = useState("")
  const [typed, setTyped] = useState("")
  const [items, setItems] = useState<ReaderItem[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [entries, setEntries] = useState<FeedEntry[]>([])
  const [moreEntries, setMoreEntries] = useState(false)
  const [feeds, setFeeds] = useState<Feed[]>([])
  const [open, setOpen] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [adding, setAdding] = useState<string | null>(null)
  const [collections, setCollections] = useState<ReaderCollection[]>([])
  const [naming, setNaming] = useState<string | null>(null)
  const [dragging, setDragging] = useState("")
  const [renaming, setRenaming] = useState<string | null>(null)
  const [note, setNote] = useState("")
  const opml = useRef<HTMLInputElement>(null)
  const seq = useRef(0)

  const fail = useCallback(
    (e: unknown) => setError(e instanceof Error ? e.message : "That did not work."),
    [],
  )

  const loadFeeds = useCallback(
    () =>
      reader
        .feeds()
        .then((r) => setFeeds(r.feeds))
        .catch(() => setFeeds([])),
    [],
  )
  // Collections are absent on an FE that does not have them yet: the section is then empty.
  const loadCollections = useCallback(
    () =>
      reader
        .collections()
        .then((list) => {
          setCollections(list)
          return list
        })
        .catch(() => [] as ReaderCollection[]),
    [],
  )
  useEffect(() => {
    void loadCollections()
  }, [loadCollections])
  useEffect(() => {
    void loadFeeds()
    const timer = window.setInterval(() => void loadFeeds(), 120_000)
    return () => window.clearInterval(timer)
  }, [loadFeeds])

  const load = useCallback(
    async (more = false, from?: { cursor?: string | null; before?: string }) => {
      const mine = ++seq.current
      setBusy(true)
      try {
        if (view.kind === "library") {
          const page = await reader.list({
            ...view.filter,
            q: query,
            cursor: more ? (from?.cursor ?? "") : "",
          })
          if (mine !== seq.current) return
          setItems((old) => (more ? [...old, ...page.items] : page.items))
          setCursor(page.next_cursor ?? null)
        } else if (view.kind === "collection") {
          const page = await reader.collectionItems(
            view.collection,
            more ? (from?.cursor ?? "") : "",
          )
          if (mine !== seq.current) return
          setItems((old) => (more ? [...old, ...page.items] : page.items))
          setCursor(page.next_cursor ?? null)
          setCollections((list) =>
            list.map((c) =>
              c.collection_id === page.collection.collection_id ? page.collection : c,
            ),
          )
        } else {
          const r = await reader.entries({
            feed: view.feed,
            unread: view.unread,
            starred: view.starred,
            q: query,
            before: more ? from?.before : undefined,
          })
          if (mine !== seq.current) return
          setEntries((old) => (more ? [...old, ...r.entries] : r.entries))
          setMoreEntries(r.entries.length >= 60)
        }
        setError("")
      } catch (e) {
        if (mine === seq.current) fail(e)
      } finally {
        if (mine === seq.current) setBusy(false)
      }
    },
    [view, query, fail],
  )
  useEffect(() => {
    setOpen("")
    void load()
  }, [load])

  useEffect(() => {
    const refresh = () => {
      void load()
      void loadFeeds()
      void loadCollections()
    }
    window.addEventListener("basalisk:reader-changed", refresh)
    return () => window.removeEventListener("basalisk:reader-changed", refresh)
  }, [load, loadFeeds, loadCollections])

  // The search box narrows the view shortly after typing stops.
  useEffect(() => {
    const timer = window.setTimeout(() => setQuery(typed.trim()), 350)
    return () => window.clearTimeout(timer)
  }, [typed])

  const item = useMemo(() => items.find((i) => i.fragment_id === open), [items, open])
  const entry = useMemo(() => entries.find((e) => e.id === open), [entries, open])
  const patchItem = useCallback(
    (next: ReaderItem) =>
      setItems((list) => list.map((i) => (i.fragment_id === next.fragment_id ? next : i))),
    [],
  )
  const patchEntry = useCallback(
    (id: string, change: Partial<FeedEntry>) => {
      setEntries((list) => list.map((e) => (e.id === id ? { ...e, ...change } : e)))
      void reader.markEntry(id, change).then(loadFeeds).catch(fail)
    },
    [loadFeeds, fail],
  )

  const show = (id: string) => {
    setOpen(id)
    const e = entries.find((x) => x.id === id)
    if (view.kind === "feeds" && e && !e.read) patchEntry(id, { read: true })
  }
  const ids = view.kind === "feeds" ? entries.map((e) => e.id) : items.map((i) => i.fragment_id)
  const step = (delta: number) => {
    if (!ids.length) return
    const at = ids.indexOf(open)
    const next = ids[Math.min(ids.length - 1, Math.max(0, at + delta))]
    if (next) show(next)
  }

  const run = async (it: ReaderItem, command: string, extra?: Record<string, unknown>) => {
    try {
      patchItem(await reader.command(it, command, extra))
    } catch (e) {
      fail(e)
      // A stale revision: read the item as it is now.
      void reader
        .item(it.fragment_id)
        .then(patchItem)
        .catch(() => undefined)
    }
  }

  // A collection change: do it with the collection as it is now, once more if it moved on.
  const withCollection = async (
    id: string,
    change: (c: ReaderCollection) => Promise<ReaderCollection | void>,
  ) => {
    const find = (list: ReaderCollection[]) => list.find((c) => c.collection_id === id)
    // One just made is not in this render's list yet: read the list again.
    let c = find(collections) ?? find(await loadCollections())
    if (!c) return
    try {
      await change(c)
    } catch {
      c = find(await loadCollections())
      if (!c) return
      await change(c).catch(fail)
    }
    await loadCollections()
  }
  const setMembership = async (it: ReaderItem, id: string, member: boolean) => {
    await withCollection(id, (c) =>
      member
        ? reader.addToCollection(c, it.fragment_id)
        : reader.removeFromCollection(c, it.fragment_id),
    )
    if (view.kind === "collection" && view.collection === id && !member) {
      setItems((list) => list.filter((i) => i.fragment_id !== it.fragment_id))
      setOpen("")
    } else {
      void reader
        .item(it.fragment_id)
        .then(patchItem)
        .catch(() => undefined)
    }
  }
  const newCollection = async (name: string, then?: (c: ReaderCollection) => void) => {
    if (!name.trim()) return
    try {
      const c = await reader.createCollection(name.trim())
      setNaming(null)
      const list = await loadCollections()
      then?.(list.find((x) => x.collection_id === c.collection_id) ?? c)
    } catch (e) {
      fail(e)
    }
  }
  const renameCollection = async (id: string, name: string) => {
    setRenaming(null)
    if (!name.trim()) return
    await withCollection(id, (c) =>
      c.name === name.trim()
        ? Promise.resolve()
        : reader.updateCollection(c, { name: name.trim() }),
    )
    setView((v) =>
      v.kind === "collection" && v.collection === id ? { ...v, label: name.trim() } : v,
    )
  }
  // An OPML file (the list another reader exports): follow every feed in it, in its folders.
  const importOPML = async (file: File) => {
    try {
      const doc = new DOMParser().parseFromString(await file.text(), "text/xml")
      const found = [...doc.querySelectorAll("outline[xmlUrl]")].map((o) => ({
        url: o.getAttribute("xmlUrl") ?? "",
        folder:
          o.parentElement?.tagName === "outline"
            ? (o.parentElement.getAttribute("title") ?? o.parentElement.getAttribute("text") ?? "")
            : "",
      }))
      if (!found.length) return setError("That file lists no feeds (it should be an OPML export).")
      const have = new Set(feeds.map((f) => f.url))
      let added = 0
      let failed = 0
      for (const [i, f] of found.entries()) {
        setNote(`Following feed ${i + 1} of ${found.length}…`)
        if (have.has(f.url)) continue
        await reader.addFeed(f.url, f.folder).then(
          () => added++,
          () => failed++,
        )
      }
      setNote(`${added} feeds followed${failed ? `; ${failed} could not be reached` : ""}.`)
      await loadFeeds()
    } catch (e) {
      fail(e)
    }
  }
  const dropCollection = async (c: ReaderCollection) => {
    const yes = await desk.confirm({
      title: `Delete the collection “${c.name}”?`,
      message: "Its items stay in your library; only the collection goes.",
      confirmLabel: "Delete",
      danger: true,
    })
    if (!yes) return
    await withCollection(c.collection_id, (x) => reader.deleteCollection(x))
    setView(LIBRARY[0])
  }
  // Drop a card on another: it takes that place in the collection's order.
  const reorder = async (onto: string) => {
    const moved = dragging
    setDragging("")
    if (view.kind !== "collection" || !moved || moved === onto || cursor) return
    const order = items.map((i) => i.fragment_id).filter((i) => i !== moved)
    order.splice(order.indexOf(onto), 0, moved)
    setItems((list) => order.map((i) => list.find((x) => x.fragment_id === i)).filter((x) => !!x))
    await withCollection(view.collection, (c) => reader.orderCollection(c, order))
  }

  const addFeed = async () => {
    if (!adding?.trim()) return
    setBusy(true)
    try {
      const f = await reader.addFeed(adding)
      setAdding(null)
      await loadFeeds()
      setView({ kind: "feeds", id: f.id, label: f.title, feed: f.id })
    } catch (e) {
      fail(e)
    } finally {
      setBusy(false)
    }
  }
  const removeFeed = async (f: Feed) => {
    const yes = await desk.confirm({
      title: `Stop following ${f.title}?`,
      message: "Its entries are forgotten. Anything you saved to the library stays there.",
      confirmLabel: "Stop following",
      danger: true,
    })
    if (!yes) return
    await reader.removeFeed(f.id).catch(fail)
    await loadFeeds()
    setView(LIBRARY[0])
  }

  const onKey = (e: React.KeyboardEvent<HTMLDivElement>) => {
    const t = e.target as HTMLElement
    if (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || e.metaKey || e.ctrlKey || e.altKey)
      return
    if (e.key === "j") step(1)
    else if (e.key === "k") step(-1)
    else if (item && e.key === "s")
      void run(item, "set_starred", { value: !item.reading_state.starred })
    else if (item && e.key === "l")
      void run(item, "set_later", { value: !item.reading_state.later })
    else if (item && e.key === "e")
      void run(item, "set_archived", { value: !item.reading_state.archived })
    else if (item && e.key === "m")
      void run(item, item.reading_state.state === "read" ? "mark_unread" : "mark_read")
    else if (entry && e.key === "s") patchEntry(entry.id, { starred: !entry.starred })
    else if (entry && e.key === "m") patchEntry(entry.id, { read: !entry.read })
    else return
    e.preventDefault()
  }

  const feedUnread = feeds.reduce((n, f) => n + f.unread, 0)
  const pick = (v: View) => {
    setView(v)
    setTyped("")
    setQuery("")
  }
  const folders = [...new Set(feeds.map((f) => f.folder ?? ""))]

  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: the window's own keys (j, k, s, l, e, m)
    <div className="reader" onKeyDown={onKey}>
      <nav className="reader-side">
        <h3>Library</h3>
        {LIBRARY.map((v) => (
          <button key={v.id} type="button" data-on={view.id === v.id} onClick={() => pick(v)}>
            {v.kind === "library" && v.icon}
            <span>{v.label}</span>
          </button>
        ))}
        <h3>
          Collections
          <button
            type="button"
            className="reader-mini"
            title="New collection"
            aria-label="New collection"
            onClick={() => setNaming(naming === null ? "" : null)}
          >
            <FolderPlus {...ICON} />
          </button>
        </h3>
        {naming !== null && (
          <form
            className="reader-add"
            onSubmit={(e) => {
              e.preventDefault()
              void newCollection(naming, (c) =>
                pick({
                  kind: "collection",
                  id: c.collection_id,
                  label: c.name,
                  collection: c.collection_id,
                }),
              )
            }}
          >
            <input
              // biome-ignore lint/a11y/noAutofocus: the field was just asked for
              autoFocus
              value={naming}
              placeholder="Name: a playlist, a reading list…"
              onChange={(e) => setNaming(e.target.value)}
              onKeyDown={(e) => e.key === "Escape" && setNaming(null)}
            />
          </form>
        )}
        {collections.map((c) => (
          <button
            key={c.collection_id}
            type="button"
            data-on={view.id === c.collection_id}
            title={c.description || c.name}
            onClick={() =>
              pick({
                kind: "collection",
                id: c.collection_id,
                label: c.name,
                collection: c.collection_id,
              })
            }
          >
            <ListVideo {...ICON} />
            <span>{c.name}</span>
            {c.item_count > 0 && <em>{c.item_count}</em>}
          </button>
        ))}
        {!collections.length && naming === null && (
          <p className="reader-hint">None yet. + starts one.</p>
        )}
        <h3>
          Feeds
          <input
            ref={opml}
            type="file"
            accept=".opml,.xml,text/xml,text/x-opml"
            hidden
            onChange={(e) => {
              const f = e.target.files?.[0]
              e.target.value = ""
              if (f) void importOPML(f)
            }}
          />
          <button
            type="button"
            className="reader-mini reader-mini-push"
            title="Import feeds from an OPML file (another reader's export)"
            aria-label="Import feeds from an OPML file"
            onClick={() => opml.current?.click()}
          >
            <FileUp {...ICON} />
          </button>
          <button
            type="button"
            className="reader-mini"
            title="Follow a feed"
            aria-label="Follow a feed"
            onClick={() => setAdding(adding === null ? "" : null)}
          >
            <Plus {...ICON} />
          </button>
        </h3>
        {adding !== null && (
          <form
            className="reader-add"
            onSubmit={(e) => {
              e.preventDefault()
              void addFeed()
            }}
          >
            <input
              // biome-ignore lint/a11y/noAutofocus: the field was just asked for
              autoFocus
              value={adding}
              placeholder="Feed or site address"
              spellCheck={false}
              onChange={(e) => setAdding(e.target.value)}
              onKeyDown={(e) => e.key === "Escape" && setAdding(null)}
            />
          </form>
        )}
        <button
          type="button"
          data-on={view.id === "feeds-unread"}
          onClick={() =>
            pick({ kind: "feeds", id: "feeds-unread", label: "New in feeds", unread: true })
          }
        >
          <Rss {...ICON} />
          <span>New</span>
          {feedUnread > 0 && <em>{feedUnread}</em>}
        </button>
        <button
          type="button"
          data-on={view.id === "feeds-starred"}
          onClick={() =>
            pick({ kind: "feeds", id: "feeds-starred", label: "Starred in feeds", starred: true })
          }
        >
          <Star {...ICON} />
          <span>Starred</span>
        </button>
        {folders.map((folder) => (
          <div key={folder || "-"} className="reader-folder">
            {folder && <h4>{folder}</h4>}
            {feeds
              .filter((f) => (f.folder ?? "") === folder)
              .map((f) => (
                <button
                  key={f.id}
                  type="button"
                  data-on={view.id === f.id}
                  title={f.error ? `${f.url}: ${f.error}` : f.url}
                  data-error={!!f.error}
                  onClick={() => pick({ kind: "feeds", id: f.id, label: f.title, feed: f.id })}
                >
                  <span>{f.title}</span>
                  {f.unread > 0 && <em>{f.unread}</em>}
                </button>
              ))}
          </div>
        ))}
        {!feeds.length && adding === null && (
          <p className="reader-hint">No feeds yet. + follows one.</p>
        )}
      </nav>

      <section className="reader-list">
        <header>
          <label className="reader-search">
            <Search {...ICON} />
            <input
              value={typed}
              placeholder={`Search ${view.label.toLowerCase()}`}
              aria-label="Search"
              onChange={(e) => setTyped(e.target.value)}
            />
          </label>
          {view.kind === "feeds" && (
            <button
              type="button"
              className="reader-mini"
              title="Mark all read"
              onClick={() =>
                void reader
                  .readAll(view.feed ?? "")
                  .then(() => Promise.all([load(), loadFeeds()]))
                  .catch(fail)
              }
            >
              ✓
            </button>
          )}
          {view.kind === "collection" && (
            <button
              type="button"
              className="reader-mini"
              title="Rename this collection"
              aria-label="Rename this collection"
              onClick={() => setRenaming(view.label)}
            >
              <Pencil {...ICON} />
            </button>
          )}
          {view.kind === "collection" && (
            <button
              type="button"
              className="reader-mini"
              title="Delete this collection (its items stay in the library)"
              aria-label="Delete this collection"
              onClick={() => {
                const c = collections.find((x) => x.collection_id === view.collection)
                if (c) void dropCollection(c)
              }}
            >
              <Trash2 {...ICON} />
            </button>
          )}
          {view.kind === "feeds" && view.feed && (
            <button
              type="button"
              className="reader-mini"
              title="Stop following this feed"
              aria-label="Stop following this feed"
              onClick={() => {
                const f = feeds.find((x) => x.id === view.feed)
                if (f) void removeFeed(f)
              }}
            >
              <Trash2 {...ICON} />
            </button>
          )}
          <button
            type="button"
            className="reader-mini"
            title="Check for new"
            aria-label="Refresh"
            data-busy={busy}
            onClick={() =>
              void (
                view.kind === "feeds"
                  ? reader
                      .refreshFeeds(view.feed ?? "")
                      .then(() => Promise.all([load(), loadFeeds()]))
                  : load()
              ).catch(fail)
            }
          >
            <RefreshCw {...ICON} />
          </button>
        </header>
        {error && (
          <p className="reader-error" role="alert">
            {error}
          </p>
        )}
        {note && !error && (
          <p className="reader-error reader-note" role="status">
            {note}
          </p>
        )}
        {renaming !== null && view.kind === "collection" && (
          <form
            className="reader-add"
            onSubmit={(e) => {
              e.preventDefault()
              void renameCollection(view.collection, renaming)
            }}
          >
            <input
              // biome-ignore lint/a11y/noAutofocus: the field was just asked for
              autoFocus
              value={renaming}
              aria-label="The collection's name"
              onChange={(e) => setRenaming(e.target.value)}
              onKeyDown={(e) => e.key === "Escape" && setRenaming(null)}
              onBlur={() => setRenaming(null)}
            />
          </form>
        )}
        <ul>
          {view.kind !== "feeds"
            ? items.map((it) => {
                const img = leadImage(it, true)
                return (
                  <li
                    key={it.fragment_id}
                    draggable={view.kind === "collection" && !cursor}
                    onDragStart={() => setDragging(it.fragment_id)}
                    onDragOver={(e) => view.kind === "collection" && dragging && e.preventDefault()}
                    onDrop={() => void reorder(it.fragment_id)}
                    data-dragging={dragging === it.fragment_id}
                  >
                    <button
                      type="button"
                      className="reader-card"
                      data-open={open === it.fragment_id}
                      data-read={it.reading_state.state === "read"}
                      onClick={() => show(it.fragment_id)}
                    >
                      <span className="reader-card-text">
                        <strong>{it.display.title.value || "Untitled"}</strong>
                        <span className="reader-card-sum">{it.display.summary.value}</span>
                        <span className="reader-card-meta">
                          {it.renderer !== "article" && <i>{it.renderer}</i>}
                          {hostOf(it.source.canonical_url)}
                          {it.reading_state.starred && " · ★"}
                          {it.reading_state.later && " · later"}
                          {it.reading_state.state === "in_progress" && " · started"}
                        </span>
                      </span>
                      {img && <img src={img} alt="" loading="lazy" />}
                    </button>
                  </li>
                )
              })
            : entries.map((e) => (
                <li key={e.id}>
                  <button
                    type="button"
                    className="reader-card"
                    data-open={open === e.id}
                    data-read={e.read}
                    onClick={() => show(e.id)}
                  >
                    <span className="reader-card-text">
                      <strong>{e.title || "Untitled"}</strong>
                      <span className="reader-card-sum">{e.text}</span>
                      <span className="reader-card-meta">
                        {e.feed} · {ago(e.published)}
                        {e.starred && " · ★"}
                        {e.saved && " · saved"}
                      </span>
                    </span>
                  </button>
                </li>
              ))}
          {!ids.length && !busy && <li className="reader-none">Nothing here.</li>}
          {view.kind !== "feeds" && cursor && (
            <li>
              <button
                type="button"
                className="reader-more"
                onClick={() => void load(true, { cursor })}
              >
                More
              </button>
            </li>
          )}
          {view.kind === "feeds" && moreEntries && entries.length > 0 && (
            <li>
              <button
                type="button"
                className="reader-more"
                onClick={() => void load(true, { before: entries[entries.length - 1].published })}
              >
                More
              </button>
            </li>
          )}
        </ul>
      </section>

      <section className="reader-pane">
        {item ? (
          <ItemDetail
            key={item.fragment_id}
            item={item}
            run={(c, x) => run(item, c, x)}
            collections={collections}
            setMembership={(id, member) => setMembership(item, id, member)}
            newCollection={(name) =>
              newCollection(name, (c) => void setMembership(item, c.collection_id, true))
            }
          />
        ) : entry ? (
          <EntryDetail key={entry.id} entry={entry} patch={(c) => patchEntry(entry.id, c)} />
        ) : (
          <p className="reader-pick">
            {ids.length ? "Pick something to read." : "Nothing to read here."}
            <span>j / k move · m read · s star · l later · e archive</span>
          </p>
        )}
      </section>
    </div>
  )
}
