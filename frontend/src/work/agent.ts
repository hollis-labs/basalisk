/**
 * The agent's side of Work (Content, Planner, Writer): it reads workspaces and items and
 * writes draft notes. A draft note is the owner's to send: the agent never sends a round,
 * signs off or publishes.
 */
import { registerAgentTools } from "../agent/tools"
import { work } from "../api/work"

const object = (properties: Record<string, unknown>, required: string[] = []) => ({
  type: "object",
  properties,
  required,
  additionalProperties: false,
})
const str = (description: string) => ({ type: "string", description })
const WS = str("The workspace's slug, as work_list gives it.")
const ID = str("The item's id (8 letters and digits), as work_list gives it.")

registerAgentTools([
  {
    name: "work_list",
    level: "read",
    description:
      "List Work: without workspace, the workspaces in Content (articles, videos, posts), Planner (projects, features, tasks) and Writer (books, chapters, settings), each with its slug, app, title and item count. With workspace, its items: id, kind, title, stage, parent, a first line, and whether it is signed off. kind and stage narrow the items.",
    input_schema: object({
      workspace: WS,
      app: { type: "string", enum: ["content", "planner", "writer"] },
      kind: str("Only items of this kind (video, chapter, task…)."),
      stage: str("Only items at this stage."),
    }),
    run: async (input) => {
      if (typeof input.workspace !== "string" || !input.workspace) {
        const app = typeof input.app === "string" ? input.app : ""
        return (await work.list(app)).workspaces.map((w) => ({
          workspace: w.slug,
          app: w.app,
          title: w.title,
          items: w.items,
        }))
      }
      const r = await work.open(input.workspace)
      return {
        workspace: r.space.workspace.slug,
        title: r.space.workspace.title,
        app: r.space.workspace.app,
        kinds: r.space.kinds.map((k) => ({ kind: k.kind, label: k.label, stages: k.stages })),
        items: r.space.items
          .filter((c) => !c.cut)
          .filter((c) => !input.kind || c.kind === input.kind)
          .filter((c) => !input.stage || c.stage === input.stage)
          .map((c) => ({
            id: c.id,
            kind: c.kind,
            title: c.title,
            stage: c.stage,
            parent: c.parent || undefined,
            excerpt: c.excerpt,
            words: c.words,
            signed_off: c.signed ? (c.stale_sign ? "changed since" : true) : undefined,
          })),
      }
    },
  },
  {
    name: "work_get",
    level: "read",
    description:
      "Read a Work item: its kind, stage, fields, tags and links, the whole text as Markdown under its section headings, the owner's draft notes not yet sent, the last round's notes with the team's replies, and its sign-off (and whether its text has changed since).",
    input_schema: object({ workspace: WS, id: ID }, ["workspace", "id"]),
    run: async (input) => {
      const d = await work.item(String(input.workspace), String(input.id))
      const text = [
        d.lead ?? "",
        ...d.order.map((s) => `## ${s}\n\n${(d.sections[s] ?? "").trim()}`),
      ]
        .filter(Boolean)
        .join("\n\n")
      const last = d.rounds[0]
      return {
        id: d.id,
        ref: d.ref,
        kind: d.kind,
        title: d.title,
        stage: d.stage,
        rev: d.rev,
        fields: d.fields,
        tags: d.tags,
        links: d.links,
        parent: d.parent || undefined,
        children: d.children,
        sections: d.order,
        text,
        draft_notes: d.notes.map((n) => ({
          id: n.id,
          kind: n.kind,
          section: n.section,
          quote: n.quote,
          replace: n.replace,
          text: n.text,
        })),
        last_round: last
          ? { round: last.round, sent_at: last.sent_at, open: last.open, notes: last.notes }
          : null,
        changed_since_round: d.changed_since_round,
        signed_off: d.sign_off
          ? { at: d.sign_off.at, rev: d.sign_off.rev, changed_since: !!d.stale_sign }
          : null,
      }
    },
  },
  {
    name: "work_note",
    level: "write",
    description:
      "Add a draft note to a Work item, for the owner to look over and send with the next round: a comment (on the whole item, or on one section: give section), or a suggestion (section, quote: the exact passage in that section as it is now, replace: what to put instead). This only drafts: sending the round, signing off and publishing are the owner's.",
    input_schema: object(
      {
        workspace: WS,
        id: ID,
        kind: { type: "string", enum: ["comment", "suggestion"] },
        section: str(
          "The section's heading, as work_get lists them. Leave out for a comment on the whole item.",
        ),
        quote: str("For a suggestion: the exact passage to replace."),
        replace: str("For a suggestion: what to put instead."),
        text: str("The note (for a suggestion, why; optional)."),
      },
      ["workspace", "id", "kind"],
    ),
    describe: (input) =>
      `Draft a ${String(input.kind)} on ${String(input.workspace)}/${String(input.id)}${input.section ? ` (${String(input.section)})` : ""}`,
    run: async (input) => {
      const s = (v: unknown) => (typeof v === "string" && v ? v : undefined)
      if (
        input.kind === "suggestion" &&
        (!s(input.section) || !s(input.quote) || typeof input.replace !== "string")
      )
        throw new Error("A suggestion needs section, quote and replace.")
      if (input.kind === "comment" && !s(input.text)?.trim())
        throw new Error("A comment needs text.")
      const note = await work.addNote(String(input.workspace), String(input.id), {
        kind: String(input.kind),
        section: s(input.section),
        quote: s(input.quote),
        replace: typeof input.replace === "string" ? input.replace : undefined,
        text: s(input.text),
      })
      return { drafted: note.id, note: "It is in the item's draft round; the owner sends it." }
    },
  },
])
