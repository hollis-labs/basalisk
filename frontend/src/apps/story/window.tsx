/**
 * Story: video scripts and storyboards (docs/story.md). The Scripts board (scripts by
 * stage), a script opened as a storyboard of scene cards, and a scene's detail beside it.
 * The text lives in Caster ("story" namespace); every change here is written there at once.
 */
import { ArrowLeft, Copy, Plus, RotateCcw, Scissors } from "lucide-react"
import { type DragEvent, useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  clock,
  type StoryCard,
  type StoryNote,
  type StoryScene,
  type StoryScript,
  spoken,
  story,
} from "../../api/story"
import "./story.css"

const STAGE_LABEL: Record<string, string> = {
  idea: "Idea",
  outline: "Outline",
  draft: "Draft",
  review: "Review",
  revising: "Revising",
  approved: "Approved",
  production: "Production",
}

const message = (e: unknown) => (e instanceof Error ? e.message : String(e))

function ago(iso: string): string {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return ""
  const m = Math.round((Date.now() - t) / 60000)
  if (m < 1) return "just now"
  if (m < 60) return `${m}m ago`
  const h = Math.round(m / 60)
  return h < 48 ? `${h}h ago` : `${Math.round(h / 24)}d ago`
}

export function StoryWindow() {
  const [open, setOpen] = useState<string | null>(null)
  return open ? (
    <Storyboard scriptKey={open} back={() => setOpen(null)} />
  ) : (
    <Board onOpen={setOpen} />
  )
}

// ---- The Scripts board ----

function Board({ onOpen }: { onOpen: (key: string) => void }) {
  const [cards, setCards] = useState<StoryCard[]>([])
  const [stages, setStages] = useState<string[]>([])
  const [problem, setProblem] = useState("")
  const [error, setError] = useState("")
  const [project, setProject] = useState("")
  const [making, setMaking] = useState(false)
  const [loaded, setLoaded] = useState(false)

  const load = useCallback(async (refresh = false) => {
    try {
      const r = await story.board(refresh)
      setCards(r.scripts)
      setStages(r.stages)
      setProblem(r.problem ?? "")
      setError("")
    } catch (e) {
      setError(message(e))
    } finally {
      setLoaded(true)
    }
  }, [])
  useEffect(() => {
    void load(true)
    const t = window.setInterval(() => void load(), 25_000)
    return () => window.clearInterval(t)
  }, [load])

  const projects = useMemo(() => [...new Set(cards.map((c) => c.project))].sort(), [cards])
  const shown = cards.filter((c) => !project || c.project === project)
  const columns = [...stages]
  if (shown.some((c) => !stages.includes(c.stage))) columns.push("other")

  const drop = async (e: DragEvent, stage: string) => {
    e.preventDefault()
    const key = e.dataTransfer.getData("text/x-story-script")
    const c = cards.find((x) => x.key === key)
    if (!c || c.stage === stage || stage === "other") return
    if (stage === "approved") {
      setError("A script is approved by signing it off, not by moving it.")
      return
    }
    setCards((list) => list.map((x) => (x.key === key ? { ...x, stage } : x)))
    try {
      await story.stage(key, stage)
    } catch (err) {
      setError(message(err))
    }
    void load()
  }

  return (
    <div className="story">
      <header className="story-head">
        <span className="story-title">Scripts</span>
        <select
          className="story-select"
          value={project}
          onChange={(e) => setProject(e.target.value)}
          aria-label="Project"
        >
          <option value="">All projects</option>
          {projects.map((p) => (
            <option key={p} value={p}>
              {p}
            </option>
          ))}
        </select>
        <span className="story-spacer" />
        <button type="button" className="desk-button" onClick={() => setMaking((m) => !m)}>
          <Plus size={13} /> New script
        </button>
      </header>
      {making && (
        <NewScript
          projects={projects}
          done={(key) => {
            setMaking(false)
            if (key) onOpen(key)
          }}
        />
      )}
      {(error || problem) && (
        <p className="story-problem" role="status">
          {error || `Showing the copy kept here: ${problem}`}
        </p>
      )}
      {loaded && cards.length === 0 && !error && (
        <p className="story-empty">
          No scripts yet. A script is a manifest in Caster at{" "}
          <code>story/&lt;project&gt;.&lt;script&gt;</code> (docs/story.md), or start one with New
          script.
        </p>
      )}
      <div className="story-board">
        {columns.map((stage) => {
          const col = shown.filter((c) =>
            stage === "other" ? !stages.includes(c.stage) : c.stage === stage,
          )
          return (
            // biome-ignore lint/a11y/noStaticElementInteractions: a drop target for script cards
            <section
              key={stage}
              className="story-col"
              onDragOver={(e) => e.preventDefault()}
              onDrop={(e) => void drop(e, stage)}
            >
              <h3>
                {STAGE_LABEL[stage] ?? stage} <span>{col.length}</span>
              </h3>
              {col.map((c) => (
                <button
                  type="button"
                  key={c.key}
                  className="story-card"
                  draggable
                  onDragStart={(e) => e.dataTransfer.setData("text/x-story-script", c.key)}
                  onClick={() => onOpen(c.key)}
                  title={c.key}
                >
                  <span className="story-card-title">{c.title}</span>
                  <span className="story-card-meta">
                    {c.project} · {c.scenes} scene{c.scenes === 1 ? "" : "s"} ·{" "}
                    {clock(c.duration_s)}
                    {c.length_s ? ` / ${clock(c.length_s)}` : ""}
                  </span>
                  {c.problem ? (
                    <span className="story-card-problem">{c.problem}</span>
                  ) : (
                    <span className="story-card-meta">
                      {c.next && (
                        <b className="story-next" data-next={c.next}>
                          {c.next}
                        </b>
                      )}
                      {c.draft_notes > 0 &&
                        ` · ${c.draft_notes} note${c.draft_notes === 1 ? "" : "s"}`}
                      {` · ${ago(c.updated)}`}
                    </span>
                  )}
                </button>
              ))}
            </section>
          )
        })}
      </div>
    </div>
  )
}

function NewScript({ projects, done }: { projects: string[]; done: (key?: string) => void }) {
  const [project, setProject] = useState(projects[0] ?? "")
  const [script, setScript] = useState("")
  const [title, setTitle] = useState("")
  const [error, setError] = useState("")
  const slug = (s: string) =>
    s
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
  const save = async () => {
    try {
      const r = await story.create(slug(project), slug(script || title), title.trim() || script)
      done(r.key)
    } catch (e) {
      setError(message(e))
    }
  }
  return (
    <form
      className="story-new"
      onSubmit={(e) => {
        e.preventDefault()
        void save()
      }}
    >
      <input
        placeholder="Project (qspace)"
        value={project}
        onChange={(e) => setProject(e.target.value)}
        list="story-projects"
      />
      <datalist id="story-projects">
        {projects.map((p) => (
          <option key={p} value={p} />
        ))}
      </datalist>
      <input placeholder="Title" value={title} onChange={(e) => setTitle(e.target.value)} />
      <input
        placeholder={`Name (${slug(title) || "launch-teaser"})`}
        value={script}
        onChange={(e) => setScript(e.target.value)}
      />
      <button
        type="submit"
        className="desk-button"
        data-primary="true"
        disabled={!project.trim() || !(script.trim() || title.trim())}
      >
        Make it
      </button>
      <button type="button" className="desk-button" onClick={() => done()}>
        Cancel
      </button>
      {error && <span className="story-problem">{error}</span>}
    </form>
  )
}

// ---- The storyboard ----

function Storyboard({ scriptKey, back }: { scriptKey: string; back: () => void }) {
  const [s, setS] = useState<StoryScript | null>(null)
  const [drafts, setDrafts] = useState<StoryNote[]>([])
  const [problem, setProblem] = useState("")
  const [error, setError] = useState("")
  const [picked, setPicked] = useState<string | null>(null)
  // What the last save said; kept here so it outlives the pane, which is redrawn per revision.
  const [notice, setNotice] = useState("")
  // biome-ignore lint/correctness/useExhaustiveDependencies: cleared when another scene is picked
  useEffect(() => setNotice(""), [picked])
  const [dragging, setDragging] = useState<string | null>(null)
  const [over, setOver] = useState<number | null>(null)
  const editing = useRef(false)

  const load = useCallback(
    async (refresh = false) => {
      try {
        const r = await story.script(scriptKey, refresh)
        setS(r.script)
        setDrafts(r.drafts)
        setProblem(r.problem ?? "")
      } catch (e) {
        setError(message(e))
      }
    },
    [scriptKey],
  )
  useEffect(() => {
    void load(true)
    // While open, read back every 20 s (not while a section is being edited).
    const t = window.setInterval(() => {
      if (!editing.current) void load()
    }, 20_000)
    return () => window.clearInterval(t)
  }, [load])

  const act = async (fn: () => Promise<unknown>) => {
    setError("")
    try {
      await fn()
    } catch (e) {
      setError(message(e))
    }
    await load()
  }

  if (!s)
    return (
      <div className="story">
        <header className="story-head">
          <button type="button" className="story-icon" onClick={back} title="All scripts">
            <ArrowLeft size={15} />
          </button>
          <span className="story-title">{error || "…"}</span>
        </header>
      </div>
    )

  const ids = s.scene_list.map((x) => x.id)
  const words = s.scene_list.reduce((n, x) => n + x.words, 0)
  const drop = (at: number) => {
    if (!dragging) return
    const from = ids.indexOf(dragging)
    if (from < 0) return
    const next = ids.filter((x) => x !== dragging)
    next.splice(at > from ? at - 1 : at, 0, dragging)
    setDragging(null)
    setOver(null)
    if (next.join() === ids.join()) return
    // Show the new order at once; the manifest in Caster follows.
    setS({
      ...s,
      scene_list: next.map((id) => s.scene_list.find((x) => x.id === id) as StoryScene),
    })
    void act(() => story.order(s.key, next))
  }
  const scene = s.scene_list.find((x) => x.id === picked) ?? s.unused.find((x) => x.id === picked)

  return (
    <div className="story">
      <header className="story-head">
        <button type="button" className="story-icon" onClick={back} title="All scripts">
          <ArrowLeft size={15} />
        </button>
        <span className="story-title">{s.title}</span>
        <select
          className="story-select"
          value={s.stage}
          disabled={s.stage === "approved"}
          onChange={(e) => {
            const stage = e.target.value
            setS({ ...s, stage })
            void act(() => story.stage(s.key, stage))
          }}
          aria-label="Stage"
        >
          {Object.entries(STAGE_LABEL)
            .filter(([k]) => k !== "approved" || s.stage === "approved")
            .map(([k, v]) => (
              <option key={k} value={k}>
                {v}
              </option>
            ))}
        </select>
        <span
          className="story-totals"
          title="Stated scene durations, and the narration at 150 words a minute"
        >
          {clock(s.duration_s)}
          {s.length_s ? ` of ${clock(s.length_s)}` : ""} · spoken {clock(spoken(words))}
        </span>
        {s.draft_notes > 0 && (
          <span className="story-drafts">
            {s.draft_notes} draft note{s.draft_notes === 1 ? "" : "s"}
          </span>
        )}
        <span className="story-spacer" />
        <button
          type="button"
          className="desk-button"
          onClick={() => void act(async () => setPicked((await story.addScene(s.key, {})).id))}
        >
          <Plus size={13} /> Scene
        </button>
      </header>
      {(error || problem) && (
        <p className="story-problem" role="status">
          {error || `Showing the copy kept here: ${problem}`}
        </p>
      )}
      <div className="story-split">
        <div className="story-grid">
          {s.scene_list.map((x, i) => (
            // biome-ignore lint/a11y/noStaticElementInteractions: a card that is dragged to reorder
            <div
              key={x.id}
              className="story-scene"
              data-picked={picked === x.id}
              data-over={over === i}
              data-status={x.status}
              draggable
              onDragStart={(e) => {
                e.dataTransfer.setData("text/x-story-scene", x.id)
                setDragging(x.id)
              }}
              onDragEnd={() => {
                setDragging(null)
                setOver(null)
              }}
              onDragOver={(e) => {
                e.preventDefault()
                const r = e.currentTarget.getBoundingClientRect()
                setOver(e.clientX > r.left + r.width / 2 ? i + 1 : i)
              }}
              onDrop={(e) => {
                e.preventDefault()
                const r = e.currentTarget.getBoundingClientRect()
                drop(e.clientX > r.left + r.width / 2 ? i + 1 : i)
              }}
              onClick={() => setPicked(x.id)}
              onKeyDown={(e) => {
                if (e.key === "Enter") setPicked(x.id)
              }}
            >
              <div className="story-scene-top">
                <span className="story-num">{i + 1}</span>
                <span className="story-dot" data-status={x.status} title={x.status} />
                <span className="story-scene-title">{x.title}</span>
                <span className="story-scene-dur">{clock(x.duration_s)}</span>
              </div>
              <div className="story-frame">
                {x.frames[0] ? (
                  <span title={x.frames[0].ref}>{x.frames[0].caption || "frame"}</span>
                ) : (
                  <span className="story-frame-text">
                    {x.sections.Visual?.split("\n")[0] || "No frame"}
                  </span>
                )}
              </div>
              <div className="story-narr">
                {x.missing
                  ? "This scene is listed but not in Caster."
                  : x.sections.Narration?.split("\n")[0] || "—"}
              </div>
              <div className="story-scene-foot">
                {x.notes > 0 && (
                  <span>
                    {x.notes} note{x.notes === 1 ? "" : "s"}
                  </span>
                )}
                <span className="story-spacer" />
                <button
                  type="button"
                  className="story-icon"
                  title="Duplicate"
                  onClick={(e) => {
                    e.stopPropagation()
                    void act(async () =>
                      setPicked((await story.addScene(s.key, { from: x.id, after: x.id })).id),
                    )
                  }}
                >
                  <Copy size={12} />
                </button>
                <button
                  type="button"
                  className="story-icon"
                  title="Cut from the storyboard (it stays in Caster; restore below)"
                  onClick={(e) => {
                    e.stopPropagation()
                    void act(() => story.cut(s.key, x.id))
                  }}
                >
                  <Scissors size={12} />
                </button>
              </div>
            </div>
          ))}
          {/* Dropping after the last card. */}
          {/* biome-ignore lint/a11y/noStaticElementInteractions: the drop target at the end */}
          <div
            className="story-scene story-scene-end"
            data-over={over === ids.length}
            onDragOver={(e) => {
              e.preventDefault()
              setOver(ids.length)
            }}
            onDrop={(e) => {
              e.preventDefault()
              drop(ids.length)
            }}
          >
            <button
              type="button"
              className="desk-button"
              onClick={() => void act(async () => setPicked((await story.addScene(s.key, {})).id))}
            >
              <Plus size={13} /> Add a scene
            </button>
          </div>
          {s.unused.length > 0 && (
            <div className="story-unused">
              <h4>Cut scenes</h4>
              {s.unused.map((x) => (
                <div key={x.id} className="story-unused-row">
                  <button type="button" className="story-link" onClick={() => setPicked(x.id)}>
                    {x.id} · {x.title}
                  </button>
                  <button
                    type="button"
                    className="story-icon"
                    title="Put it back at the end"
                    onClick={() => void act(() => story.restore(s.key, x.id))}
                  >
                    <RotateCcw size={12} />
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>
        {scene && (
          <SceneDetail
            key={`${scene.id}@${scene.rev}`}
            scriptKey={s.key}
            scene={scene}
            notes={drafts.filter((n) => n.scene === scene.id)}
            close={() => setPicked(null)}
            status={notice}
            setStatus={setNotice}
            editing={(on) => {
              editing.current = on
            }}
            saved={() => void load()}
          />
        )}
      </div>
    </div>
  )
}

// ---- A scene, beside the board ----

const SECTIONS = ["Visual", "On-screen text", "Narration", "Notes"]

function SceneDetail({
  scriptKey,
  scene,
  notes,
  close,
  editing,
  saved,
  status,
  setStatus,
}: {
  scriptKey: string
  scene: StoryScene
  notes: StoryNote[]
  close: () => void
  status: string
  setStatus: (s: string) => void
  editing: (on: boolean) => void
  saved: () => void
}) {
  const [title, setTitle] = useState(scene.title)
  const [duration, setDuration] = useState(String(scene.duration_s || ""))
  const [edit, setEdit] = useState<{ section: string; text: string } | null>(null)
  const base = scene.rev

  const save = async (change: {
    title?: string
    duration_s?: number
    sections?: Record<string, string>
  }) => {
    setStatus("Saving…")
    try {
      const r = await story.editScene(scriptKey, scene.id, { base_rev: base, ...change })
      setStatus(`Saved as revision ${r.rev}.`)
    } catch (e) {
      // 409: the team changed it meanwhile; the owner's words became suggestions.
      setStatus(message(e))
    }
    editing(false)
    saved()
  }

  return (
    <aside className="story-detail">
      <header>
        <input
          className="story-detail-title"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          onBlur={() => title.trim() && title !== scene.title && void save({ title })}
          aria-label="Scene title"
        />
        <button type="button" className="story-icon" onClick={close} title="Close">
          ×
        </button>
      </header>
      <div className="story-detail-meta">
        <span>
          {scene.id} · revision {scene.rev} · {scene.status}
        </span>
        <label>
          <input
            className="story-dur"
            value={duration}
            onChange={(e) => setDuration(e.target.value)}
            onBlur={() => {
              const v = Number(duration)
              if (duration.trim() && !Number.isNaN(v) && v !== scene.duration_s)
                void save({ duration_s: v })
            }}
            inputMode="decimal"
            aria-label="Duration in seconds"
          />
          s
        </label>
        <span>spoken {clock(spoken(scene.words))}</span>
      </div>
      {scene.problem && <p className="story-problem">{scene.problem}</p>}
      {scene.frames.length > 0 && (
        <div className="story-frames">
          {scene.frames.map((f) => (
            <span key={f.ref} className="story-frame-chip" title={f.ref}>
              {f.caption || f.ref}
            </span>
          ))}
        </div>
      )}
      {SECTIONS.map((sec) => (
        <section key={sec} className="story-sec">
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
                if (text.trim() !== (scene.sections[sec] ?? "").trim())
                  void save({ sections: { [sec]: text } })
                else editing(false)
              }}
              rows={Math.max(3, edit.text.split("\n").length + 1)}
            />
          ) : (
            // biome-ignore lint/a11y/noStaticElementInteractions: click to edit in place
            <div
              className="story-sec-text"
              data-empty={!scene.sections[sec]}
              onClick={() => {
                editing(true)
                setEdit({ section: sec, text: scene.sections[sec] ?? "" })
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  editing(true)
                  setEdit({ section: sec, text: scene.sections[sec] ?? "" })
                }
              }}
            >
              {scene.sections[sec] || "Click to write"}
            </div>
          )}
        </section>
      ))}
      {status && <p className="story-status">{status}</p>}
      {notes.length > 0 && (
        <section className="story-sec">
          <h4>Draft notes</h4>
          {notes.map((n) => (
            <div key={n.id} className="story-note">
              <b>{n.kind}</b>
              {n.section ? ` · ${n.section}` : ""}: {n.text}
              {n.replace && <div className="story-note-replace">→ {n.replace}</div>}
            </div>
          ))}
        </section>
      )}
    </aside>
  )
}
