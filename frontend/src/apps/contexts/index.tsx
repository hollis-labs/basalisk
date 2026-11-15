/**
 * Contexts: the assistant's contexts, for the owner to shape (docs/assistant.md). The kinds
 * of work are fixed; each takes the owner's standing instructions. Projects are the
 * owner's: what selects one, where its state lives, their instructions for it. The roster
 * is the few seats the assistant writes to, and what each is leading.
 */
import { FolderKanban, Plus, Trash2 } from "lucide-react"
import { useCallback, useEffect, useState } from "react"
import { request } from "../../api/account"
import { useDesk } from "../../desktop/context"
import { registerApp } from "../../desktop/registry"
import "./contexts.css"

interface Slot {
  name: string
  about: string
  note_written?: string
  instructions?: string
}
interface Project {
  name: string
  about?: string
  match?: string[]
  torque?: string
  tesseract?: string
  workspace?: string
  repo?: string
  where?: string
  instructions?: string
  note?: string
  note_written?: string
}

interface Seat {
  key: string
  name: string
  address: string
  runs?: string
  leads: string
  roster?: string
}
type Pick = { kind: "slot" | "project" | "seat"; name: string }

function ContextsWindow() {
  const desk = useDesk()
  const [slots, setSlots] = useState<Slot[]>([])
  const [projects, setProjects] = useState<Project[]>([])
  const [roster, setRoster] = useState<Seat[]>([])
  const [pick, setPick] = useState<Pick | null>(null)
  const [draft, setDraft] = useState<Project | null>(null)
  const [seat, setSeat] = useState<Seat | null>(null)
  const [rules, setRules] = useState("")
  const [said, setSaid] = useState("")

  const load = useCallback(async () => {
    const r = await request<{ contexts: Slot[]; projects?: Project[]; roster?: Seat[] }>(
      "agent/contexts",
    ).catch(() => null)
    if (!r) return
    setSlots(r.contexts ?? [])
    setProjects(r.projects ?? [])
    setRoster(r.roster ?? [])
  }, [])
  useEffect(() => {
    void load()
  }, [load])

  const open = (kind: Pick["kind"], name: string) => {
    setPick({ kind, name })
    setSaid("")
    if (kind === "slot") setRules(slots.find((s) => s.name === name)?.instructions ?? "")
    else if (kind === "project")
      setDraft({ ...(projects.find((p) => p.name === name) ?? { name }) })
    else
      setSeat({
        ...(roster.find((s) => s.key === name) ?? {
          key: "",
          name: "",
          address: "",
          runs: "tether",
          leads: "",
        }),
      })
  }
  const fail = (e: unknown) => setSaid(e instanceof Error ? e.message : "That did not work.")
  const saveRules = async () => {
    if (!pick) return
    await request(`agent/contexts/${pick.name}/instructions`, "PUT", { text: rules }).then(
      () => setSaid("Saved. The assistant is told the next time you are in this context."),
      fail,
    )
    await load()
  }
  const saveProject = async () => {
    if (!draft) return
    const name = draft.name.trim().toLowerCase()
    await request(`agent/projects/${encodeURIComponent(name)}`, "PUT", { ...draft, name }).then(
      () => {
        setSaid("Saved.")
        setPick({ kind: "project", name })
      },
      fail,
    )
    await load()
  }
  const saveSeat = async () => {
    if (!seat) return
    const key =
      seat.key ||
      seat.name
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, "-")
        .replace(/^-+|-+$/g, "")
    if (!key) return setSaid("Give the seat a name.")
    await request(`agent/roster/${encodeURIComponent(key)}`, "PUT", { ...seat, key }).then(() => {
      setSaid("Saved. The assistant is told with your next message.")
      setPick({ kind: "seat", name: key })
      setSeat({ ...seat, key })
    }, fail)
    await load()
  }
  const dropSeat = async () => {
    if (!seat) return
    const yes = await desk.confirm({
      title: `Take “${seat.name}” off the roster?`,
      message:
        "The assistant stops going to this seat first. Nothing about the seat itself changes.",
      confirmLabel: "Remove",
      danger: true,
    })
    if (!yes) return
    await request(`agent/roster/${encodeURIComponent(seat.key)}`, "DELETE").catch(fail)
    setPick(null)
    setSeat(null)
    await load()
  }
  const seatField = (key: keyof Seat, label: string, hint: string, long = false) =>
    seat && (
      // biome-ignore lint/a11y/noLabelWithoutControl: the control is the input or textarea just below
      <label>
        <span>{label}</span>
        {long ? (
          <textarea
            rows={5}
            value={seat[key] ?? ""}
            placeholder={hint}
            onChange={(e) => setSeat({ ...seat, [key]: e.target.value })}
          />
        ) : (
          <input
            value={seat[key] ?? ""}
            placeholder={hint}
            spellCheck={false}
            onChange={(e) => setSeat({ ...seat, [key]: e.target.value })}
          />
        )}
      </label>
    )
  const newSeat = pick?.kind === "seat" && !roster.some((s) => s.key === pick.name)
  const drop = async () => {
    if (!draft) return
    const yes = await desk.confirm({
      title: `Remove the project “${draft.name}”?`,
      message:
        "The assistant's note for it goes too. Nothing in Torque, Tesseract or your files is touched.",
      confirmLabel: "Remove",
      danger: true,
    })
    if (!yes) return
    await request(`agent/projects/${encodeURIComponent(draft.name)}`, "DELETE").catch(fail)
    setPick(null)
    setDraft(null)
    await load()
  }
  const field = (key: keyof Project, label: string, hint: string, long = false) =>
    draft && (
      // biome-ignore lint/a11y/noLabelWithoutControl: the control is the input or textarea just below
      <label>
        <span>{label}</span>
        {long ? (
          <textarea
            rows={5}
            value={(draft[key] as string) ?? ""}
            placeholder={hint}
            onChange={(e) => setDraft({ ...draft, [key]: e.target.value })}
          />
        ) : (
          <input
            value={(draft[key] as string) ?? ""}
            placeholder={hint}
            spellCheck={false}
            onChange={(e) => setDraft({ ...draft, [key]: e.target.value })}
          />
        )}
      </label>
    )
  const slot = pick?.kind === "slot" ? slots.find((s) => s.name === pick.name) : undefined
  const isNew = pick?.kind === "project" && !projects.some((p) => p.name === pick.name)

  return (
    <div className="ctx">
      <nav>
        <h3>Kinds of work</h3>
        {slots.map((s) => (
          <button
            key={s.name}
            type="button"
            data-on={pick?.kind === "slot" && pick.name === s.name}
            onClick={() => open("slot", s.name)}
          >
            <span>{s.name}</span>
            {s.instructions && <em>rules</em>}
          </button>
        ))}
        <h3>
          Projects
          <button
            type="button"
            title="New project"
            aria-label="New project"
            onClick={() => open("project", "")}
          >
            <Plus size={13} aria-hidden="true" />
          </button>
        </h3>
        {projects.map((p) => (
          <button
            key={p.name}
            type="button"
            data-on={pick?.kind === "project" && pick.name === p.name}
            onClick={() => open("project", p.name)}
          >
            <span>{p.name}</span>
            {p.instructions && <em>rules</em>}
          </button>
        ))}
        {!projects.length && <p>No projects yet. + adds one, or tell the assistant to make one.</p>}
        <h3>
          Roster
          <button
            type="button"
            title="New seat"
            aria-label="New seat"
            onClick={() => open("seat", "")}
          >
            <Plus size={13} aria-hidden="true" />
          </button>
        </h3>
        {roster.map((s) => (
          <button
            key={s.key}
            type="button"
            data-on={pick?.kind === "seat" && pick.name === s.key}
            onClick={() => open("seat", s.key)}
          >
            <span>{s.name}</span>
            <em>{s.roster || s.runs || ""}</em>
          </button>
        ))}
        {!roster.length && (
          <p>No seats yet. + adds one: who the assistant writes to, and for what.</p>
        )}
      </nav>
      <section>
        {!pick && (
          <p className="ctx-intro">
            The assistant follows you between kinds of work and between projects. A kind of work is
            chosen by the window in front; a project by words in that window (a folder, a repo, a
            workspace) or by you, from the chip in the assistant's header. Each can carry your
            standing instructions, and the assistant keeps its own note for each. The roster is the
            few seats the assistant writes to on your behalf, with what each is leading; they pass
            work on to their own teams from there.
          </p>
        )}
        {pick?.kind === "seat" && seat && (
          <>
            <h2>{newSeat ? "New seat" : seat.name}</h2>
            {seatField("name", "Name", "Task Ops Manager")}
            {seatField("address", "Address", "msg://agent/…")}
            {seatField(
              "leads",
              "What it is managing, responsible for or leading now",
              "The assistant picks who to write to from this. Say what goes to this seat, and what does not.",
              true,
            )}
            <div className="ctx-two">
              <label>
                <span>Runs</span>
                <select
                  value={seat.runs ?? ""}
                  onChange={(e) => setSeat({ ...seat, runs: e.target.value })}
                >
                  <option value="tether">Tether (a seat on the agent system)</option>
                  <option value="local">Local (a session on this machine)</option>
                  <option value="">Not said</option>
                </select>
              </label>
              {seatField("roster", "Roster", "standard, or a workspace: personal, hollis-labs")}
            </div>
            <footer>
              {!newSeat && (
                <button type="button" className="ctx-drop" onClick={() => void dropSeat()}>
                  <Trash2 size={13} aria-hidden="true" /> Remove
                </button>
              )}
              <span>{said}</span>
              <button type="button" className="ctx-save" onClick={() => void saveSeat()}>
                Save
              </button>
            </footer>
          </>
        )}
        {slot && (
          <>
            <h2>{slot.name}</h2>
            <p className="ctx-about">{slot.about}</p>
            <label>
              <span>Your standing instructions for this kind of work</span>
              <textarea
                rows={9}
                value={rules}
                placeholder="For example: when I am writing, suggest, never rewrite my prose."
                onChange={(e) => setRules(e.target.value)}
              />
            </label>
            <footer>
              <span>{said}</span>
              <button type="button" className="ctx-save" onClick={() => void saveRules()}>
                Save
              </button>
            </footer>
          </>
        )}
        {pick?.kind === "project" && draft && (
          <>
            <h2>{isNew ? "New project" : draft.name}</h2>
            {isNew && field("name", "Name", "lowercase letters, digits and dashes")}
            {field("about", "What it is", "One line")}
            <label>
              <span>
                Words that select it (in the window's title, path or address), separated by commas
              </span>
              <input
                value={(draft.match ?? []).join(", ")}
                placeholder="apps/basalisk, basalisk-wt"
                spellCheck={false}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    match: e.target.value
                      .split(",")
                      .map((m) => m.trim())
                      .filter(Boolean),
                  })
                }
              />
            </label>
            <div className="ctx-two">
              {field("torque", "Torque project", "PRJ-…")}
              {field("tesseract", "Tesseract namespace", "project/…")}
              {field("workspace", "Workspace (Planner, Content, Writer)", "slug")}
              {field("repo", "Repository", "owner/name or a path")}
            </div>
            {field("where", "Anything else worth knowing where to look", "")}
            {field(
              "instructions",
              "Your standing instructions for this project",
              "For example: never push without the public-repo scan.",
              true,
            )}
            {draft.note && (
              <div className="ctx-note">
                <strong>The assistant's note · {draft.note_written}</strong>
                {draft.note}
              </div>
            )}
            <footer>
              {!isNew && (
                <button type="button" className="ctx-drop" onClick={() => void drop()}>
                  <Trash2 size={13} aria-hidden="true" /> Remove
                </button>
              )}
              <span>{said}</span>
              <button type="button" className="ctx-save" onClick={() => void saveProject()}>
                Save
              </button>
            </footer>
          </>
        )}
      </section>
    </div>
  )
}

registerApp({
  id: "contexts",
  title: "Contexts",
  icon: FolderKanban,
  menu: true,
  size: [860, 640],
  bare: true,
  render: () => <ContextsWindow />,
})
