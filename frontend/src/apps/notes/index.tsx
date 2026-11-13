import {
  Check,
  Code2,
  Copy,
  Eye,
  FilePlus2,
  FileText,
  ListTree,
  Pencil,
  RotateCw,
  Save,
  SaveAll,
  TextCursorInput,
  Undo2,
} from "lucide-react"
import { useCallback, useEffect, useRef, useState } from "react"
import { type CiteRecord, kindLabel, loadRecord, type RefIdentity, refsIn } from "../../cite"
import { CiteText, Markdown } from "../../cite/ui"
import { useDesk } from "../../desktop/context"
import {
  type FileRef,
  registerApp,
  registerCommands,
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

import { continueList, EDITOR_KEYS, indent, link, replace, wrap } from "./editor"
import { NotesFind } from "./find"
import { createNote, NameForm, notesFolder, renameNote, saveAs } from "./notefiles"
import { noteWindows } from "./open"
import { Outline, useRichPage } from "./outline"
import { resolveRelative, scrollToAnchor } from "./rich"
import { complete, linkName, useBacklinks, useNoteIndex } from "./wiki"
import "./agent"
import "./notes.css"

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
  const view = useRef<HTMLElement>(null)
  const preview = useRef<HTMLElement>(null)
  const [finding, setFinding] = useState(false)
  const [outline, setOutline] = useState(() => {
    try {
      return window.localStorage.getItem("basalisk.notes.outline") === "1"
    } catch {
      return false
    }
  })
  const toggleOutline = () =>
    setOutline((o) => {
      try {
        window.localStorage.setItem("basalisk.notes.outline", o ? "0" : "1")
      } catch {
        // Not kept.
      }
      return !o
    })
  // A name being asked for: a new note, a rename, a save as.
  const [form, setForm] = useState<"rename" | "save-as" | null>(null)
  const [folder, setFolder] = useState<FileRef | undefined>()
  useEffect(() => {
    if (args.new || form === "save-as")
      void notesFolder()
        .then(setFolder)
        .catch(() => undefined)
  }, [args.new, form])
  // A note just created opens for editing.
  const editOnLoad = useRef(!!args.edit)

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
          setDraft(editOnLoad.current && d.file ? d.text : null)
          setNotice("")
        }
        editOnLoad.current = false
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
  // What this window shows, for the agent's tools (apps/notes/agent.ts).
  const latest = useRef({ doc, draft })
  latest.current = { doc, draft }
  const fileKey = doc?.file ? `${doc.file.root}:${doc.file.path}` : ""
  // biome-ignore lint/correctness/useExhaustiveDependencies: fileKey is the file, serialized
  useEffect(() => {
    const f = latest.current.doc?.file
    if (!f) return
    noteWindows.set(winId, {
      file: f,
      saved: () => latest.current.doc?.text ?? "",
      shown: () => latest.current.draft ?? latest.current.doc?.text ?? "",
      dirty: () => {
        const { doc: d, draft: t } = latest.current
        return t !== null && d !== null && t !== d.text
      },
      replaced: (text, etag) => {
        writeDraft(f, null)
        setDoc((d) => (d ? { ...d, text, etag } : d))
        setDraft((t) => (t === null ? null : text))
        setConflict(false)
        setNotice("Changed by the agent.")
      },
    })
    return () => noteWindows.set(winId, null)
  }, [winId, fileKey])
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
  // ⌘F finds in the note, in the editor or the rendered page.
  const onWindowKey = (e: React.KeyboardEvent) => {
    const cmd = e.metaKey && !e.altKey && !e.ctrlKey
    if (draft !== null && cmd && !e.shiftKey && e.code === "KeyS") {
      e.preventDefault()
      void save()
    } else if (cmd && !e.shiftKey && e.code === "KeyF") {
      e.preventDefault()
      setFinding(true)
    }
  }
  // [[ in the editor offers the notes to link to.
  const wiki = useNoteIndex(doc?.file)
  const [completing, setCompleting] = useState<{ query: string; start: number; at: number } | null>(
    null,
  )
  const picks =
    completing && wiki && doc?.file ? complete(completing.query, doc.file.path, wiki) : []
  const noteCompletion = (el: HTMLTextAreaElement) => {
    const caret = el.selectionStart
    if (!wiki || el.selectionEnd !== caret) return setCompleting(null)
    const m = /\[\[([^[\]\n|#]*)$/.exec(el.value.slice(Math.max(0, caret - 120), caret))
    setCompleting((c) =>
      m ? { query: m[1], start: caret - m[1].length, at: c?.query === m[1] ? c.at : 0 } : null,
    )
  }
  const pick = (path: string) => {
    const el = editor.current
    if (!el || !completing || !wiki) return
    const name = linkName(path, wiki)
    const caret = el.selectionStart
    const closed = el.value.slice(caret, caret + 2) === "]]"
    const after = completing.start + name.length + 2
    replace(el, completing.start, caret, closed ? name : `${name}]]`, after, after)
    setCompleting(null)
  }
  // Markdown keys in the editor; each edit goes through the textarea, so ⌘Z undoes it.
  const onEditorKey = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    const el = e.currentTarget
    if (completing) {
      const n = picks.length
      let used = true
      if (e.key === "Escape") setCompleting(null)
      else if (n && e.key === "ArrowDown")
        setCompleting({ ...completing, at: (completing.at + 1) % n })
      else if (n && e.key === "ArrowUp")
        setCompleting({ ...completing, at: (completing.at - 1 + n) % n })
      else if (n && (e.key === "Enter" || e.key === "Tab") && !e.shiftKey)
        pick(picks[completing.at] ?? picks[0])
      else used = false
      if (used) {
        e.preventDefault()
        e.stopPropagation()
        return
      }
    }
    const cmd = e.metaKey && !e.altKey && !e.ctrlKey && !e.shiftKey
    let handled = true
    if (e.key === "Tab" && !e.metaKey && !e.ctrlKey && !e.altKey) indent(el, e.shiftKey)
    else if (e.key === "Enter" && !e.metaKey && !e.ctrlKey && !e.altKey && !e.shiftKey)
      handled = continueList(el)
    else if (cmd && e.code === "KeyB") wrap(el, "**")
    else if (cmd && e.code === "KeyI") wrap(el, "_")
    else if (cmd && e.code === "KeyK") link(el)
    else handled = false
    if (handled) e.preventDefault()
  }

  // A link in the page to another file opens it (a note in Notes, at its anchor); to this
  // file, it scrolls.
  const here = doc?.file
  const openLinked = useCallback(
    (file: FileRef, anchor: string) => {
      if (here && file.root === here.root && file.path === here.path) {
        const page = view.current ?? preview.current
        if (page && anchor) scrollToAnchor(page, anchor)
      } else if (TEXT.test(file.path)) desk.open("notes", { args: { file, anchor } })
      else desk.openFile(file)
    },
    [desk, here],
  )
  const rendered = !!doc?.markdown && !raw && draft === null
  const viewText = doc ? resolveRelative(doc.text, doc.file, doc.markdown ? wiki : null) : ""
  const previewText = draft !== null && doc?.markdown ? resolveRelative(draft, doc.file, wiki) : ""
  // A [[link]] to a note that does not exist yet makes it, in the notes folder, for editing.
  const createMissing = useCallback(
    async (name: string) => {
      try {
        const ref = await createNote(await notesFolder(), name)
        desk.open("notes", { args: { file: ref, edit: true } })
      } catch (e) {
        setNotice(e instanceof Error ? e.message : "The note could not be made.")
      }
    },
    [desk],
  )
  const headings = useRichPage(view, viewText, openLinked, rendered, createMissing)
  useRichPage(preview, previewText, openLinked, draft !== null && !!doc?.markdown, createMissing)
  const linkedFrom = useBacklinks(doc?.file, wiki, rendered && outline)
  // Opened at an anchor (a link from another note): scroll there once it is drawn.
  const anchor = typeof args.anchor === "string" ? args.anchor : ""
  useEffect(() => {
    if (anchor && rendered && view.current) scrollToAnchor(view.current, anchor)
  }, [anchor, rendered])

  const openFile = (file: FileRef, edit: boolean) => {
    editOnLoad.current = edit
    setForm(null)
    desk.setArgs(winId, { file, edit })
  }
  if (args.new && !source)
    return (
      <div className="notes-empty notes-new">
        <h2>New note</h2>
        {folder && (
          <NameForm
            mode="new"
            initial="Untitled.md"
            folder={folder}
            onDone={async (name, where) => openFile(await createNote(where ?? folder, name), true)}
            onCancel={() => desk.close(winId)}
          />
        )}
      </div>
    )
  if (!source)
    return (
      <div className="notes-empty">
        <button
          type="button"
          className="desk-button"
          onClick={() => desk.setArgs(winId, { new: true })}
        >
          New note
        </button>
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
    <div className="notes" onKeyDown={onWindowKey} data-keys-own="cmd+f cmd+s cmd+g shift+cmd+g">
      <header className="notes-bar">
        <span className="notes-origin" title={doc.origin}>
          {"cite" in source ? `${kindLabel(source.cite.kind)} · ` : ""}
          {doc.origin}
        </span>
        {notice && <span className="notes-notice">{notice}</span>}
        <button
          type="button"
          title="New note (⌃⌥N)"
          onClick={() => desk.open("notes", { args: { new: true } })}
        >
          <FilePlus2 aria-hidden="true" size={14} strokeWidth={1.6} />
        </button>
        {doc.file && (
          <>
            <button
              type="button"
              title="Rename"
              onClick={() => setForm(form === "rename" ? null : "rename")}
            >
              <TextCursorInput aria-hidden="true" size={14} strokeWidth={1.6} />
            </button>
            <button
              type="button"
              title="Save as…"
              onClick={() => setForm(form === "save-as" ? null : "save-as")}
            >
              <SaveAll aria-hidden="true" size={14} strokeWidth={1.6} />
            </button>
          </>
        )}
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
        {rendered && (
          <button
            type="button"
            title={outline ? "Hide the outline" : "Show the outline"}
            aria-pressed={outline}
            onClick={toggleOutline}
          >
            <ListTree aria-hidden="true" size={14} strokeWidth={1.6} />
          </button>
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
      {form && doc.file && (
        <div className="notes-form-panel">
          {form === "rename" ? (
            <NameForm
              mode="rename"
              initial={doc.title}
              onCancel={() => setForm(null)}
              onDone={async (name) => {
                const old = doc.file as FileRef
                const next = await renameNote(old, name)
                // An unsaved edit follows the note to its new name.
                const kept = readDraft(old)
                if (kept) {
                  writeDraft(next, kept)
                  writeDraft(old, null)
                }
                openFile(next, draft !== null)
              }}
            />
          ) : (
            folder && (
              <NameForm
                mode="save-as"
                initial={doc.title.replace(/(\.[^.]+)?$/, " copy$1")}
                folder={{
                  root: doc.file.root,
                  path: doc.file.path.includes("/")
                    ? doc.file.path.slice(0, doc.file.path.lastIndexOf("/"))
                    : "",
                }}
                onCancel={() => setForm(null)}
                onDone={async (name, where) => {
                  const text = draft ?? doc.text
                  const { ref } = await saveAs(text, where ?? folder, name)
                  if (doc.file) writeDraft(doc.file, null)
                  openFile(ref, draft !== null)
                }}
              />
            )
          )}
        </div>
      )}
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
      {finding && (
        <NotesFind
          key={draft !== null ? "edit" : raw ? "raw" : "view"}
          editor={draft !== null ? editor : undefined}
          view={draft !== null ? undefined : view}
          onClose={() => setFinding(false)}
        />
      )}
      {draft !== null && completing && picks.length > 0 && (
        <div className="notes-complete" role="listbox" aria-label="Link to a note">
          <span>Link to</span>
          {picks.map((p, i) => (
            <button
              key={p}
              type="button"
              role="option"
              aria-selected={i === completing.at}
              data-on={i === completing.at}
              title={p}
              onMouseDown={(e) => {
                e.preventDefault()
                pick(p)
              }}
            >
              {wiki ? linkName(p, wiki) : p}
            </button>
          ))}
          <small>↑↓ · ↵ or ⇥ to link · esc</small>
        </div>
      )}
      {draft !== null ? (
        <div className="notes-edit" data-preview={doc.markdown}>
          <textarea
            ref={editor}
            className="notes-editor"
            value={draft}
            spellCheck={false}
            onChange={(e) => {
              edit(e.target.value)
              noteCompletion(e.target)
            }}
            onSelect={(e) => noteCompletion(e.currentTarget)}
            onBlur={() => setCompleting(null)}
            onKeyDown={onEditorKey}
            data-keys-own={EDITOR_KEYS}
            aria-label={`Edit ${doc.title}`}
          />
          {doc.markdown && (
            <div className="notes-preview">
              <article className="notes-page" ref={preview}>
                <Markdown source={previewText} />
              </article>
            </div>
          )}
        </div>
      ) : (
        <div className="notes-body">
          {rendered && outline && (
            <Outline
              headings={headings}
              page={view}
              backlinks={linkedFrom}
              root={wiki?.root}
              onOpen={(f) => openLinked(f, "")}
            />
          )}
          {/* biome-ignore lint/a11y/noNoninteractiveTabindex: the page scrolls and takes ⌘F */}
          <div className="notes-scroll" tabIndex={0}>
            <article className="notes-page" ref={view}>
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
                <Markdown source={viewText} />
              ) : (
                <pre className="notes-text">
                  <CiteText text={doc.text} />
                </pre>
              )}
              {!doc.text.trim() && <p className="notes-hint">This one has no text.</p>}
            </article>
          </div>
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

registerCommands([
  {
    id: "notes.new",
    title: "Notes: new note",
    section: "Notes",
    icon: FilePlus2,
    keywords: "create write markdown",
    keys: "ctrl+alt+n",
    run: (desk) => desk.open("notes", { args: { new: true } }),
  },
])
