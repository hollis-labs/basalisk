/**
 * The agent's side of Story: it reads the scripts and writes draft notes. A draft note is
 * the owner's to send: the agent never sends a round, approves a scene or signs off.
 */
import { registerAgentTools } from "../../agent/tools"
import { story } from "../../api/story"
import { scriptMarkdown } from "./window"

const object = (properties: Record<string, unknown>, required: string[] = []) => ({
  type: "object",
  properties,
  required,
  additionalProperties: false,
})
const str = (description: string) => ({ type: "string", description })
const KEY = str('The script\'s key, "<project>.<script>", as story_list gives it.')

registerAgentTools([
  {
    name: "story_list",
    level: "read",
    description:
      "List the video scripts in Story: each one's key, title, stage (idea, outline, draft, review, revising, approved, production), scene count, length and how many draft notes wait to be sent.",
    input_schema: object({}),
    run: async () =>
      (await story.board()).scripts.map((c) => ({
        key: c.key,
        title: c.title,
        stage: c.stage,
        round: c.round,
        scenes: c.scenes,
        duration_s: c.duration_s,
        target_s: c.length_s,
        draft_notes: c.draft_notes,
        task: c.task,
      })),
  },
  {
    name: "story_get",
    level: "read",
    description:
      "Read a script: the whole text as Markdown in scene order (each scene's Visual, On-screen text, Narration and Notes), each scene's id, revision and status, the owner's draft notes not yet sent, and the last round's notes with the writing team's replies.",
    input_schema: object({ key: KEY }, ["key"]),
    run: async (input) => {
      const r = await story.script(String(input.key))
      const last = r.rounds?.[r.rounds.length - 1]
      return {
        key: r.script.key,
        title: r.script.title,
        stage: r.script.stage,
        scenes: r.script.scene_list.map((x) => ({
          id: x.id,
          title: x.title,
          rev: x.rev,
          status: x.status,
          duration_s: x.duration_s,
        })),
        script: scriptMarkdown(r.script),
        draft_notes: r.drafts.map((n) => ({
          id: n.id,
          scene: n.scene,
          kind: n.kind,
          section: n.section,
          quote: n.quote,
          replace: n.replace,
          text: n.text,
        })),
        last_round: last ? { round: last.round, sent_at: last.sent_at, notes: last.notes } : null,
        changed_since_round: r.since?.changed ?? [],
      }
    },
  },
  {
    name: "story_note",
    level: "write",
    description:
      "Add a draft note to a script, for the owner to look over and send with the next round: a comment on a scene (give scene; section narrows it to Visual, On-screen text, Narration or Notes) or on the whole script (no scene), or a suggestion (quote is the exact passage in that scene's section, replace is what to put instead). This only drafts: sending the round, approving and signing off are the owner's.",
    input_schema: object(
      {
        key: KEY,
        kind: { type: "string", enum: ["comment", "suggestion"] },
        scene: str("The scene's id (s01…). Leave out for a note on the whole script."),
        section: { type: "string", enum: ["Visual", "On-screen text", "Narration", "Notes"] },
        quote: str("For a suggestion: the exact passage to replace."),
        replace: str("For a suggestion: what to put instead."),
        text: str("The note."),
      },
      ["key", "kind"],
    ),
    describe: (input) =>
      `Draft a ${String(input.kind)} on ${String(input.key)}${input.scene ? ` (${String(input.scene)})` : ""}`,
    run: async (input) => {
      const key = String(input.key)
      const r = await story.script(key)
      const scene =
        typeof input.scene === "string" && input.scene
          ? r.script.scene_list.find((x) => x.id === input.scene)
          : undefined
      if (input.scene && !scene)
        throw new Error(`There is no scene ${String(input.scene)} in that script.`)
      if (input.kind === "suggestion") {
        if (
          !scene ||
          typeof input.section !== "string" ||
          typeof input.quote !== "string" ||
          typeof input.replace !== "string"
        )
          throw new Error("A suggestion needs scene, section, quote and replace.")
        if (!(scene.sections[input.section] ?? "").includes(input.quote))
          throw new Error(
            "That quote is not in that section of the scene as written. Copy it exactly.",
          )
      } else if (typeof input.text !== "string" || !input.text.trim()) {
        throw new Error("A comment needs text.")
      }
      const note = await story.addNote(key, {
        kind: String(input.kind),
        scene: scene?.id,
        scene_rev: scene?.rev,
        section: typeof input.section === "string" ? input.section : undefined,
        quote: typeof input.quote === "string" ? input.quote : undefined,
        replace: typeof input.replace === "string" ? input.replace : undefined,
        text: typeof input.text === "string" ? input.text : undefined,
      })
      return { drafted: note.id, note: "It is in the draft round; the owner sends it." }
    },
  },
])

registerAgentTools([
  {
    name: "story_create",
    level: "write",
    description: "Create a Story script, initially a draft for the owner and writing team.",
    input_schema: object(
      { project: str("Project slug"), script: str("Script slug"), title: str("Title") },
      ["project", "script", "title"],
    ),
    describe: (i) => `Create script “${String(i.title)}”`,
    run: async (i, _d, { signal }) => {
      signal.throwIfAborted()
      return story.create(String(i.project), String(i.script), String(i.title))
    },
  },
  {
    name: "story_scene_create",
    level: "write",
    description: "Add a scene to a script, optionally after an existing scene.",
    input_schema: object(
      { key: KEY, title: str("Scene title"), after: str("Scene id to follow") },
      ["key", "title"],
    ),
    describe: (i) => `Add scene “${String(i.title)}” to ${String(i.key)}`,
    run: async (i, _d, { signal }) => {
      signal.throwIfAborted()
      return story.addScene(String(i.key), {
        title: String(i.title),
        after: i.after as string | undefined,
      })
    },
  },
  {
    name: "story_scene_update",
    level: "write",
    description:
      "Edit a scene's title, duration or text by section (Visual, On-screen text, Narration, Notes). Requires the revision from story_get. Does not approve or sign off.",
    input_schema: object(
      {
        key: KEY,
        id: str("Scene id"),
        base_rev: { type: "integer", minimum: 1 },
        title: str("New title"),
        duration_s: { type: "number", minimum: 0 },
        sections: { type: "object", additionalProperties: { type: "string" } },
      },
      ["key", "id", "base_rev"],
    ),
    describe: (i) => `Edit scene ${String(i.id)} in ${String(i.key)}`,
    run: async (i, _d, { signal }) => {
      signal.throwIfAborted()
      return story.editScene(String(i.key), String(i.id), {
        base_rev: Number(i.base_rev),
        title: i.title as string | undefined,
        duration_s: i.duration_s as number | undefined,
        sections: i.sections as Record<string, string> | undefined,
      })
    },
  },
  {
    name: "story_stage",
    level: "write",
    description:
      "Change a script's drafting stage. Approval, sign-off and production remain the owner's actions.",
    input_schema: object(
      {
        key: KEY,
        stage: { type: "string", enum: ["idea", "outline", "draft", "review", "revising"] },
      },
      ["key", "stage"],
    ),
    describe: (i) => `Move script ${String(i.key)} to ${String(i.stage)}`,
    run: async (i, _d, { signal }) => {
      if (!["idea", "outline", "draft", "review", "revising"].includes(String(i.stage)))
        throw new Error("That stage is reserved for the owner.")
      signal.throwIfAborted()
      return story.stage(String(i.key), String(i.stage))
    },
  },
  {
    name: "story_scene_delete",
    level: "confirm",
    description: "Cut a scene from the script, retaining it for restoration. Always asks.",
    input_schema: object({ key: KEY, id: str("Scene id") }, ["key", "id"]),
    describe: (i) => `Cut scene ${String(i.id)} from ${String(i.key)}`,
    run: async (i, _d, { signal }) => {
      signal.throwIfAborted()
      return story.cut(String(i.key), String(i.id))
    },
  },
])
