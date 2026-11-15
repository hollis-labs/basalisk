/**
 * The agent's side of Work (Content, Planner, Writer): it reads workspaces and items and
 * creates and edits workspaces and items, and writes draft notes. A draft note is the owner's to send: the agent never sends a round,
 * signs off or publishes.
 */
import { registerAgentTools } from "../agent/tools"
import { type WorkEdit, type WorkLink, work } from "../api/work"

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
          archived: w.archived,
        }))
      }
      const r = await work.open(input.workspace)
      return {
        workspace: r.space.workspace.slug,
        title: r.space.workspace.title,
        app: r.space.workspace.app,
        kinds: r.space.kinds.map((k) => ({
          kind: k.kind,
          label: k.label,
          stages: k.stages,
          fields: k.fields,
          sections: k.sections,
          children: k.children,
        })),
        views: r.space.views.map((v) => ({ id: v.id, view: v.view, title: v.title })),
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

const strings = { type: "array", items: str("Value") }
const fields = { type: "object", additionalProperties: true }
const sections = {
  type: "object",
  additionalProperties: { type: "string" },
  description: "Text by section heading; omitted sections stay unchanged.",
}
const links = {
  type: "array",
  items: object(
    {
      rel: str("Relation, e.g. relates or depends-on (contains uses parent instead)."),
      to: str("Item id, torque://task/CW-…, tesseract://item/<id>, caster://…, or http(s) URL."),
    },
    ["rel", "to"],
  ),
}
const change = {
  title: str("Item title"),
  fields,
  sections,
  tags: strings,
  links,
}
const check = (signal: AbortSignal) => {
  if (signal.aborted) throw new Error("The run stopped.")
}
registerAgentTools([
  {
    name: "work_space_create",
    level: "write",
    description:
      "Create a Work workspace. Use app planner to organize plans and child items; content for publication drafts; writer for manuscripts. Does not send, sign off or publish.",
    input_schema: object(
      {
        app: { type: "string", enum: ["planner", "content", "writer"] },
        slug: str("Unique lowercase letters, digits and hyphens."),
        title: str("Workspace title"),
        about: str("Workspace description"),
      },
      ["app", "slug", "title"],
    ),
    describe: (i) => `Create ${String(i.app)} workspace “${String(i.title)}”`,
    run: async (i, _desk, { signal }) => {
      check(signal)
      return work.create(String(i.slug), String(i.title), String(i.app), String(i.about ?? ""))
    },
  },
  {
    name: "work_item_create",
    level: "write",
    description:
      "Create a Work item, optionally inside a parent, with text by section, fields, tags and links. Read work_list for kinds and stages. Publishing is reserved for the owner.",
    input_schema: object(
      {
        workspace: WS,
        kind: str("Kind from work_list"),
        ...change,
        parent: ID,
        stage: str("Stage from work_list"),
      },
      ["workspace", "kind", "title"],
    ),
    describe: (i) => `Create ${String(i.kind)} “${String(i.title)}” in ${String(i.workspace)}`,
    run: async (i, _desk, { signal }) => {
      if (i.stage === "published") throw new Error("Publishing is the owner's action.")
      check(signal)
      return work.make(String(i.workspace), {
        kind: String(i.kind),
        title: String(i.title),
        parent: i.parent as string | undefined,
        stage: i.stage as string | undefined,
        fields: i.fields as Record<string, unknown> | undefined,
        sections: i.sections as Record<string, string> | undefined,
        links: i.links as WorkLink[] | undefined,
        tags: i.tags as string[] | undefined,
      })
    },
  },
  {
    name: "work_item_update",
    level: "write",
    description:
      "Edit a Work item's title, text by section, fields, tags or links. Give base_rev from work_get: a concurrent change becomes suggestions rather than being overwritten. tags and links replace their full lists. Use work_item_place for parent and order.",
    input_schema: object(
      { workspace: WS, id: ID, base_rev: { type: "integer", minimum: 1 }, ...change },
      ["workspace", "id", "base_rev"],
    ),
    describe: (i) => `Edit ${String(i.workspace)}/${String(i.id)}`,
    run: async (i, _desk, { signal }) => {
      check(signal)
      const edit: WorkEdit = {
        base_rev: Number(i.base_rev),
        title: i.title as string | undefined,
        fields: i.fields as Record<string, unknown> | undefined,
        sections: i.sections as Record<string, string> | undefined,
        tags: i.tags as string[] | undefined,
        links: i.links as WorkLink[] | undefined,
      }
      return work.edit(String(i.workspace), String(i.id), edit)
    },
  },
  {
    name: "work_item_place",
    level: "write",
    description:
      "Change a Work item's parent and sibling order. Empty parent means top level; empty before means last. Give the outline view id to persist top-level order.",
    input_schema: object(
      {
        workspace: WS,
        id: ID,
        parent: str("Parent id or empty for top level"),
        before: str("Sibling id or empty for last"),
        view: str("Outline view id"),
      },
      ["workspace", "id", "parent"],
    ),
    describe: (i) => `Move ${String(i.id)} under ${String(i.parent || "the workspace")}`,
    run: async (i, _desk, { signal }) => {
      check(signal)
      return work.place(
        String(i.workspace),
        String(i.id),
        String(i.parent),
        String(i.before ?? ""),
        String(i.view ?? ""),
      )
    },
  },
  {
    name: "work_item_stage",
    level: "write",
    description:
      "Move a Work item between its kind's stages. Cannot publish; that remains the owner's action.",
    input_schema: object({ workspace: WS, id: ID, stage: str("Stage from work_list") }, [
      "workspace",
      "id",
      "stage",
    ]),
    describe: (i) => `Move ${String(i.id)} to ${String(i.stage)}`,
    run: async (i, _desk, { signal }) => {
      if (i.stage === "published") throw new Error("Publishing is the owner's action.")
      check(signal)
      return work.move(String(i.workspace), String(i.id), "stage", String(i.stage))
    },
  },
  {
    name: "work_item_delete",
    level: "confirm",
    description:
      "Remove a Work item from views (recoverable cut, keeps Caster history). Always asks the owner.",
    input_schema: object({ workspace: WS, id: ID }, ["workspace", "id"]),
    describe: (i) => `Remove ${String(i.workspace)}/${String(i.id)} from Work`,
    run: async (i, _desk, { signal }) => {
      check(signal)
      return work.cut(String(i.workspace), String(i.id))
    },
  },
])

registerAgentTools([
  {
    name: "work_space_update",
    level: "write",
    description:
      "Rename a workspace's title or mark it archived (or unarchive it). Its slug and references stay stable.",
    input_schema: object(
      { workspace: WS, title: str("New title"), archived: { type: "boolean" } },
      ["workspace"],
    ),
    describe: (i) => `Update workspace ${String(i.workspace)}`,
    run: async (i, _desk, { signal }) => {
      check(signal)
      return work.updateSpace(String(i.workspace), {
        title: i.title as string | undefined,
        archived: i.archived as boolean | undefined,
      })
    },
  },
  {
    name: "work_space_delete",
    level: "confirm",
    description:
      "Remove a workspace from the workspace list, retaining its Caster data and history. Always asks.",
    input_schema: object({ workspace: WS }, ["workspace"]),
    describe: (i) => `Remove workspace ${String(i.workspace)}`,
    run: async (i, _desk, { signal }) => {
      check(signal)
      return work.deleteSpace(String(i.workspace))
    },
  },
])
