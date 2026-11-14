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
