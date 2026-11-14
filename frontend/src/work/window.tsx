/**
 * The Work engine's window (docs/work.md): a list of the app's workspaces, a workspace opened
 * on one of its views, and an item's detail beside it. Content, Planner and Writer are this
 * window with their preset. The items live in Caster; every change is written there at once.
 */
import { ArrowLeft, Plus } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { type WorkPreset, type WorkSpace, type Workspace, work } from "../api/work"
import { Board } from "./board"
import { Detail } from "./detail"
import { ago, message, slug } from "./util"
import "./work.css"

export function WorkWindow({ app, appTitle }: { app: string; appTitle: string }) {
  const [open, setOpen] = useState<string | null>(null)
  return open ? (
    <SpaceView ws={open} back={() => setOpen(null)} />
  ) : (
    <Workspaces app={app} appTitle={appTitle} onOpen={setOpen} />
  )
}

// ---- The app's workspaces ----

function Workspaces({
  app,
  appTitle,
  onOpen,
}: {
  app: string
  appTitle: string
  onOpen: (ws: string) => void
}) {
  const [list, setList] = useState<Workspace[]>([])
  const [presets, setPresets] = useState<WorkPreset[]>([])
  const [problem, setProblem] = useState("")
  const [error, setError] = useState("")
  const [loaded, setLoaded] = useState(false)
  const [making, setMaking] = useState(false)

  const load = useCallback(
    async (refresh = false) => {
      try {
        const r = await work.list(app, refresh)
        setList(r.workspaces)
        setPresets(r.presets.filter((p) => p.app === app))
        setProblem(r.problem ?? "")
        setError("")
      } catch (e) {
        setError(message(e))
      } finally {
        setLoaded(true)
      }
    },
    [app],
  )
  useEffect(() => {
    void load(true)
    const t = window.setInterval(() => void load(), 25_000)
    return () => window.clearInterval(t)
  }, [load])

  return (
    <div className="work">
      <header className="work-head">
        <span className="work-title">{appTitle}</span>
        <span className="work-spacer" />
        <button type="button" className="desk-button" onClick={() => setMaking((m) => !m)}>
          <Plus size={13} /> New workspace
        </button>
      </header>
      {making && (
        <NewWorkspace
          presets={presets}
          done={(ws) => {
            setMaking(false)
            if (ws) onOpen(ws)
          }}
        />
      )}
      {(error || problem) && (
        <p className="work-problem" role="status">
          {error || `Showing the copy kept here: ${problem}`}
        </p>
      )}
      {loaded && list.length === 0 && !error && (
        <p className="work-empty">
          No workspaces yet. A workspace is one body of work: a channel, a book, a launch. Make one
          with New workspace; it is kept in Caster (docs/work.md).
        </p>
      )}
      <div className="work-spaces">
        {list.map((w) => (
          <button
            type="button"
            key={w.slug}
            className="work-space-card"
            onClick={() => onOpen(w.slug)}
            title={`caster://work/${w.slug}`}
          >
            <span className="work-card-title">{w.title}</span>
            <span className="work-card-meta">
              {w.items} item{w.items === 1 ? "" : "s"} · {ago(w.updated)}
            </span>
            {w.problem ? (
              <span className="work-problem-inline">{w.problem}</span>
            ) : (
              w.body.trim() && (
                <span className="work-card-text">{w.body.trim().split("\n")[0]}</span>
              )
            )}
          </button>
        ))}
      </div>
    </div>
  )
}

function NewWorkspace({ presets, done }: { presets: WorkPreset[]; done: (ws?: string) => void }) {
  const [title, setTitle] = useState("")
  const [name, setName] = useState("")
  const [about, setAbout] = useState("")
  const [preset, setPreset] = useState(presets[0]?.id ?? "")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (!preset && presets[0]) setPreset(presets[0].id)
  }, [preset, presets])
  const save = async () => {
    setBusy(true)
    try {
      const r = await work.create(slug(name || title), title.trim(), preset, about)
      done(r.workspace)
    } catch (e) {
      setError(message(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <form
      className="work-new"
      onSubmit={(e) => {
        e.preventDefault()
        void save()
      }}
    >
      <input
        // biome-ignore lint/a11y/noAutofocus: the owner just asked for a new workspace
        autoFocus
        placeholder="Title"
        value={title}
        onChange={(e) => setTitle(e.target.value)}
      />
      <input
        placeholder={`Name (${slug(title) || "my-channel"})`}
        value={name}
        onChange={(e) => setName(e.target.value)}
      />
      {presets.length > 1 && (
        <select
          className="work-select"
          value={preset}
          onChange={(e) => setPreset(e.target.value)}
          aria-label="Start from"
        >
          {presets.map((p) => (
            <option key={p.id} value={p.id}>
              {p.label}
            </option>
          ))}
        </select>
      )}
      <input
        className="work-new-about"
        placeholder="What it is (optional)"
        value={about}
        onChange={(e) => setAbout(e.target.value)}
      />
      <button
        type="submit"
        className="desk-button"
        data-primary="true"
        disabled={busy || !preset || !(name.trim() || title.trim())}
      >
        Make it
      </button>
      <button type="button" className="desk-button" onClick={() => done()}>
        Cancel
      </button>
      {error && <span className="work-problem-inline">{error}</span>}
    </form>
  )
}

// ---- A workspace opened ----

function SpaceView({ ws, back }: { ws: string; back: () => void }) {
  const [sp, setSp] = useState<WorkSpace | null>(null)
  const [problem, setProblem] = useState("")
  const [error, setError] = useState("")
  const [picked, setPicked] = useState<string | null>(null)
  const [viewId, setViewId] = useState<string | null>(null)
  const [showCut, setShowCut] = useState(false)
  const editing = useRef(false)

  const load = useCallback(
    async (refresh = false) => {
      try {
        const r = await work.open(ws, refresh)
        setSp(r.space)
        setProblem(r.problem ?? "")
        setError("")
      } catch (e) {
        setError(message(e))
      }
    },
    [ws],
  )
  useEffect(() => {
    void load(true)
    // While open, read back every 20 s (not while a section is being edited).
    const t = window.setInterval(() => {
      if (!editing.current) void load()
    }, 20_000)
    return () => window.clearInterval(t)
  }, [load])

  const kinds = useMemo(() => new Map((sp?.kinds ?? []).map((k) => [k.kind, k])), [sp])
  const view = sp?.views.find((v) => v.id === viewId) ?? sp?.views[0] ?? null

  if (!sp)
    return (
      <div className="work">
        <header className="work-head">
          <button type="button" className="work-icon" onClick={back} title="All workspaces">
            <ArrowLeft size={15} />
          </button>
          <span className="work-title">{error || "…"}</span>
        </header>
      </div>
    )

  const cut = sp.items.filter((x) => x.cut)
  return (
    <div className="work">
      <header className="work-head">
        <button type="button" className="work-icon" onClick={back} title="All workspaces">
          <ArrowLeft size={15} />
        </button>
        <span className="work-title" title={`caster://work/${sp.workspace.slug}`}>
          {sp.workspace.title}
        </span>
        <nav className="work-tabs">
          {sp.views.map((v) => (
            <button
              type="button"
              key={v.id}
              data-on={v.id === view?.id}
              onClick={() => setViewId(v.id)}
            >
              {v.title || v.view}
            </button>
          ))}
        </nav>
        <span className="work-spacer" />
        {cut.length > 0 && (
          <button
            type="button"
            className="work-link"
            onClick={() => setShowCut((c) => !c)}
            title="Items taken out of the views; they stay in Caster"
          >
            {cut.length} cut
          </button>
        )}
      </header>
      {(error || problem) && (
        <p className="work-problem" role="status">
          {error || `Showing the copy kept here: ${problem}`}
        </p>
      )}
      <div className="work-split">
        <div className="work-main">
          {showCut && (
            <div className="work-cutlist">
              {cut.map((x) => (
                <div key={x.id} className="work-cutrow">
                  <button type="button" className="work-link" onClick={() => setPicked(x.id)}>
                    {kinds.get(x.kind)?.label ?? x.kind} · {x.title}
                  </button>
                  <button
                    type="button"
                    className="desk-button"
                    onClick={async () => {
                      try {
                        await work.restore(ws, x.id)
                      } catch (e) {
                        setError(message(e))
                      }
                      void load()
                    }}
                  >
                    Restore
                  </button>
                </div>
              ))}
            </div>
          )}
          {view?.view === "board" || !view ? (
            <Board
              ws={ws}
              space={sp}
              view={view}
              picked={picked}
              pick={setPicked}
              changed={() => void load()}
              setError={setError}
            />
          ) : (
            <p className="work-empty">The {view.view} view comes in a later step.</p>
          )}
        </div>
        {picked && (
          <Detail
            key={picked}
            ws={ws}
            id={picked}
            kinds={kinds}
            items={sp.items}
            revOnBoard={sp.items.find((x) => x.id === picked)?.rev ?? 0}
            close={() => setPicked(null)}
            editing={(on) => {
              editing.current = on
            }}
            changed={() => void load()}
          />
        )}
      </div>
    </div>
  )
}
