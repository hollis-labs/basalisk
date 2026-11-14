/**
 * An item's detail, beside the view: title, stage and fields, the body's sections edited in
 * place, tags, links, material and the publication log, the owner's unsent notes, and the
 * item's revisions with a word diff against any of them.
 */
import { Scissors, X } from "lucide-react"
import { useCallback, useEffect, useRef, useState } from "react"
import { hostOf, type ReaderItem, reader } from "../api/reader"
import {
  type WorkCard,
  type WorkDetail,
  type WorkEdit,
  type WorkField,
  type WorkKind,
  type WorkLink,
  type WorkMaterial,
  type WorkPublished,
  type WorkRevision,
  work,
} from "../api/work"
import { CiteText } from "../cite/ui"
import { ymd } from "./calendar"
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
  open,
}: {
  ws: string
  id: string
  kinds: Map<string, WorkKind>
  items: WorkCard[]
  /** Open another item in the detail. */
  open: (id: string) => void
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
  const save = async (change: Omit<WorkEdit, "base_rev">) => {
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
  const channelOf = (v: unknown) => (typeof v === "string" && v ? titleOf(v) : "")

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
              self={d.id}
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

      <section className="work-sec">
        <h4>Links</h4>
        <Links
          d={d}
          items={items}
          kinds={kinds}
          open={open}
          set={(links) => void save({ links })}
        />
      </section>
      <section className="work-sec">
        <h4>Material</h4>
        <MaterialList material={d.material} set={(material) => void save({ material })} />
      </section>
      {(k?.stages.includes("published") || d.published.length > 0) && (
        <section className="work-sec">
          <h4>Published</h4>
          <PublicationLog
            log={d.published}
            where={channelOf(d.fields.channel)}
            set={(published) => void save({ published })}
          />
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
  self,
  value,
  items,
  set,
}: {
  field: WorkField
  self: string
  value: unknown
  items: WorkCard[]
  set: (v: unknown) => void
}) {
  // A ref field's options name the kinds it may point at (empty: any kind).
  const targets = items.filter(
    (x) =>
      !x.cut &&
      x.id !== self &&
      (field.type === "enum" || !field.options?.length || field.options.includes(x.kind)),
  )
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
          {!targets.some((x) => x.id === text) && text && (
            <option value={text}>{items.find((x) => x.id === text)?.title ?? text}</option>
          )}
          {targets.map((x) => (
            <option key={x.id} value={x.id}>
              {x.title}
            </option>
          ))}
        </select>
      )
      break
    case "refs": {
      const list = Array.isArray(value) ? value.map(String) : []
      input = (
        <span className="work-refs">
          {list.map((r) => (
            <span key={r} className="work-refchip">
              {items.find((x) => x.id === r)?.title ?? r}
              <button
                type="button"
                className="work-icon"
                title="Take it out"
                onClick={() => {
                  const next = list.filter((x) => x !== r)
                  set(next.length ? next : null)
                }}
              >
                <X size={10} />
              </button>
            </span>
          ))}
          <select
            className="work-select"
            value=""
            onChange={(e) => e.target.value && set([...list, e.target.value])}
            aria-label={`Add to ${name}`}
          >
            <option value="">+ add</option>
            {targets
              .filter((x) => !list.includes(x.id))
              .map((x) => (
                <option key={x.id} value={x.id}>
                  {x.title}
                </option>
              ))}
          </select>
        </span>
      )
      break
    }
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

/**
 * Material: references to things kept elsewhere, never copies. Each shows as its Cite chip
 * (a fragment, a Caster item, a Tesseract item, a link). Add one by pasting its reference or
 * by picking a fragment from the Fragments Engine library.
 */
function MaterialList({
  material,
  set,
}: {
  material: WorkMaterial[]
  set: (m: WorkMaterial[]) => void
}) {
  const [ref, setRef] = useState("")
  const [picking, setPicking] = useState(false)
  const add = (m: WorkMaterial) => {
    const r = m.ref.trim()
    if (!r || material.some((x) => x.ref === r)) return
    set([...material, { ref: r, caption: m.caption?.trim() || undefined }])
  }
  return (
    <div className="work-material">
      {material.length === 0 && <div className="work-rowtext work-dim">Nothing attached.</div>}
      {material.map((m, i) => (
        <div key={m.ref} className="work-matrow">
          <span className="work-matref">
            <CiteText text={m.ref} />
          </span>
          <Caption
            value={m.caption ?? ""}
            set={(caption) =>
              set(material.map((x, j) => (j === i ? { ...x, caption: caption || undefined } : x)))
            }
          />
          <button
            type="button"
            className="work-icon"
            title="Take it off (the thing itself is not touched)"
            onClick={() => set(material.filter((_, j) => j !== i))}
          >
            <X size={12} />
          </button>
        </div>
      ))}
      <form
        className="work-matadd"
        onSubmit={(e) => {
          e.preventDefault()
          add({ ref })
          setRef("")
        }}
      >
        <input
          value={ref}
          onChange={(e) => setRef(e.target.value)}
          placeholder="Paste a reference: tach:fragment/…, caster://…, a link"
          aria-label="Reference"
        />
        <button type="submit" className="desk-button" disabled={!ref.trim()}>
          Add
        </button>
        <button type="button" className="desk-button" onClick={() => setPicking((p) => !p)}>
          {picking ? "Close" : "From Fragments…"}
        </button>
      </form>
      {picking && (
        <FragmentPicker
          taken={new Set(material.map((m) => m.ref))}
          pick={(it) =>
            add({ ref: `tach:fragment/${it.fragment_id}`, caption: it.display.title.value })
          }
        />
      )}
    </div>
  )
}

function Caption({ value, set }: { value: string; set: (v: string) => void }) {
  const [text, setText] = useState(value)
  useEffect(() => setText(value), [value])
  return (
    <input
      className="work-caption"
      value={text}
      onChange={(e) => setText(e.target.value)}
      onBlur={() => text.trim() !== value && set(text.trim())}
      onKeyDown={(e) => e.key === "Enter" && e.currentTarget.blur()}
      placeholder="caption"
      aria-label="Caption"
    />
  )
}

/** A search over the Fragments Engine library (the Reader's own route); picking cites it. */
function FragmentPicker({ taken, pick }: { taken: Set<string>; pick: (it: ReaderItem) => void }) {
  const [q, setQ] = useState("")
  const [items, setItems] = useState<ReaderItem[] | null>(null)
  const [error, setError] = useState("")
  const seq = useRef(0)
  useEffect(() => {
    const n = ++seq.current
    const t = window.setTimeout(
      () => {
        reader
          .list({ scope: "library", q: q.trim() || undefined })
          .then((r) => {
            if (n !== seq.current) return
            setItems(r.items.slice(0, 30))
            setError("")
          })
          .catch((e) => n === seq.current && setError(message(e)))
      },
      q ? 300 : 0,
    )
    return () => window.clearTimeout(t)
  }, [q])
  return (
    <div className="work-picker">
      <input
        // biome-ignore lint/a11y/noAutofocus: the owner just opened the picker
        autoFocus
        value={q}
        onChange={(e) => setQ(e.target.value)}
        placeholder="Search your fragments"
        aria-label="Search fragments"
      />
      {error && <p className="work-problem-inline">{error}</p>}
      {!error && items === null && <p className="work-rowtext work-dim">…</p>}
      {items?.length === 0 && <p className="work-rowtext work-dim">Nothing found.</p>}
      {items?.map((it) => {
        const ref = `tach:fragment/${it.fragment_id}`
        const host = hostOf(it.source.canonical_url ?? it.source.submitted_url)
        return (
          <button
            type="button"
            key={it.fragment_id}
            className="work-pickrow"
            disabled={taken.has(ref)}
            onClick={() => pick(it)}
            title={taken.has(ref) ? "Already attached" : "Attach it by reference"}
          >
            <span>{it.display.title.value || it.fragment_id.slice(0, 12)}</span>
            <span className="work-dim">
              {it.renderer}
              {host ? ` · ${host}` : ""}
            </span>
          </button>
        )
      })}
    </div>
  )
}

/**
 * The publication log: when and where it went out, with the address. Publishing is not done
 * here; this records it. Reaching the Published stage starts the log with one entry.
 */
function PublicationLog({
  log,
  where,
  set,
}: {
  log: WorkPublished[]
  where: string
  set: (l: WorkPublished[]) => void
}) {
  const [editing, setEditing] = useState<number | null>(null)
  const blank = (): WorkPublished => ({ at: ymd(new Date()), where, url: "", note: "" })
  return (
    <div className="work-publog">
      {log.length === 0 && editing === null && (
        <div className="work-rowtext work-dim">Not published yet.</div>
      )}
      {log.map((p, i) =>
        editing === i ? (
          <PublicationForm
            // biome-ignore lint/suspicious/noArrayIndexKey: entries may repeat; the log's order is its identity
            key={`${p.at}:${p.where}:${i}`}
            start={p}
            done={(next) => {
              setEditing(null)
              if (next) set(log.map((x, j) => (j === i ? next : x)))
            }}
          />
        ) : (
          // biome-ignore lint/suspicious/noArrayIndexKey: entries may repeat; the log's order is its identity
          <div key={`${p.at}:${p.where}:${i}`} className="work-pubrow">
            <button
              type="button"
              className="work-link"
              onClick={() => setEditing(i)}
              title="Change this entry"
            >
              {p.at.slice(0, 10)} · {p.where}
            </button>
            {p.url && <CiteText text={p.url} />}
            {p.note && <span className="work-dim">{p.note}</span>}
            <span className="work-spacer" />
            <button
              type="button"
              className="work-icon"
              title="Take this entry out of the log"
              onClick={() => set(log.filter((_, j) => j !== i))}
            >
              <X size={12} />
            </button>
          </div>
        ),
      )}
      {editing === -1 ? (
        <PublicationForm
          start={blank()}
          done={(next) => {
            setEditing(null)
            if (next) set([...log, next])
          }}
        />
      ) : (
        editing === null && (
          <button type="button" className="work-link" onClick={() => setEditing(-1)}>
            + Log a publication
          </button>
        )
      )}
    </div>
  )
}

function PublicationForm({
  start,
  done,
}: {
  start: WorkPublished
  done: (p?: WorkPublished) => void
}) {
  const [p, setP] = useState(start)
  const ok = /^\d{4}-\d{2}-\d{2}/.test(p.at) && p.where.trim() !== ""
  return (
    <form
      className="work-pubform"
      onSubmit={(e) => {
        e.preventDefault()
        if (ok)
          done({
            at: p.at,
            where: p.where.trim(),
            url: p.url?.trim() || undefined,
            note: p.note?.trim() || undefined,
          })
      }}
      onKeyDown={(e) => e.key === "Escape" && done()}
    >
      <input
        type={/^\d{4}-\d{2}-\d{2}$/.test(p.at) ? "date" : "text"}
        value={p.at}
        onChange={(e) => setP({ ...p, at: e.target.value })}
        aria-label="When"
      />
      <input
        value={p.where}
        onChange={(e) => setP({ ...p, where: e.target.value })}
        placeholder="Where (YouTube, the blog…)"
        aria-label="Where"
      />
      <input
        value={p.url ?? ""}
        onChange={(e) => setP({ ...p, url: e.target.value })}
        placeholder="https://…"
        aria-label="Address"
      />
      <input
        value={p.note ?? ""}
        onChange={(e) => setP({ ...p, note: e.target.value })}
        placeholder="Note (optional)"
        aria-label="Note"
      />
      <div className="work-pubbtns">
        <button type="submit" className="desk-button" data-primary="true" disabled={!ok}>
          Keep
        </button>
        <button type="button" className="desk-button" onClick={() => done()}>
          Cancel
        </button>
      </div>
    </form>
  )
}

/** Relations an item can carry (contains is the outline: parent and children). */
const RELS = ["appears-in", "leads-to", "depends-on", "uses", "relates"]

/**
 * Links both ways: where the item sits (its parent, what it holds), its own typed links
 * (edited here), and what points at it: other items' links and their ref fields.
 */
function Links({
  d,
  items,
  kinds,
  open,
  set,
}: {
  d: WorkDetail
  items: WorkCard[]
  kinds: Map<string, WorkKind>
  open: (id: string) => void
  set: (links: WorkLink[]) => void
}) {
  const [rel, setRel] = useState(RELS[0])
  const byId = new Map(items.map((x) => [x.id, x]))
  const go = (id: string) => (
    <button type="button" className="work-link" onClick={() => open(id)} title={id}>
      {byId.get(id)?.title ?? id}
    </button>
  )
  // What points at this item: links from others, and their ref and refs fields.
  const incoming: { from: string; how: string }[] = []
  for (const x of items) {
    if (x.cut || x.id === d.id) continue
    for (const l of x.links) if (l.to === d.id) incoming.push({ from: x.id, how: l.rel })
    const xk = kinds.get(x.kind)
    for (const f of xk?.fields ?? []) {
      const v = x.fields[f.key]
      if (
        (f.type === "ref" && v === d.id) ||
        (f.type === "refs" && Array.isArray(v) && v.includes(d.id))
      )
        incoming.push({ from: x.id, how: (f.label || f.key).toLowerCase() })
    }
  }
  const candidates = items.filter(
    (x) => !x.cut && x.id !== d.id && !d.links.some((l) => l.rel === rel && l.to === x.id),
  )
  return (
    <div className="work-links">
      {d.parent && <div className="work-linkrow">in {go(d.parent)}</div>}
      {d.children.length > 0 && (
        <div className="work-linkrow">
          holds{" "}
          {d.children.map((c, i) => (
            <span key={c}>
              {i > 0 && ", "}
              {go(c)}
            </span>
          ))}
        </div>
      )}
      {d.links.map((l, i) => (
        <div key={`${l.rel}:${l.to}`} className="work-linkrow">
          <span className="work-dim">{l.rel}</span> {go(l.to)}
          <span className="work-spacer" />
          <button
            type="button"
            className="work-icon"
            title="Take this link out"
            onClick={() => set(d.links.filter((_, j) => j !== i))}
          >
            <X size={12} />
          </button>
        </div>
      ))}
      <div className="work-linkadd">
        <select
          className="work-select"
          value={rel}
          onChange={(e) => setRel(e.target.value)}
          aria-label="Relation"
        >
          {RELS.map((r) => (
            <option key={r} value={r}>
              {r}
            </option>
          ))}
        </select>
        <select
          className="work-select"
          value=""
          onChange={(e) => e.target.value && set([...d.links, { rel, to: e.target.value }])}
          aria-label="Link to"
        >
          <option value="">+ link to…</option>
          {candidates.map((x) => (
            <option key={x.id} value={x.id}>
              {kinds.get(x.kind)?.label ?? x.kind} · {x.title}
            </option>
          ))}
        </select>
      </div>
      {incoming.length > 0 && (
        <>
          <div className="work-dim work-linkhead">Pointing here</div>
          {incoming.map((x, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: one item may point here twice
            <div key={`${x.from}:${x.how}:${i}`} className="work-linkrow">
              {go(x.from)} <span className="work-dim">{x.how}</span>
            </div>
          ))}
        </>
      )}
    </div>
  )
}
