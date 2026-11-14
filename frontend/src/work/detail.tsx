/**
 * An item's detail, beside the view: title, stage and fields, the body's sections edited in
 * place, tags, links, material and the publication log, the owner's unsent notes, and the
 * item's revisions with a word diff against any of them.
 */
import { Scissors } from "lucide-react"
import { useCallback, useEffect, useState } from "react"
import {
  type WorkCard,
  type WorkDetail,
  type WorkField,
  type WorkKind,
  type WorkRevision,
  work,
} from "../api/work"
import { ago, diffWords, kindIcon, label, message } from "./util"

export function Detail({
  ws,
  id,
  kinds,
  items,
  revOnBoard,
  close,
  editing,
  changed,
}: {
  ws: string
  id: string
  kinds: Map<string, WorkKind>
  items: WorkCard[]
  /** The item's revision in the view's copy: a newer one reloads the detail. */
  revOnBoard: number
  close: () => void
  editing: (on: boolean) => void
  changed: () => void
}) {
  const [d, setD] = useState<WorkDetail | null>(null)
  const [error, setError] = useState("")
  const [status, setStatus] = useState("")
  const [edit, setEdit] = useState<{ section: string; text: string } | null>(null)
  const [title, setTitle] = useState("")
  const [showRevs, setShowRevs] = useState(false)

  const load = useCallback(async () => {
    try {
      const r = await work.item(ws, id)
      setD(r)
      setTitle(r.title)
      setError("")
    } catch (e) {
      setError(message(e))
    }
  }, [ws, id])
  // biome-ignore lint/correctness/useExhaustiveDependencies: a newer revision on the board reloads it
  useEffect(() => {
    if (!edit) void load()
  }, [load, revOnBoard])

  if (!d)
    return (
      <aside className="work-detail">
        <header>
          <span className="work-detail-title">{error || "…"}</span>
          <button type="button" className="work-icon" onClick={close} title="Close">
            ×
          </button>
        </header>
      </aside>
    )

  const k = kinds.get(d.kind)
  const Icon = kindIcon(k?.icon)
  const save = async (change: {
    title?: string
    fields?: Record<string, unknown>
    sections?: Record<string, string>
    tags?: string[]
  }) => {
    setStatus("Saving…")
    try {
      const r = await work.edit(ws, id, { base_rev: d.rev, ...change })
      setStatus(`Saved as revision ${r.rev}.`)
    } catch (e) {
      // 409: changed in Caster meanwhile; the owner's words became suggestions.
      setStatus(message(e))
    }
    editing(false)
    await load()
    changed()
  }
  const titleOf = (ref: string) => items.find((x) => x.id === ref)?.title ?? ref

  return (
    <aside className="work-detail">
      <header>
        <Icon size={14} className="work-kindicon" />
        <input
          className="work-detail-title"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          onBlur={() => title.trim() && title !== d.title && void save({ title })}
          onKeyDown={(e) => e.key === "Enter" && e.currentTarget.blur()}
          aria-label="Title"
        />
        <button type="button" className="work-icon" onClick={close} title="Close">
          ×
        </button>
      </header>
      <div className="work-detail-meta">
        <span title={d.ref}>
          {k?.label ?? d.kind} · {d.id} · revision {d.rev}
        </span>
        {k && k.stages.length > 0 && (
          <select
            className="work-select"
            value={d.stage}
            onChange={async (e) => {
              setStatus("")
              try {
                await work.move(ws, id, "stage", e.target.value)
              } catch (err) {
                setStatus(message(err))
              }
              await load()
              changed()
            }}
            aria-label="Stage"
          >
            {!k.stages.includes(d.stage) && <option value={d.stage}>{d.stage || "—"}</option>}
            {k.stages.map((s) => (
              <option key={s} value={s}>
                {label(s)}
              </option>
            ))}
          </select>
        )}
        <span className="work-spacer" />
        <button
          type="button"
          className="work-icon"
          title={d.cut ? "Put it back" : "Cut: take it out of the views (it stays in Caster)"}
          onClick={async () => {
            try {
              if (d.cut) await work.restore(ws, id)
              else await work.cut(ws, id)
            } catch (e) {
              setStatus(message(e))
            }
            await load()
            changed()
          }}
        >
          {d.cut ? "Restore" : <Scissors size={13} />}
        </button>
      </div>
      {d.problem && <p className="work-problem-inline">{d.problem}</p>}
      {status && <p className="work-status">{status}</p>}

      {k && k.fields.length > 0 && (
        <div className="work-fields">
          {k.fields.map((f) => (
            <FieldRow
              key={f.key}
              field={f}
              value={d.fields[f.key]}
              items={items}
              set={(v) => void save({ fields: { [f.key]: v } })}
            />
          ))}
        </div>
      )}

      {d.lead && <div className="work-lead">{d.lead}</div>}
      {d.order.map((sec) => (
        <section key={sec} className="work-sec">
          <h4>{sec}</h4>
          {edit?.section === sec ? (
            <textarea
              // biome-ignore lint/a11y/noAutofocus: the section the owner clicked to edit
              autoFocus
              value={edit.text}
              onChange={(e) => setEdit({ section: sec, text: e.target.value })}
              onKeyDown={(e) => {
                if (e.key === "Escape") {
                  setEdit(null)
                  editing(false)
                } else if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                  e.currentTarget.blur()
                }
              }}
              onBlur={() => {
                const text = edit.text
                setEdit(null)
                if (text.trim() !== (d.sections[sec] ?? "").trim())
                  void save({ sections: { [sec]: text } })
                else editing(false)
              }}
              rows={Math.max(4, edit.text.split("\n").length + 1)}
            />
          ) : (
            // biome-ignore lint/a11y/noStaticElementInteractions: click to edit in place
            <div
              className="work-sec-text"
              data-empty={!d.sections[sec]}
              onClick={() => {
                editing(true)
                setEdit({ section: sec, text: d.sections[sec] ?? "" })
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  editing(true)
                  setEdit({ section: sec, text: d.sections[sec] ?? "" })
                }
              }}
              title="Click to edit (⌘↩ or clicking away keeps it, Esc does not)"
            >
              {d.sections[sec] || "Click to write"}
            </div>
          )}
        </section>
      ))}

      <section className="work-sec">
        <h4>Tags</h4>
        <TagsRow tags={d.tags} set={(tags) => void save({ tags })} />
      </section>

      {(d.links.length > 0 || d.children.length > 0 || d.parent) && (
        <section className="work-sec">
          <h4>Links</h4>
          {d.parent && <div className="work-rowtext">in {titleOf(d.parent)}</div>}
          {d.children.length > 0 && (
            <div className="work-rowtext">holds {d.children.map(titleOf).join(", ")}</div>
          )}
          {d.links.map((l) => (
            <div key={`${l.rel}:${l.to}`} className="work-rowtext">
              {l.rel} {titleOf(l.to)}
            </div>
          ))}
        </section>
      )}
      {d.material.length > 0 && (
        <section className="work-sec">
          <h4>Material</h4>
          {d.material.map((m) => (
            <div key={m.ref} className="work-rowtext" title={m.ref}>
              {m.caption || m.ref}
            </div>
          ))}
        </section>
      )}
      {d.published.length > 0 && (
        <section className="work-sec">
          <h4>Published</h4>
          {d.published.map((p) => (
            <div key={`${p.at}:${p.where}`} className="work-rowtext">
              {p.at} · {p.where}
              {p.url ? ` · ${p.url}` : ""}
            </div>
          ))}
        </section>
      )}

      {d.notes.length > 0 && (
        <section className="work-sec">
          <h4>Your notes, not sent</h4>
          {d.notes.map((n) => (
            <div key={n.id} className="work-note" data-kind={n.kind}>
              <div className="work-note-head">
                <b>{n.kind === "suggestion" ? "Suggestion" : "Comment"}</b>
                {n.section && <span>{n.section}</span>}
                <span>on revision {n.item_rev}</span>
                <span className="work-spacer" />
                <button
                  type="button"
                  className="work-icon"
                  title="Take it out"
                  onClick={async () => {
                    await work.dropNote(ws, n.id)
                    await load()
                  }}
                >
                  ×
                </button>
              </div>
              {n.quote && <div className="work-quote">− {n.quote}</div>}
              {n.kind === "suggestion" && <div className="work-note-replace">+ {n.replace}</div>}
              {n.text && <div className="work-rowtext">{n.text}</div>}
            </div>
          ))}
        </section>
      )}

      <section className="work-sec">
        <h4>
          <button type="button" className="work-link" onClick={() => setShowRevs((s) => !s)}>
            {showRevs ? "Hide revisions" : "Revisions"}
          </button>
        </h4>
        {showRevs && <Revisions ws={ws} d={d} />}
      </section>
    </aside>
  )
}

/** One field of the item, edited by its type. */
function FieldRow({
  field,
  value,
  items,
  set,
}: {
  field: WorkField
  value: unknown
  items: WorkCard[]
  set: (v: unknown) => void
}) {
  const [text, setText] = useState(value == null ? "" : String(value))
  useEffect(() => setText(value == null ? "" : String(value)), [value])
  const name = field.label || field.key
  const commit = (v: string) => {
    if (v === (value == null ? "" : String(value))) return
    if (field.type === "number") set(v.trim() === "" ? null : Number(v))
    else set(v.trim() === "" ? null : v)
  }
  let input: React.ReactNode
  switch (field.type) {
    case "enum":
      input = (
        <select
          className="work-select"
          value={text}
          onChange={(e) => commit(e.target.value)}
          aria-label={name}
        >
          <option value="">—</option>
          {(field.options ?? []).map((o) => (
            <option key={o} value={o}>
              {o}
            </option>
          ))}
        </select>
      )
      break
    case "bool":
      input = (
        <input
          type="checkbox"
          checked={value === true}
          onChange={(e) => set(e.target.checked)}
          aria-label={name}
        />
      )
      break
    case "ref":
      input = (
        <select
          className="work-select"
          value={text}
          onChange={(e) => commit(e.target.value)}
          aria-label={name}
        >
          <option value="">—</option>
          {!items.some((x) => x.id === text) && text && <option value={text}>{text}</option>}
          {items
            .filter((x) => !x.cut)
            .map((x) => (
              <option key={x.id} value={x.id}>
                {x.title}
              </option>
            ))}
        </select>
      )
      break
    default:
      input = (
        <input
          type={field.type === "date" ? "date" : field.type === "number" ? "number" : "text"}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onBlur={() => commit(text)}
          onKeyDown={(e) => e.key === "Enter" && e.currentTarget.blur()}
          placeholder={field.type === "url" ? "https://…" : ""}
          aria-label={name}
        />
      )
  }
  return (
    <div className="work-field">
      <span>{name}</span>
      {input}
    </div>
  )
}

function TagsRow({ tags, set }: { tags: string[]; set: (t: string[]) => void }) {
  const [text, setText] = useState(tags.join(", "))
  useEffect(() => setText(tags.join(", ")), [tags])
  return (
    <input
      className="work-tags"
      value={text}
      onChange={(e) => setText(e.target.value)}
      onBlur={() => {
        const next = text
          .split(",")
          .map((t) => t.trim())
          .filter(Boolean)
        if (next.join(",") !== tags.join(",")) set(next)
      }}
      onKeyDown={(e) => e.key === "Enter" && e.currentTarget.blur()}
      placeholder="comma, separated"
      aria-label="Tags"
    />
  )
}

/** The item's revisions; picking one shows what changed from it to the current revision. */
function Revisions({ ws, d }: { ws: string; d: WorkDetail }) {
  const name = `i.${d.id}`
  const [revs, setRevs] = useState<WorkRevision[] | null>(null)
  const [from, setFrom] = useState<number | null>(null)
  const [before, setBefore] = useState<{
    title?: string
    stage?: string
    sections: Record<string, string>
  } | null>(null)
  const [error, setError] = useState("")
  useEffect(() => {
    work
      .log(ws, name)
      .then((r) => setRevs(r.revisions))
      .catch((e) => setError(message(e)))
  }, [ws, name])
  useEffect(() => {
    if (from == null) return
    setBefore(null)
    work
      .at(ws, name, from)
      .then((r) => setBefore({ title: r.title, stage: r.stage, sections: r.sections ?? {} }))
      .catch((e) => setError(message(e)))
  }, [ws, name, from])
  if (error) return <p className="work-problem-inline">{error}</p>
  if (!revs) return <p className="work-rowtext">…</p>
  const norm = (h: string) => h.toLowerCase().replace(/[\s_-]/g, "")
  const sectionAt = (secs: Record<string, string>, h: string) =>
    Object.entries(secs).find(([k]) => norm(k) === norm(h))?.[1] ?? ""
  const changedSecs = before
    ? d.order.filter((s) => sectionAt(before.sections, s).trim() !== (d.sections[s] ?? "").trim())
    : []
  return (
    <div className="work-revs">
      <div className="work-revlist">
        {revs.map((r) => (
          <button
            type="button"
            key={r.rev}
            data-on={from === r.rev}
            disabled={r.rev === d.rev}
            onClick={() => setFrom(r.rev)}
            title={r.rev === d.rev ? "The current revision" : "Compare with the current revision"}
          >
            {r.rev}
            {r.at ? ` · ${ago(r.at)}` : ""}
          </button>
        ))}
      </div>
      {before && from != null && (
        <div className="work-changes">
          <div className="work-rowtext">
            Revision {from} → {d.rev}
            {before.title !== d.title ? ` · title was "${before.title}"` : ""}
            {before.stage !== d.stage ? ` · stage was ${before.stage || "—"}` : ""}
          </div>
          {changedSecs.length === 0 && (
            <div className="work-rowtext">No section changed; only its details did.</div>
          )}
          {changedSecs.map((sec) => (
            <div key={sec} className="work-diff">
              <b>{sec}</b>
              <div className="work-rowtext">
                {diffWords(sectionAt(before.sections, sec), d.sections[sec] ?? "").map((p, i) =>
                  p.op === "same" ? (
                    // biome-ignore lint/suspicious/noArrayIndexKey: parts of one fixed diff
                    <span key={i}>{p.text}</span>
                  ) : p.op === "del" ? (
                    // biome-ignore lint/suspicious/noArrayIndexKey: parts of one fixed diff
                    <del key={i}>{p.text}</del>
                  ) : (
                    // biome-ignore lint/suspicious/noArrayIndexKey: parts of one fixed diff
                    <ins key={i}>{p.text}</ins>
                  ),
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
