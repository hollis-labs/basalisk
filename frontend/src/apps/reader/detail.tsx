import {
  Archive,
  BookOpenCheck,
  Clock,
  ExternalLink,
  Highlighter,
  Save,
  Star,
  X,
} from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"
import { openLink } from "../../api/browser"
import { frag } from "../../api/frag"
import {
  type FeedEntry,
  hostOf,
  imageOf,
  leadImage,
  mediaURL,
  type ReaderItem,
  reader,
} from "../../api/reader"
import { Markdown } from "../../cite/ui"
import { captureIdFor, newDraft } from "../frag/draft"

const ICON = { size: 14, strokeWidth: 1.6, "aria-hidden": true } as const
type Run = (command: string, extra?: Record<string, unknown>) => Promise<void>

/**
 * The text with its pictures pointed at the copies Fragments Engine holds. A picture FE does
 * not hold is left out (its caption stays), so reading never fetches from the source site.
 */
function withHeldImages(md: string, it: ReaderItem): string {
  const held = new Map<string, string>()
  for (const m of it.media) {
    const src = imageOf(it, m)
    if (!src) continue
    for (const v of m.variants) if (v.source_url) held.set(v.source_url, src)
  }
  const swap = (alt: string, url: string) => {
    const src = held.get(url) ?? held.get(url.split("?")[0])
    return src ? `![${alt}](${src})` : alt ? `*${alt}*` : ""
  }
  return md
    .replace(/!\[([^\]]*)\]\(\s*<?([^)\s>]+)>?[^)]*\)/g, (_m, alt: string, url: string) =>
      swap(alt, url),
    )
    .replace(/!\[([^\]]*)\]\[[^\]]*\]/g, (_m, alt: string) => (alt ? `*${alt}*` : ""))
}

/** A long highlight (a whole page selected at capture) is shown by its start. */
const clip = (s: string) => (s.length > 600 ? `${s.slice(0, 600).trimEnd()}…` : s)

function Player({ item, run }: { item: ReaderItem; run: Run }) {
  const pos = item.reading_state.position
  const startAt =
    pos.kind === "video" ? Math.floor(pos.elapsed_seconds) : (item.playback?.start_seconds ?? 0)
  const sent = useRef(startAt)
  const runRef = useRef(run)
  runRef.current = run
  const p = item.playback
  // A video Fragments Engine holds plays here and remembers where it was left.
  const own = item.media
    .filter((m) => m.kind === "video")
    .flatMap((m) =>
      m.variants
        .filter(
          (v) => v.acquisition_state === "available" && (v.mime_type ?? "").startsWith("video/"),
        )
        .map((v) => mediaURL(item, m, v)),
    )[0]
  if (own) {
    return (
      // biome-ignore lint/a11y/useMediaCaption: captions are not part of the reader item yet
      <video
        className="reader-video"
        src={`${own}#t=${startAt}`}
        poster={leadImage(item)}
        controls
        onTimeUpdate={(e) => {
          const v = e.currentTarget
          if (Math.abs(v.currentTime - sent.current) < 15) return
          sent.current = v.currentTime
          void runRef.current("set_reading_progress", {
            position: {
              kind: "video",
              elapsed_seconds: Math.floor(v.currentTime),
              ...(v.duration > 0 ? { duration_seconds: Math.floor(v.duration) } : {}),
            },
          })
        }}
        onEnded={() => void runRef.current("mark_read")}
      />
    )
  }
  if (
    p?.provider === "youtube" &&
    p.provider_item_id &&
    /^[A-Za-z0-9_-]{11}$/.test(p.provider_item_id)
  ) {
    return (
      <iframe
        className="reader-video"
        title={item.display.title.value}
        src={`https://www.youtube-nocookie.com/embed/${p.provider_item_id}?start=${startAt}&rel=0`}
        allow="fullscreen; picture-in-picture; encrypted-media"
        allowFullScreen
        referrerPolicy="strict-origin-when-cross-origin"
        sandbox="allow-scripts allow-same-origin allow-presentation"
      />
    )
  }
  const poster = leadImage(item)
  return (
    <div className="reader-video reader-video-none">
      {poster && <img src={poster} alt="" />}
      <span>This video is not held here. Open the source to watch it.</span>
    </div>
  )
}

/** One library item: its actions, its tags, the player or the text, its highlights. */
export function ItemDetail({ item, run }: { item: ReaderItem; run: Run }) {
  const [full, setFull] = useState<string | null>(null)
  const [tag, setTag] = useState("")
  const [pick, setPick] = useState<{ quote: string; x: number; y: number } | null>(null)
  const scroller = useRef<HTMLDivElement>(null)
  const saved = useRef(
    item.reading_state.position.kind === "article" ? item.reading_state.position.progress : 0,
  )
  const runRef = useRef(run)
  runRef.current = run
  const itemRef = useRef(item)
  itemRef.current = item
  const can = (c: string) => item.actions.some((a) => a.command === c)
  const state = item.reading_state
  const source = item.source.canonical_url ?? item.source.submitted_url

  // The whole text, when FE has it; the preview until then.
  // biome-ignore lint/correctness/useExhaustiveDependencies: once per item revision
  useEffect(() => {
    let live = true
    if (!item.article.full_content_available) return
    void reader
      .content(item)
      .then((r) => live && setFull(r.markdown))
      .catch(() => undefined)
    return () => {
      live = false
    }
  }, [item.fragment_revision_id])

  // biome-ignore lint/correctness/useExhaustiveDependencies: the media of one revision does not change
  const text = useMemo(
    () => withHeldImages(full ?? itemRef.current.article.preview_markdown ?? "", itemRef.current),
    [full, item.fragment_revision_id],
  )

  // Go back to where reading stopped, once the text is in.
  // biome-ignore lint/correctness/useExhaustiveDependencies: when the full text arrives
  useEffect(() => {
    const el = scroller.current
    if (!el || item.renderer === "video" || !saved.current) return
    requestAnimationFrame(() => {
      el.scrollTop = saved.current * Math.max(0, el.scrollHeight - el.clientHeight)
    })
  }, [full])

  // Remember how far the text has been read: a little after scrolling stops.
  useEffect(() => {
    const el = scroller.current
    if (!el || item.renderer === "video") return
    let timer = 0
    const onScroll = () => {
      window.clearTimeout(timer)
      timer = window.setTimeout(() => {
        const room = el.scrollHeight - el.clientHeight
        if (room < 200) return
        const progress = Math.min(1, Math.max(0, el.scrollTop / room))
        const it = itemRef.current
        if (Math.abs(progress - saved.current) < 0.04 || it.reading_state.state === "read") return
        saved.current = progress
        void runRef.current(
          progress > 0.97 ? "mark_read" : "set_reading_progress",
          progress > 0.97
            ? {}
            : { position: { kind: "article", progress: Number(progress.toFixed(3)) } },
        )
      }, 2500)
    }
    el.addEventListener("scroll", onScroll, { passive: true })
    return () => {
      window.clearTimeout(timer)
      el.removeEventListener("scroll", onScroll)
    }
  }, [item.renderer])

  const onSelect = () => {
    const sel = window.getSelection()
    const el = scroller.current
    if (
      !sel ||
      sel.isCollapsed ||
      !el ||
      !el.contains(sel.anchorNode) ||
      !can("append_capture_note")
    )
      return setPick(null)
    const quote = sel.toString().trim()
    if (quote.length < 3 || quote.length > 4000) return setPick(null)
    const r = sel.getRangeAt(0).getBoundingClientRect()
    const box = el.getBoundingClientRect()
    setPick({ quote, x: r.left - box.left + r.width / 2, y: r.top - box.top + el.scrollTop - 34 })
  }
  const highlight = async () => {
    if (!pick) return
    const id = globalThis.crypto?.randomUUID?.() ?? String(Date.now())
    await run("append_capture_note", {
      annotation_id: `basalisk-${id}`,
      text: pick.quote,
      selector: { exact: pick.quote },
    })
    setPick(null)
    window.getSelection()?.removeAllRanges()
  }

  const images = item.media.filter((m) => m.kind === "image")
  return (
    <article className="reader-detail">
      <header>
        <h1>{item.display.title.value || "Untitled"}</h1>
        <p className="reader-by">
          {[item.display.byline?.value, hostOf(source), item.display.published_at?.slice(0, 10)]
            .filter(Boolean)
            .join(" · ")}
        </p>
        <div className="reader-actions">
          {source && (
            <button
              type="button"
              title="Open the source in the Browser"
              onClick={() => openLink(source)}
            >
              <ExternalLink {...ICON} />
              <span>Source</span>
            </button>
          )}
          <button
            type="button"
            data-on={state.state === "read"}
            title="Read or unread (m)"
            onClick={() => void run(state.state === "read" ? "mark_unread" : "mark_read")}
          >
            <BookOpenCheck {...ICON} />
            <span>{state.state === "read" ? "Read" : "Mark read"}</span>
          </button>
          <button
            type="button"
            data-on={!!state.later}
            title="Later (l)"
            onClick={() => void run("set_later", { value: !state.later })}
          >
            <Clock {...ICON} />
            <span>Later</span>
          </button>
          <button
            type="button"
            data-on={!!state.starred}
            title="Star (s)"
            onClick={() => void run("set_starred", { value: !state.starred })}
          >
            <Star {...ICON} />
            <span>Star</span>
          </button>
          <button
            type="button"
            data-on={!!state.archived}
            title="Archive (e)"
            onClick={() => void run("set_archived", { value: !state.archived })}
          >
            <Archive {...ICON} />
            <span>{state.archived ? "Archived" : "Archive"}</span>
          </button>
        </div>
        <div className="reader-tags">
          {item.tags.combined.map((t) => (
            <span key={t} className="reader-tag">
              {t}
              {can("remove_tag") && (
                <button
                  type="button"
                  aria-label={`Remove tag ${t}`}
                  onClick={() => void run("remove_tag", { tag: t })}
                >
                  <X size={10} strokeWidth={2} aria-hidden="true" />
                </button>
              )}
            </span>
          ))}
          {can("add_tag") && (
            <form
              onSubmit={(e) => {
                e.preventDefault()
                const t = tag.trim()
                if (!t) return
                setTag("")
                void run("add_tag", { tag: t })
              }}
            >
              <input
                value={tag}
                placeholder="+ tag"
                aria-label="Add a tag"
                onChange={(e) => setTag(e.target.value)}
              />
            </form>
          )}
        </div>
      </header>
      {/* biome-ignore lint/a11y/noStaticElementInteractions: a selection in the text offers Highlight */}
      <div className="reader-scroll" ref={scroller} onMouseUp={onSelect}>
        {item.renderer === "video" && <Player item={item} run={run} />}
        {(item.renderer === "image" || item.renderer === "gallery") && (
          <div className="reader-gallery">
            {images.map((m) => {
              const src = imageOf(item, m)
              return src ? (
                <figure key={m.attachment.attachment_id}>
                  <img src={src} alt={m.alt_text ?? ""} loading="lazy" />
                  {m.attachment.caption && <figcaption>{m.attachment.caption}</figcaption>}
                </figure>
              ) : null
            })}
          </div>
        )}
        {item.renderer === "article" &&
          leadImage(item) &&
          !text.includes(leadImage(item) ?? "\0") && (
            <img className="reader-lead" src={leadImage(item)} alt="" />
          )}
        <div className="reader-text">
          <Markdown source={text || "*Nothing to read in this item.*"} />
        </div>
        {item.annotations.length > 0 && (
          <section className="reader-notes">
            <h2>Highlights and notes</h2>
            {item.annotations.map((a, i) => (
              <blockquote key={a.annotation_id ?? i}>
                {a.selector?.exact && <p className="reader-quote">{clip(a.selector.exact)}</p>}
                {a.text && a.text !== a.selector?.exact && <p>{clip(a.text)}</p>}
              </blockquote>
            ))}
          </section>
        )}
        {pick && (
          <button
            type="button"
            className="reader-highlight"
            style={{ left: pick.x, top: Math.max(0, pick.y) }}
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => void highlight()}
          >
            <Highlighter {...ICON} />
            <span>Highlight</span>
          </button>
        )}
      </div>
    </article>
  )
}

/** One feed entry: its text, and the ways on (open it, save it to the library). */
export function EntryDetail({
  entry,
  patch,
}: {
  entry: FeedEntry
  patch: (c: Partial<FeedEntry>) => void
}) {
  const [whole, setWhole] = useState(entry)
  const [note, setNote] = useState("")
  useEffect(() => {
    let live = true
    void reader
      .entry(entry.id)
      .then((e) => live && setWhole(e))
      .catch(() => undefined)
    return () => {
      live = false
    }
  }, [entry.id])

  // Saving sends the link, with what the feed said of it, to Fragments Engine as a capture.
  const save = async () => {
    const content = `[${entry.title || entry.url}](${entry.url})\n\n${whole.text}`.trim()
    const d = captureIdFor({ ...newDraft(), content, tags: "" })
    try {
      await frag.capture({
        captureId: d.captureId,
        content,
        tags: ["feed", ...(entry.feed ? [entry.feed] : [])],
        draftAt: new Date().toISOString(),
        submittedAt: new Date().toISOString(),
        context: { feed: entry.feed, url: entry.url },
      })
      patch({ saved: true })
      setNote("Queued for Fragments Engine. Open it and clip the page to keep the whole article.")
    } catch (e) {
      setNote(e instanceof Error ? e.message : "It could not be saved.")
    }
  }

  return (
    <article className="reader-detail">
      <header>
        <h1>{entry.title || "Untitled"}</h1>
        <p className="reader-by">
          {[entry.author, entry.feed, new Date(entry.published).toLocaleDateString()]
            .filter(Boolean)
            .join(" · ")}
        </p>
        <div className="reader-actions">
          {entry.url && (
            <button
              type="button"
              title="Open in the Browser (clip it there to keep the page)"
              onClick={() => openLink(entry.url)}
            >
              <ExternalLink {...ICON} />
              <span>Open</span>
            </button>
          )}
          <button
            type="button"
            data-on={entry.saved}
            title="Save the link to the library"
            onClick={() => void save()}
          >
            <Save {...ICON} />
            <span>{entry.saved ? "Saved" : "Save"}</span>
          </button>
          <button
            type="button"
            data-on={entry.starred}
            title="Star (s)"
            onClick={() => patch({ starred: !entry.starred })}
          >
            <Star {...ICON} />
            <span>Star</span>
          </button>
          <button
            type="button"
            title="Read or unread (m)"
            onClick={() => patch({ read: !entry.read })}
          >
            <BookOpenCheck {...ICON} />
            <span>{entry.read ? "Mark unread" : "Mark read"}</span>
          </button>
        </div>
        {note && <p className="reader-by">{note}</p>}
      </header>
      <div className="reader-scroll">
        <div className="reader-text reader-plain">
          {whole.text
            ? whole.text.split(/\n{2,}/).map((p, i) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: paragraphs of fixed text
                <p key={i}>{p}</p>
              ))
            : "This feed gave no text for the entry. Open it to read."}
        </div>
      </div>
    </article>
  )
}
