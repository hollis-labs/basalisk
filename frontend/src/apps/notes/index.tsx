import { Check, Code2, Copy, Eye, FileText, RotateCw } from "lucide-react"
import { useCallback, useEffect, useState } from "react"
import { type CiteRecord, kindLabel, loadRecord, type RefIdentity, refsIn } from "../../cite"
import { CiteText, Markdown } from "../../cite/ui"
import { useDesk } from "../../desktop/context"
import {
  type FileRef,
  registerApp,
  registerFileHandler,
  registerProvider,
  type WindowProps,
} from "../../desktop/registry"

/**
 * Notes: a viewer for markdown and text. It shows a file (through the file API) or a cited
 * record (a Torque task, a Tesseract item) the same way: a header, and the text with its
 * references live. Editing comes later; this is the reading half.
 */

const MARKDOWN = /\.(md|markdown|mdx)$/i
const TEXT = /\.(md|markdown|mdx|txt|text|log|rst|org)$/i

type Source = { file: FileRef } | { cite: RefIdentity } | null

interface Doc {
  title: string
  /** Where it came from, for the header: a path or an id. */
  origin: string
  text: string
  markdown: boolean
  record?: CiteRecord
}

function sourceOf(args: Record<string, unknown>): Source {
  if (args.file) return { file: args.file as FileRef }
  if (args.cite) return { cite: args.cite as RefIdentity }
  return null
}

async function load(source: NonNullable<Source>): Promise<Doc> {
  if ("cite" in source) {
    const record = await loadRecord(source.cite)
    return {
      title: record.title,
      origin: source.cite.canonical,
      text: record.body || record.description || "",
      markdown: true,
      record,
    }
  }
  const { root, path } = source.file
  const response = await fetch(
    `/api/files/content?root=${encodeURIComponent(root)}&path=${encodeURIComponent(path)}`,
    { credentials: "same-origin" },
  )
  if (!response.ok) {
    let message = `The file could not be read (${response.status}).`
    try {
      message = ((await response.json()) as { message?: string }).message ?? message
    } catch {
      // Not a JSON error body.
    }
    throw new Error(message)
  }
  const name = path.split("/").pop() || path
  return {
    title: name,
    origin: `${root}:${path}`,
    text: await response.text(),
    markdown: MARKDOWN.test(name),
  }
}

function NotesWindow({ winId, args }: WindowProps) {
  const desk = useDesk()
  const source = sourceOf(args)
  const key = source ? JSON.stringify(source) : ""
  const [doc, setDoc] = useState<Doc | null>(null)
  const [error, setError] = useState("")
  const [raw, setRaw] = useState(false)
  const [copied, setCopied] = useState(false)
  const [nonce, setNonce] = useState(0)

  // biome-ignore lint/correctness/useExhaustiveDependencies: key is the source, serialized; nonce reloads
  useEffect(() => {
    if (!source) return
    let live = true
    setError("")
    load(source)
      .then((d) => {
        if (!live) return
        setDoc(d)
        desk.setTitle(winId, d.title)
      })
      .catch((e) => live && setError(e instanceof Error ? e.message : "Could not be opened."))
    return () => {
      live = false
    }
  }, [key, nonce])

  const copy = useCallback(() => {
    if (!doc) return
    void navigator.clipboard?.writeText(doc.text).then(() => {
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1200)
    })
  }, [doc])

  if (!source)
    return (
      <div className="notes-empty">
        <FileText aria-hidden="true" size={22} strokeWidth={1.4} />
        <p>Open a markdown or text file from Files, or a reference from anywhere.</p>
        <p className="notes-hint">
          Type an id such as CW-20261113-0017 into the command palette to open it here.
        </p>
      </div>
    )
  if (error)
    return (
      <div className="notes-empty">
        <p>{error}</p>
        <button type="button" className="desk-button" onClick={() => setNonce((n) => n + 1)}>
          Try again
        </button>
      </div>
    )
  if (!doc) return <div className="notes-empty">Opening…</div>

  const record = doc.record
  return (
    <div className="notes">
      <header className="notes-bar">
        <span className="notes-origin" title={doc.origin}>
          {"cite" in source ? `${kindLabel(source.cite.kind)} · ` : ""}
          {doc.origin}
        </span>
        {doc.markdown && (
          <button
            type="button"
            title={raw ? "Show rendered" : "Show source"}
            onClick={() => setRaw((r) => !r)}
          >
            {raw ? (
              <Eye aria-hidden="true" size={14} strokeWidth={1.6} />
            ) : (
              <Code2 aria-hidden="true" size={14} strokeWidth={1.6} />
            )}
          </button>
        )}
        <button type="button" title="Copy the text" onClick={copy}>
          {copied ? (
            <Check aria-hidden="true" size={14} strokeWidth={1.6} />
          ) : (
            <Copy aria-hidden="true" size={14} strokeWidth={1.6} />
          )}
        </button>
        <button type="button" title="Reload" onClick={() => setNonce((n) => n + 1)}>
          <RotateCw aria-hidden="true" size={14} strokeWidth={1.6} />
        </button>
      </header>
      <div className="notes-scroll">
        <article className="notes-page">
          {record && (
            <div className="notes-record">
              <h1>{record.title}</h1>
              <div className="notes-meta">
                {record.status && (
                  <span className="cite-status" data-tone={record.status.tone}>
                    {record.status.label}
                  </span>
                )}
                {record.fields?.map((f) => (
                  <span key={f.label} className="notes-field">
                    <span>{f.label}</span>
                    {f.mono ? <CiteText text={f.value} /> : f.value}
                  </span>
                ))}
              </div>
            </div>
          )}
          {doc.markdown && !raw ? (
            <Markdown source={doc.text} />
          ) : (
            <pre className="notes-text">
              <CiteText text={doc.text} />
            </pre>
          )}
          {!doc.text.trim() && <p className="notes-hint">This one has no text.</p>}
        </article>
      </div>
    </div>
  )
}

registerApp({
  id: "notes",
  title: "Notes",
  icon: FileText,
  menu: true,
  size: [860, 720],
  bare: true,
  multi: true,
  render: (props) => <NotesWindow {...props} />,
})

registerFileHandler({ app: "notes", match: (name) => TEXT.test(name) })

// A reference typed (or pasted) into the palette opens in a Notes window.
registerProvider({
  id: "cite",
  search: (query) =>
    refsIn(query, true)
      .slice(0, 6)
      .map((r) => ({
        id: `cite:${r.kind}:${r.canonical}`,
        title: `Open ${r.canonical}`,
        subtitle: kindLabel(r.kind),
        section: "References",
        icon: FileText,
        run: (desk) => {
          desk.open("notes", {
            args: { cite: { kind: r.kind, canonical: r.canonical } },
            key: `${r.kind}:${r.canonical}`,
            title: r.canonical,
          })
        },
      })),
})
