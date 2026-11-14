/**
 * The agent's side of collab: it reads a document with its marks (so "this, here" is
 * concrete), answers a mark, adds its own, and proposes a change as a new revision the owner
 * can compare with the one before. Reading is free; writing asks, as with any write tool.
 */
import { encodePicture } from "../agent/look"
import { registerAgentTools, withImages } from "../agent/tools"
import { type CollabKind, type CollabMark, collab } from "../api/collab"
import { files } from "../api/files"
import { registerContext } from "../desktop/registry"
import { locate } from "./diff"

/** What each collab window is showing, for the agent's view of the desktop. */
export const collabOpen = new Map<string, { name: string; rev: number; openMarks: number }>()

for (const app of ["drafts", "viewer", "diagram"]) {
  registerContext(app, (win) => {
    const doc = collabOpen.get(win.id)
    return doc
      ? { extra: { collab_document: doc.name, revision: doc.rev, open_marks: doc.openMarks } }
      : {}
  })
}

const object = (properties: Record<string, unknown>, required: string[] = []) => ({
  type: "object",
  properties,
  required,
  additionalProperties: false,
})
const str = (description: string) => ({ type: "string", description })
const NAME = str(
  'The document\'s name, as collab_list gives it ("draft.launch-plan", "diagram.auth-flow").',
)

const numbered = (body: string) =>
  body
    .split("\n")
    .map((l, i) => `${String(i + 1).padStart(4)}  ${l}`)
    .join("\n")

const brief = (m: CollabMark) => ({
  id: m.id,
  by: m.by,
  kind: m.kind,
  state: m.state,
  on_revision: m.rev,
  points_at: m.anchor,
  note: m.text,
  replace_with: m.replace,
  reply: m.reply,
})

registerAgentTools([
  {
    name: "collab_list",
    level: "read",
    description:
      "List the documents you and the owner work on together: drafts (code, content, plans), artifacts under review (an image, a video, a document, a mock) and diagrams. Each has a name, a title and its latest revision. The focused window's document, if it shows one, is in the desktop snapshot as collab_document.",
    input_schema: object({ kind: { type: "string", enum: ["draft", "artifact", "diagram"] } }),
    run: async (input) =>
      (await collab.list(input.kind as CollabKind | undefined)).map((d) => ({
        name: d.name,
        kind: d.kind,
        title: d.title,
        revision: d.rev,
        updated: d.updated,
      })),
  },
  {
    name: "collab_read",
    level: "read",
    description:
      "Read a collab document: its text with line numbers (a draft), its JSON (a diagram) or what it points at (an artifact), and every mark on it. A mark is the owner (or you) pointing at something and saying something about it: points_at holds the quoted passage and its lines, or a region (x, y, w, h as fractions of the image or page), a time in seconds, or a node or edge id. Read the open marks by the user first: they are what the owner is asking about. Give rev to read an older revision.",
    input_schema: object(
      { name: NAME, rev: { type: "number", description: "A revision; the latest when left out." } },
      ["name"],
    ),
    run: async (input) => {
      const { doc, marks } = await collab.get(String(input.name), Number(input.rev) || 0)
      const body =
        doc.body.length > 120_000
          ? `${doc.body.slice(0, 120_000)}\n… (cut: the document is longer)`
          : doc.body
      return {
        name: doc.name,
        kind: doc.kind,
        title: doc.title,
        lang: doc.lang,
        points_at: doc.ref,
        revision: doc.rev,
        text: doc.kind === "draft" ? numbered(body) : body,
        marks: marks.map(brief),
      }
    },
  },
  {
    name: "collab_look",
    level: "read",
    description:
      "See an image under review, the file itself at full detail: you are sent the picture, with the owner's open region marks drawn and numbered on it (the numbers follow the order of the marks that have a region). Give mark (a mark's id) to see only that region, with some of what is around it. For an artifact that is not a still image (a video, a PDF, a mock), a draft or a diagram, use desktop_look on its window instead.",
    input_schema: object({ name: NAME, mark: str("A mark's id: see only its region, close up.") }, [
      "name",
    ]),
    describe: (input) => `Look at ${String(input.name)}`,
    run: async (input) => {
      const { doc, marks } = await collab.get(String(input.name))
      const m = doc.ref?.match(/^file:([^:]+):(.+)$/)
      if (doc.kind !== "artifact" || !m || !/\.(png|jpe?g|gif|webp|avif)$/i.test(m[2]))
        throw new Error("That is not a still image under review. Use desktop_look on its window.")
      const res = await fetch(files.contentURL(m[1], m[2]), { credentials: "same-origin" })
      if (!res.ok) throw new Error("The file could not be read.")
      const img = await createImageBitmap(await res.blob())
      const placed = marks.filter((x) => x.state === "open" && x.anchor.x !== undefined)
      const one =
        typeof input.mark === "string" ? placed.find((x) => x.id === input.mark) : undefined
      if (input.mark && !one) throw new Error("There is no open mark with a region by that id.")
      let crop = { x: 0, y: 0, w: img.width, h: img.height }
      if (one) {
        // The region with a margin of half its size (a pin: a fifth of the picture around it).
        const a = one.anchor
        const w = (a.w || 0.2) * img.width
        const h = (a.h || 0.2) * img.height
        const cx = ((a.x ?? 0) + (a.w ?? 0) / 2) * img.width
        const cy = ((a.y ?? 0) + (a.h ?? 0) / 2) * img.height
        const x0 = Math.max(0, cx - w)
        const y0 = Math.max(0, cy - h)
        crop = {
          x: x0,
          y: y0,
          w: Math.min(img.width - x0, w * 2),
          h: Math.min(img.height - y0, h * 2),
        }
      }
      const pic = encodePicture(img, crop, (ctx, scale) => {
        ctx.lineWidth = Math.max(2, 3 * scale)
        ctx.font = `600 ${Math.max(13, Math.round(18 * scale))}px sans-serif`
        placed.forEach((x, i) => {
          const a = x.anchor
          const px = ((a.x ?? 0) * img.width - crop.x) * scale
          const py = ((a.y ?? 0) * img.height - crop.y) * scale
          ctx.strokeStyle = x.by === "agent" ? "#b08cff" : "#ff3b6b"
          ctx.fillStyle = ctx.strokeStyle
          if (a.w) ctx.strokeRect(px, py, a.w * img.width * scale, (a.h ?? 0) * img.height * scale)
          ctx.beginPath()
          ctx.arc(px, py, Math.max(9, 12 * scale), 0, Math.PI * 2)
          ctx.fill()
          ctx.fillStyle = "#fff"
          ctx.textAlign = "center"
          ctx.textBaseline = "middle"
          ctx.fillText(String(i + 1), px, py)
        })
      })
      img.close()
      return withImages(
        {
          showing: one ? `the region of mark ${one.id}, with what is around it` : "the whole image",
          size: `${pic.width}×${pic.height}`,
          numbered_marks: placed.map((x, i) => ({
            number: i + 1,
            id: x.id,
            by: x.by,
            note: x.text,
          })),
        },
        [{ media_type: pic.media_type, data: pic.data }],
      )
    },
  },
  {
    name: "collab_mark",
    level: "write",
    description:
      "Point at part of a collab document and say something: a comment, a question, or a suggestion (replace_with is the text to put instead of the quoted passage; the owner accepts it with one click). For a draft give quote, copied exactly from the text (without the line numbers). For a diagram give node or edge. For an artifact give a region, a time or nothing (the whole thing). Prefer several small marks to one long one.",
    input_schema: object(
      {
        name: NAME,
        kind: { type: "string", enum: ["comment", "question", "suggestion"] },
        note: str("What you have to say about it."),
        quote: str("The exact passage of a draft this is about."),
        replace_with: str("For a suggestion: what to put instead of the quote."),
        node: str("A diagram node's id."),
        edge: str("A diagram edge's id."),
        time: { type: "number", description: "Seconds into a video." },
      },
      ["name", "kind"],
    ),
    describe: (input) => `Add a ${String(input.kind)} to ${String(input.name)}`,
    run: async (input) => {
      const name = String(input.name)
      const { doc } = await collab.get(name)
      const anchor: CollabMark["anchor"] = {}
      if (typeof input.quote === "string" && input.quote) {
        const at = locate(doc.body, { quote: input.quote })
        if (!at)
          throw new Error(
            "That quote is not in the document as written. Copy it exactly, without line numbers.",
          )
        anchor.quote = input.quote
        anchor.prefix = doc.body.slice(Math.max(0, at.offset - 40), at.offset)
        anchor.line_start = at.lineStart
        anchor.line_end = at.lineEnd
      }
      if (typeof input.node === "string") anchor.node = input.node
      if (typeof input.edge === "string") anchor.edge = input.edge
      if (typeof input.time === "number") anchor.time = input.time
      const marks = await collab.mark(name, "add", {
        rev: doc.rev,
        anchor,
        kind: input.kind as CollabMark["kind"],
        text: typeof input.note === "string" ? input.note : "",
        replace: typeof input.replace_with === "string" ? input.replace_with : undefined,
        by: "agent",
      })
      return { added: marks[marks.length - 1]?.id, marks_now: marks.length }
    },
  },
  {
    name: "collab_reply",
    level: "write",
    description:
      "Answer a mark on a collab document: reply is shown under the owner's note. Set state to done when what it asked for is finished, declined when it will not be done (say why in the reply). Leave state out to answer and keep it open.",
    input_schema: object(
      {
        name: NAME,
        mark: str("The mark's id."),
        reply: str("Your answer."),
        state: { type: "string", enum: ["open", "done", "declined"] },
      },
      ["name", "mark", "reply"],
    ),
    describe: (input) => `Answer a mark on ${String(input.name)}`,
    run: async (input) => {
      await collab.mark(String(input.name), "update", {
        id: String(input.mark),
        reply: String(input.reply),
        state: input.state as CollabMark["state"] | undefined,
      })
      return "answered"
    },
  },
  {
    name: "collab_write",
    level: "write",
    description:
      "Write a collab document: a new revision of an existing one (give name and base_revision, the revision you read), or a new one (give kind and title, and lang for a draft). The owner sees the change as a comparison with the revision before, so write the whole new text, changing only what should change. Refused when the document changed since base_revision: read it again. A diagram's text is its JSON, in the shape collab_read showed.",
    input_schema: object(
      {
        name: NAME,
        base_revision: { type: "number", description: "The revision this change is made on." },
        kind: { type: "string", enum: ["draft", "diagram"] },
        title: str("The title of a new document."),
        lang: str("What a new draft is written in: markdown, go, typescript, python, text…"),
        text: str("The whole text."),
      },
      ["text"],
    ),
    describe: (input) =>
      input.name
        ? `Write a new revision of ${String(input.name)}`
        : `Create the ${String(input.kind ?? "draft")} “${String(input.title ?? "Untitled")}”`,
    run: async (input, desk) => {
      const text = String(input.text ?? "")
      if (typeof input.name === "string" && input.name) {
        const base = Number(input.base_revision) || 0
        if (!base) throw new Error("Give base_revision: the revision you read.")
        const doc = await collab.save(input.name, text, base, undefined, "agent")
        return { name: doc.name, revision: doc.rev }
      }
      const kind = input.kind === "diagram" ? "diagram" : "draft"
      const doc = await collab.create(
        {
          kind,
          title: String(input.title ?? "Untitled"),
          lang: typeof input.lang === "string" ? input.lang : "markdown",
          body: text,
        },
        "agent",
      )
      desk.open(kind === "diagram" ? "diagram" : "drafts", {
        args: { name: doc.name },
        key: doc.name,
        title: doc.title,
      })
      return { name: doc.name, revision: doc.rev, opened: true }
    },
  },
])
