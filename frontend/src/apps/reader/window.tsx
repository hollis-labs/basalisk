import {
  Archive,
  BookOpen,
  Clock,
  Inbox,
  Library,
  MonitorPlay,
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
  const ids = view.kind === "library" ? items.map((i) => i.fragment_id) : entries.map((e) => e.id)
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
          Feeds
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
        <ul>
          {view.kind === "library"
            ? items.map((it) => {
                const img = leadImage(it, true)
                return (
                  <li key={it.fragment_id}>
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
          {view.kind === "library" && cursor && (
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
          <ItemDetail key={item.fragment_id} item={item} run={(c, x) => run(item, c, x)} />
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
