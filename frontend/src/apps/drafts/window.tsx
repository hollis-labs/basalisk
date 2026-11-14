import { FilePlus2, GitCompare, History, Pencil, Save, ScanText } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  COLLAB_CHANGED,
  type CollabDoc,
  type CollabMark,
  type CollabRevision,
  collab,
  isConflict,
} from "../../api/collab"
import { Markdown } from "../../cite/ui"
import { collabOpen } from "../../collab/agent"
import { diffLines, diffStat, locate } from "../../collab/diff"
import { MarkComposer, MarksPanel } from "../../collab/marks"
import { useDesk } from "../../desktop/context"
import type { WindowProps } from "../../desktop/registry"
import "../../collab/collab.css"

const ICON = { size: 14, strokeWidth: 1.6, "aria-hidden": true } as const
const LANGS = [
  "markdown",
  "text",
  "go",
  "typescript",
  "javascript",
  "python",
  "shell",
  "sql",
  "css",
  "html",
  "json",
  "yaml",
]
type Mode = "review" | "edit" | "diff" | "preview"

interface Picked {
  quote: string
  prefix: string
  lineStart: number
  lineEnd: number
}

/**
 * The Drafts window. Review shows the text by line: select a passage and mark it (a comment,
 * a question, a suggested replacement). Edit changes the text; every save is a revision.
 * Compare shows any revision against another. The marks are listed on the right.
 */
export function DraftsWindow({ winId, args }: WindowProps) {
  const desk = useDesk()
  const [docs, setDocs] = useState<CollabDoc[]>([])
  const [name, setName] = useState(typeof args.name === "string" ? args.name : "")
  const [doc, setDoc] = useState<CollabDoc | null>(null)
  const [marks, setMarks] = useState<CollabMark[]>([])
  const [mode, setMode] = useState<Mode>("review")
  const [text, setText] = useState("")
  const [title, setTitle] = useState("")
  const [revs, setRevs] = useState<CollabRevision[]>([])
  const [from, setFrom] = useState(0)
  const [fromBody, setFromBody] = useState<string | null>(null)
  const [picked, setPicked] = useState<Picked | null>(null)
  const [active, setActive] = useState("")
  const [note, setNote] = useState("")
  const [error, setError] = useState("")
  const [newer, setNewer] = useState(0)
  const lines = useRef<HTMLDivElement>(null)
  const dirty = !!doc && (text !== doc.body || title !== doc.title)
  const dirtyRef = useRef(dirty)
  dirtyRef.current = dirty
  const nameRef = useRef(name)
  nameRef.current = name

  const fail = useCallback(
    (e: unknown) => setError(e instanceof Error ? e.message : "That did not work."),
    [],
  )

  const loadList = useCallback(() => collab.list("draft").then(setDocs).catch(fail), [fail])

  const load = useCallback(
    async (which: string, keepEdits = false) => {
      if (!which) return
      try {
        const r = await collab.get(which)
        if (nameRef.current !== which) return
        setMarks(r.marks)
        if (keepEdits && dirtyRef.current) {
          // The agent (or another window) saved while there are unsaved edits here: say so.
          setDoc((old) => {
            if (old && r.doc.rev > old.rev) setNewer(r.doc.rev)
            return old
          })
          return
        }
        setDoc(r.doc)
        setText(r.doc.body)
        setTitle(r.doc.title)
        setNewer(0)
        setError("")
        void collab
          .log(which)
          .then(setRevs)
          .catch(() => setRevs([]))
      } catch (e) {
        fail(e)
      }
    },
    [fail],
  )

  useEffect(() => {
    void loadList()
  }, [loadList])
  useEffect(() => {
    setDoc(null)
    setPicked(null)
    setFrom(0)
    setFromBody(null)
    void load(name)
  }, [name, load])

  // A change made elsewhere (the agent wrote a revision or a mark) shows here at once.
  useEffect(() => {
    const onChange = (e: Event) => {
      void loadList()
      if ((e as CustomEvent<string>).detail === nameRef.current) void load(nameRef.current, true)
    }
    window.addEventListener(COLLAB_CHANGED, onChange)
    const timer = window.setInterval(() => void load(nameRef.current, true), 30_000)
    return () => {
      window.removeEventListener(COLLAB_CHANGED, onChange)
      window.clearInterval(timer)
    }
  }, [load, loadList])

  // What the agent is told this window shows.
  useEffect(() => {
    if (doc)
      collabOpen.set(winId, {
        name: doc.name,
        rev: doc.rev,
        openMarks: marks.filter((m) => m.state === "open").length,
      })
    desk.setTitle(winId, doc ? `${dirty ? "● " : ""}${doc.title}` : "Drafts")
    desk.setDirty(winId, dirty)
    return () => {
      collabOpen.delete(winId)
    }
  }, [doc, marks, dirty, winId, desk])

  const save = async (body = text, force = false): Promise<CollabDoc | null> => {
    if (!doc) return null
    try {
      const saved = await collab.save(
        doc.name,
        body,
        force ? 0 : doc.rev,
        title !== doc.title ? title : undefined,
      )
      setDoc(saved)
      setText(saved.body)
      setTitle(saved.title)
      setNewer(0)
      setError("")
      setNote(`Saved as revision ${saved.rev}.`)
      void collab
        .log(saved.name)
        .then(setRevs)
        .catch(() => undefined)
      void loadList()
      return saved
    } catch (e) {
      if (isConflict(e)) setNewer((e as { rev?: number }).rev ?? doc.rev + 1)
      fail(e)
      return null
    }
  }

  const create = async () => {
    try {
      const d = await collab.create({
        kind: "draft",
        title: "Untitled draft",
        lang: "markdown",
        body: "",
      })
      await loadList()
      setName(d.name)
      setMode("edit")
    } catch (e) {
      fail(e)
    }
  }

  // The comparison's other side.
  useEffect(() => {
    if (mode !== "diff" || !doc) return
    const rev = from || Math.max(1, doc.rev - 1)
    if (rev === doc.rev) return setFromBody(doc.body)
    let live = true
    void collab
      .get(doc.name, rev)
      .then((r) => live && setFromBody(r.doc.body))
      .catch(fail)
    return () => {
      live = false
    }
  }, [mode, from, doc, fail])

  const body = doc?.body ?? ""
  const split = useMemo(() => body.split("\n"), [body])
  // Which lines carry an open mark, and which marks have lost their passage.
  const placed = useMemo(() => {
    const byLine = new Map<number, CollabMark[]>()
    const lost = new Set<string>()
    for (const m of marks) {
      if (!m.anchor.quote) continue
      const at = locate(body, m.anchor)
      if (!at) {
        lost.add(m.id)
        continue
      }
      if (m.state !== "open") continue
      for (let n = at.lineStart; n <= at.lineEnd; n++) byLine.set(n, [...(byLine.get(n) ?? []), m])
    }
    return { byLine, lost }
  }, [marks, body])

  const onSelect = () => {
    const sel = window.getSelection()
    const root = lines.current
    if (
      !sel ||
      sel.isCollapsed ||
      !root ||
      !root.contains(sel.anchorNode) ||
      !root.contains(sel.focusNode)
    )
      return
    const range = sel.getRangeAt(0)
    const lineOf = (node: Node) =>
      (node instanceof Element ? node : node.parentElement)?.closest<HTMLElement>("[data-line]")
    const a = lineOf(range.startContainer)
    const b = lineOf(range.endContainer)
    if (!a || !b) return
    const lineStart = Number(a.dataset.line)
    const lineEnd = Number(b.dataset.line)
    // The passage from the text itself: the offset of each end within its line.
    const within = (el: HTMLElement, node: Node, offset: number) => {
      const r = document.createRange()
      r.selectNodeContents(el.querySelector(".collab-code") ?? el)
      r.setEnd(node, offset)
      return r.toString().length
    }
    const startOffset =
      split.slice(0, lineStart - 1).reduce((n, l) => n + l.length + 1, 0) +
      within(a, range.startContainer, range.startOffset)
    const endOffset =
      split.slice(0, lineEnd - 1).reduce((n, l) => n + l.length + 1, 0) +
      within(b, range.endContainer, range.endOffset)
    const quote = body.slice(startOffset, endOffset)
    if (!quote.trim()) return
    setPicked({
      quote,
      prefix: body.slice(Math.max(0, startOffset - 40), startOffset),
      lineStart,
      lineEnd,
    })
  }

  const addMark = async (m: { kind: CollabMark["kind"]; text: string; replace?: string }) => {
    if (!doc || !picked) return
    try {
      setMarks(
        await collab.mark(doc.name, "add", {
          rev: doc.rev,
          kind: m.kind,
          text: m.text,
          replace: m.replace,
          anchor: {
            quote: picked.quote,
            prefix: picked.prefix,
            line_start: picked.lineStart,
            line_end: picked.lineEnd,
          },
        }),
      )
      setPicked(null)
      window.getSelection()?.removeAllRanges()
    } catch (e) {
      fail(e)
    }
  }
  const setState = (m: CollabMark, state: CollabMark["state"]) =>
    doc && void collab.mark(doc.name, "update", { id: m.id, state }).then(setMarks).catch(fail)
  const remove = (m: CollabMark) =>
    doc && void collab.mark(doc.name, "remove", { id: m.id }).then(setMarks).catch(fail)
  const accept = async (m: CollabMark) => {
    if (!doc || m.replace === undefined) return
    if (dirty)
      return setError(
        "Save or drop your edits first: accepting a suggestion writes a new revision.",
      )
    const at = locate(doc.body, m.anchor)
    if (!at || !m.anchor.quote) return
    const next =
      doc.body.slice(0, at.offset) + m.replace + doc.body.slice(at.offset + m.anchor.quote.length)
    const saved = await save(next)
    if (saved) setState(m, "done")
  }
  const goTo = (m: CollabMark) => {
    setActive(m.id)
    if (mode !== "review") setMode("review")
    const at = locate(body, m.anchor)
    requestAnimationFrame(() =>
      lines.current
        ?.querySelector(`[data-line="${at?.lineStart ?? m.anchor.line_start ?? 1}"]`)
        ?.scrollIntoView({ block: "center" }),
    )
  }

  const compare =
    fromBody !== null && doc ? diffLines(fromBody, mode === "diff" && dirty ? text : doc.body) : []
  const stat = diffStat(compare)
  const fromRev = from || (doc ? Math.max(1, doc.rev - 1) : 0)

  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: the window's own key (⌘S)
    <div
      className="collab"
      onKeyDown={(e) => {
        if ((e.metaKey || e.ctrlKey) && e.key === "s") {
          e.preventDefault()
          if (dirty) void save()
        }
      }}
    >
      <nav className="collab-list">
        <header>
          <strong>Drafts</strong>
          <button
            type="button"
            title="New draft"
            aria-label="New draft"
            onClick={() => void create()}
          >
            <FilePlus2 {...ICON} />
          </button>
        </header>
        {docs.map((d) => (
          <button
            key={d.name}
            type="button"
            data-on={d.name === name}
            onClick={() => setName(d.name)}
          >
            <span>{d.title || d.name}</span>
            <em>r{d.rev}</em>
          </button>
        ))}
        {!docs.length && (
          <p className="collab-none">
            No drafts yet. Start one here, ask the agent to draft something, or use “Draft: start
            one from what this window shows”.
          </p>
        )}
      </nav>
      {doc ? (
        <section className="collab-main">
          <header className="collab-bar">
            <input
              className="collab-title"
              value={title}
              aria-label="Title"
              onChange={(e) => setTitle(e.target.value)}
            />
            <span className="collab-rev" title={doc.name}>
              r{doc.rev} · {doc.lang || "text"}
            </span>
            <div className="collab-modes" role="tablist">
              <button type="button" data-on={mode === "review"} onClick={() => setMode("review")}>
                <ScanText {...ICON} /> Review
              </button>
              <button type="button" data-on={mode === "edit"} onClick={() => setMode("edit")}>
                <Pencil {...ICON} /> Edit
              </button>
              <button type="button" data-on={mode === "diff"} onClick={() => setMode("diff")}>
                <GitCompare {...ICON} /> Compare
              </button>
              {doc.lang === "markdown" && (
                <button
                  type="button"
                  data-on={mode === "preview"}
                  onClick={() => setMode("preview")}
                >
                  Preview
                </button>
              )}
            </div>
            <button
              type="button"
              className="collab-primary"
              disabled={!dirty}
              title="Save a new revision (⌘S)"
              onClick={() => void save()}
            >
              <Save {...ICON} /> Save
            </button>
          </header>
          {(error || newer > 0 || note) && (
            <p className="collab-status" data-tone={error || newer ? "warn" : "ok"} role="status">
              {newer > 0
                ? `Revision ${newer} was saved elsewhere while you were editing. `
                : error || note}
              {newer > 0 && (
                <>
                  <button type="button" onClick={() => void load(doc.name)}>
                    Load it (drops your edits)
                  </button>
                  <button type="button" onClick={() => void save(text, true)}>
                    Save mine over it
                  </button>
                </>
              )}
            </p>
          )}
          {mode === "review" && (
            // biome-ignore lint/a11y/noStaticElementInteractions: a selection in the text opens the mark form
            <div className="collab-lines" ref={lines} onMouseUp={onSelect}>
              {dirty && (
                <p className="collab-hint">
                  You have unsaved edits; this shows the saved text (revision {doc.rev}).
                </p>
              )}
              {split.map((line, i) => {
                const on = placed.byLine.get(i + 1)
                return (
                  <div
                    // biome-ignore lint/suspicious/noArrayIndexKey: lines of a fixed text, by number
                    key={i}
                    className="collab-line"
                    data-line={i + 1}
                    data-marked={!!on}
                    data-active={!!on?.some((m) => m.id === active)}
                    data-agent={!!on?.some((m) => m.by === "agent")}
                  >
                    <span className="collab-n" aria-hidden="true">
                      {i + 1}
                    </span>
                    <span className="collab-code">{line || "​"}</span>
                  </div>
                )
              })}
            </div>
          )}
          {mode === "edit" && (
            <textarea
              className="collab-edit"
              value={text}
              spellCheck={doc.lang === "markdown" || doc.lang === "text"}
              aria-label="The draft's text"
              onChange={(e) => {
                setText(e.target.value)
                setNote("")
              }}
            />
          )}
          {mode === "preview" && (
            <div className="collab-preview">
              <Markdown source={text} />
            </div>
          )}
          {mode === "diff" && (
            <div className="collab-diff">
              <header>
                <History {...ICON} />
                <select
                  value={fromRev}
                  aria-label="Compare from revision"
                  onChange={(e) => setFrom(Number(e.target.value))}
                >
                  {revs
                    .filter((r) => r.rev < doc.rev || dirty)
                    .map((r) => (
                      <option key={r.rev} value={r.rev}>
                        revision {r.rev} ·{" "}
                        {new Date(r.at).toLocaleString([], {
                          month: "short",
                          day: "numeric",
                          hour: "2-digit",
                          minute: "2-digit",
                        })}
                      </option>
                    ))}
                  {!revs.some((r) => r.rev === fromRev) && (
                    <option value={fromRev}>revision {fromRev}</option>
                  )}
                </select>
                <span>→ {dirty ? "your unsaved text" : `revision ${doc.rev}`}</span>
                <span className="collab-stat">
                  <b>+{stat.added}</b> <i>−{stat.removed}</i>
                </span>
              </header>
              <div className="collab-difflines">
                {compare.map((l, i) => (
                  // biome-ignore lint/suspicious/noArrayIndexKey: lines of a fixed comparison
                  <div key={i} className="collab-line" data-op={l.op}>
                    <span className="collab-n">{l.a ?? ""}</span>
                    <span className="collab-n">{l.b ?? ""}</span>
                    <span className="collab-code">{l.text || "​"}</span>
                  </div>
                ))}
                {!stat.added && !stat.removed && (
                  <p className="collab-none">No difference between these.</p>
                )}
              </div>
            </div>
          )}
          {doc.lang && mode === "edit" && (
            <footer className="collab-foot">
              <label>
                written in{" "}
                <select
                  value={doc.lang}
                  onChange={() =>
                    setNote(
                      "The language is set when a draft is made; it only changes how it is shown.",
                    )
                  }
                >
                  {[...new Set([doc.lang, ...LANGS])].map((l) => (
                    <option key={l}>{l}</option>
                  ))}
                </select>
              </label>
              <span>{text.split("\n").length} lines</span>
            </footer>
          )}
        </section>
      ) : (
        <section className="collab-main collab-empty">
          <p>{name ? "Opening…" : "Pick a draft, or start one."}</p>
        </section>
      )}
      {doc && (
        <div className="collab-side">
          {picked && (
            <MarkComposer
              what={
                picked.lineEnd !== picked.lineStart
                  ? `lines ${picked.lineStart}–${picked.lineEnd}`
                  : `line ${picked.lineStart}`
              }
              quote={picked.quote}
              canSuggest
              onAdd={(m) => void addMark(m)}
              onCancel={() => setPicked(null)}
            />
          )}
          <MarksPanel
            marks={marks}
            active={active}
            detached={(m) => placed.lost.has(m.id)}
            onPick={goTo}
            onState={setState}
            onRemove={remove}
            onAccept={(m) => void accept(m)}
          />
        </div>
      )}
    </div>
  )
}
