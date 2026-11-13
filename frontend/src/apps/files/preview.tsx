import { Download, ExternalLink, Link2Off } from "lucide-react"
import { useEffect, useState } from "react"
import { type FileEntry, type FileRoot, files } from "../../api/files"
import { CiteText, Markdown } from "../../cite/ui"
import { formatSize, formatTime, iconOf, kindOf, textual } from "./model"

/** How much of a text file the preview reads. */
const TEXT_LIMIT = 256 * 1024

type Text =
  | { state: "loading" }
  | { state: "text"; text: string; cut: boolean }
  | { state: "binary" }
  | { state: "error"; message: string }

function useText(root: string, entry: FileEntry | null, wanted: boolean): Text | null {
  const [text, setText] = useState<Text | null>(null)
  const path = entry?.path
  const mtime = entry?.mtime
  // biome-ignore lint/correctness/useExhaustiveDependencies: a changed file is read again
  useEffect(() => {
    if (!wanted || path === undefined) {
      setText(null)
      return
    }
    const ctrl = new AbortController()
    // Arrowing through a folder should not fetch every file on the way.
    const timer = window.setTimeout(() => {
      setText({ state: "loading" })
      fetch(files.contentURL(root, path), {
        credentials: "same-origin",
        headers: { Range: `bytes=0-${TEXT_LIMIT - 1}` },
        signal: ctrl.signal,
      })
        .then(async (r) => {
          if (!r.ok && r.status !== 206) throw new Error(`HTTP ${r.status}`)
          const buf = new Uint8Array(await r.arrayBuffer())
          const head = buf.subarray(0, 8192)
          if (head.includes(0)) return setText({ state: "binary" })
          const total = Number(r.headers.get("Content-Range")?.split("/")[1] ?? buf.length)
          setText({
            state: "text",
            text: new TextDecoder("utf-8", { fatal: false }).decode(buf),
            cut: total > buf.length,
          })
        })
        .catch((e: unknown) => {
          if (!ctrl.signal.aborted)
            setText({
              state: "error",
              message: e instanceof Error ? e.message : "Could not read it.",
            })
        })
    }, 90)
    return () => {
      window.clearTimeout(timer)
      ctrl.abort()
    }
  }, [root, path, mtime, wanted])
  return text
}

export function Preview({
  root,
  entry,
  count,
  onOpen,
}: {
  root: FileRoot
  entry: FileEntry | null
  /** Entries in the folder, shown when nothing is selected. */
  count: number
  onOpen: (e: FileEntry) => void
}) {
  const kind = entry ? kindOf(entry) : null
  const text = useText(root.id, entry, !!entry && !entry.broken && !!kind && textual(kind))

  if (!entry) {
    return (
      <aside className="files-preview" aria-label="Preview">
        <div className="files-preview-empty">
          {count} item{count === 1 ? "" : "s"}
        </div>
      </aside>
    )
  }
  const Icon = entry.broken ? Link2Off : iconOf(entry)
  const src = files.contentURL(root.id, entry.path)

  let body: React.ReactNode = null
  if (entry.broken) {
    body = <p className="files-preview-note">This link leads nowhere this root can reach.</p>
  } else if (kind === "image") {
    body = (
      <div className="files-preview-media">
        <img src={src} alt={entry.name} key={entry.mtime} />
      </div>
    )
  } else if (kind === "video") {
    body = (
      <div className="files-preview-media">
        {/* biome-ignore lint/a11y/useMediaCaption: a local file has no captions to offer */}
        <video src={src} controls preload="metadata" />
      </div>
    )
  } else if (kind === "audio") {
    body = (
      <div className="files-preview-media">
        {/* biome-ignore lint/a11y/useMediaCaption: a local file has no captions to offer */}
        <audio src={src} controls preload="metadata" />
      </div>
    )
  } else if (text) {
    if (text.state === "loading") body = <p className="files-preview-note">Reading…</p>
    else if (text.state === "binary") body = <p className="files-preview-note">Binary file.</p>
    else if (text.state === "error") body = <p className="files-preview-note">{text.message}</p>
    else
      body = (
        <>
          {/\.(md|markdown|mdx)$/i.test(entry.name) ? (
            <div className="files-preview-md">
              <Markdown source={text.text} />
            </div>
          ) : (
            <pre className="files-preview-text">
              <CiteText text={text.text} />
            </pre>
          )}
          {text.cut && (
            <p className="files-preview-note">Showing the first {formatSize(TEXT_LIMIT)}.</p>
          )}
        </>
      )
  }

  return (
    <aside className="files-preview" aria-label="Preview">
      <header className="files-preview-head">
        <Icon aria-hidden="true" size={18} strokeWidth={1.6} />
        <div className="files-preview-name" title={entry.name}>
          {entry.name}
        </div>
      </header>
      <dl className="files-preview-meta">
        {!entry.dir && (
          <>
            <dt>Size</dt>
            <dd>{formatSize(entry.size)}</dd>
          </>
        )}
        <dt>Modified</dt>
        <dd>{formatTime(entry.mtime)}</dd>
        <dt>Mode</dt>
        <dd>{entry.mode}</dd>
        {entry.symlink && (
          <>
            <dt>Link</dt>
            <dd>{entry.broken ? "broken" : "yes"}</dd>
          </>
        )}
      </dl>
      <div className="files-preview-actions">
        <button type="button" className="desk-button" onClick={() => onOpen(entry)}>
          <ExternalLink aria-hidden="true" size={13} strokeWidth={1.6} />
          Open
        </button>
        {!entry.dir && !entry.broken && (
          <a className="desk-button" href={files.contentURL(root.id, entry.path, true)} download>
            <Download aria-hidden="true" size={13} strokeWidth={1.6} />
            Download
          </a>
        )}
      </div>
      <div className="files-preview-body">{body}</div>
    </aside>
  )
}
