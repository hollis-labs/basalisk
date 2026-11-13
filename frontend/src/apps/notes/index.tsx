import { Check, Code2, Copy, Eye, FileText, Pencil, RotateCw, Save, Undo2 } from "lucide-react"
import { useCallback, useEffect, useRef, useState } from "react"
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
 * references live. A file can be edited in place (a record cannot): source on the left, the
 * rendered page on the right, saved with ⌘S, refused if the file changed underneath.
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
  /** For a file: where it is and the version that was read, so a save can tell if it changed. */
  file?: FileRef
  etag?: string
}

const fileURL = (f: FileRef) =>
  `/api/files/content?root=${encodeURIComponent(f.root)}&path=${encodeURIComponent(f.path)}`

// An unsaved edit is kept in local storage until it is saved or discarded, so closing the
// window (or the app) does not lose it.
const draftKey = (f: FileRef) => `basalisk.notes.draft:${f.root}:${f.path}`
interface Draft {
  text: string
  /** The version of the file the edit started from. */
  etag?: string
}
function readDraft(f: FileRef): Draft | null {
  try {
    const raw = window.localStorage.getItem(draftKey(f))
    return raw ? (JSON.parse(raw) as Draft) : null
  } catch {
    return null
  }
}
function writeDraft(f: FileRef, draft: Draft | null) {
  try {
    if (draft === null) window.localStorage.removeItem(draftKey(f))
    else window.localStorage.setItem(draftKey(f), JSON.stringify(draft))
  } catch {
    // Not kept.
  }
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
  const response = await fetch(fileURL(source.file), { credentials: "same-origin" })
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
    file: source.file,
    etag: response.headers.get("ETag") ?? undefined,
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
  // Editing (files only): the text being edited, or null when reading.
  const [draft, setDraft] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [notice, setNotice] = useState("")
  const [conflict, setConflict] = useState(false)
  const editor = useRef<HTMLTextAreaElement>(null)

  // biome-ignore lint/correctness/useExhaustiveDependencies: key is the source, serialized; nonce reloads
  useEffect(() => {
    if (!source) return
    let live = true
    setError("")
    load(source)
      .then((d) => {
        if (!live) return
        setConflict(false)
        // Pick up an edit that was never saved. It keeps the version it started from, so
        // saving it still notices a change made to the file in the meantime.
        const kept = d.file ? readDraft(d.file) : null
        if (kept !== null && kept.text !== d.text) {
          setDoc({ ...d, etag: kept.etag ?? d.etag })
          setDraft(kept.text)
          setNotice("Restored your unsaved changes.")
        } else {
          setDoc(d)
          setDraft(null)
          setNotice("")
        }
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

  const dirty = draft !== null && doc !== null && draft !== doc.text
  // Entering edit mode (by the button, or with a restored draft) puts the cursor in the editor.
  const editing = draft !== null
  useEffect(() => {
    if (editing) editor.current?.focus()
  }, [editing])
  const title = doc?.title
  useEffect(() => {
    if (title) desk.setTitle(winId, dirty ? `● ${title}` : title)
  }, [desk, winId, title, dirty])
  // Unsaved changes: the desktop asks before closing this window.
  useEffect(() => {
    desk.setDirty(winId, dirty)
  }, [desk, winId, dirty])

  const edit = (text: string) => {
    setDraft(text)
    if (doc?.file) writeDraft(doc.file, text === doc.text ? null : { text, etag: doc.etag })
  }
  const discard = () => {
    if (doc?.file) writeDraft(doc.file, null)
    setDraft(null)
    setNotice("")
    setConflict(false)
  }
  // Save: the server refuses (412) when the file changed since it was read, unless told to overwrite.
  const save = async (overwrite = false) => {
    if (!doc?.file || draft === null || saving) return
    setSaving(true)
    setNotice("")
    try {
      const headers: Record<string, string> = {
        "Content-Type": "text/plain; charset=utf-8",
        "X-Requested-With": "XMLHttpRequest",
      }
      if (doc.etag && !overwrite) headers["If-Match"] = doc.etag
      const response = await fetch(fileURL(doc.file), {
        method: "PUT",
        credentials: "same-origin",
        headers,
        body: draft,
      })
      if (response.status === 412) {
        setConflict(true)
        return
      }
      if (!response.ok) {
        let message = `The file could not be saved (${response.status}).`
        try {
          message = ((await response.json()) as { message?: string }).message ?? message
        } catch {
          // Not a JSON error body.
        }
        setNotice(message)
        return
      }
      writeDraft(doc.file, null)
      setDoc({ ...doc, text: draft, etag: response.headers.get("ETag") ?? undefined })
      setConflict(false)
      setNotice("Saved.")
      window.setTimeout(() => setNotice((n) => (n === "Saved." ? "" : n)), 1500)
    } catch {
      setNotice("The file could not be saved: the server did not answer.")
    } finally {
      setSaving(false)
    }
  }
  // ⌘S saves from anywhere in the window, not only with the cursor in the editor.
  const onWindowKey = (e: React.KeyboardEvent) => {
    if (draft !== null && e.metaKey && !e.altKey && !e.ctrlKey && e.key === "s") {
      e.preventDefault()
      void save()
    }
  }
  const onEditorKey = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Tab" && !e.metaKey && !e.ctrlKey && !e.altKey) {
      // Tab indents instead of leaving the editor.
      e.preventDefault()
      const el = e.currentTarget
      const { selectionStart: a, selectionEnd: b, value } = el
      edit(`${value.slice(0, a)}  ${value.slice(b)}`)
      window.requestAnimationFrame(() => el.setSelectionRange(a + 2, a + 2))
    }
  }

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
    // biome-ignore lint/a11y/noStaticElementInteractions: a key handler for the whole window, not a control
    <div className="notes" onKeyDown={onWindowKey}>
      <header className="notes-bar">
        <span className="notes-origin" title={doc.origin}>
          {"cite" in source ? `${kindLabel(source.cite.kind)} · ` : ""}
          {doc.origin}
        </span>
        {notice && <span className="notes-notice">{notice}</span>}
        {doc.file && draft === null && (
          <button
            type="button"
            title="Edit"
            onClick={() => {
              setDraft(doc.text)
              window.requestAnimationFrame(() => editor.current?.focus())
            }}
          >
            <Pencil aria-hidden="true" size={14} strokeWidth={1.6} />
          </button>
        )}
        {draft !== null && (
          <>
            <button
              type="button"
              title="Save (⌘S)"
              disabled={!dirty || saving}
              onClick={() => void save()}
            >
              <Save aria-hidden="true" size={14} strokeWidth={1.6} />
            </button>
            <button
              type="button"
              title={dirty ? "Discard changes" : "Stop editing"}
              onClick={discard}
            >
              <Undo2 aria-hidden="true" size={14} strokeWidth={1.6} />
            </button>
          </>
        )}
        {doc.markdown && draft === null && (
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
      {conflict && (
        <div className="notes-conflict" role="alert">
          <span>This file changed on disk after you opened it.</span>
          <button type="button" className="desk-button" onClick={() => void save(true)}>
            Save mine anyway
          </button>
          <button
            type="button"
            className="desk-button"
            onClick={() => {
              discard()
              setNonce((n) => n + 1)
            }}
          >
            Discard mine and reload
          </button>
        </div>
      )}
      {draft !== null ? (
        <div className="notes-edit" data-preview={doc.markdown}>
          <textarea
            ref={editor}
            className="notes-editor"
            value={draft}
            spellCheck={false}
            onChange={(e) => edit(e.target.value)}
            onKeyDown={onEditorKey}
            aria-label={`Edit ${doc.title}`}
          />
          {doc.markdown && (
            <div className="notes-preview">
              <article className="notes-page">
                <Markdown source={draft} />
              </article>
            </div>
          )}
        </div>
      ) : (
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
      )}
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
