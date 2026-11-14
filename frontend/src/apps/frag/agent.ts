/**
 * The agent's tools for Frag: the recent captures, and capturing a note through the same
 * durable queue as the capture box (it is queued in Basalisk first and delivered behind it).
 * A capture the agent makes says so: a closing line in the note, and a tag. No delete or retry.
 */
import { registerAgentTools } from "../../agent/tools"
import { ApiError } from "../../api/account"
import { type FragCapture, frag } from "../../api/frag"
import { type Moment, moment } from "./context"
import { captureIdFor, newDraft } from "./draft"

const object = (properties: Record<string, unknown>, required: string[] = []) => ({
  type: "object",
  properties,
  required,
  additionalProperties: false,
})
const str = (description: string) => ({ type: "string", description })

/** The first line with text, without a heading's #s. */
const firstLine = (s: string) =>
  (s.split("\n").find((l) => l.trim()) ?? "")
    .trim()
    .replace(/^#+\s*/, "")
    .slice(0, 120)

function stateOf(c: FragCapture) {
  switch (c.state) {
    case "saved":
      return { state: "sent", ...(c.fragmentID ? { fragment: c.fragmentID } : {}) }
    case "needs_attention":
      return { state: "failed", reason: c.code.replaceAll("_", " ") }
    case "discarded":
      return { state: "discarded" }
    default:
      return { state: c.delivering ? "sending" : "queued" }
  }
}

/** What marks a capture as the agent's, in the note itself and as a tag on it. */
const AGENT_LINE = "_Captured by the Basalisk agent._"
const AGENT_TAG = "agent-generated"

// HTML is not kept: a capture is markdown or plain text at rest.
const HTML =
  /<\/?(html|head|body|div|span|p|br|script|style|table|tr|td|ul|ol|li|a|img|h[1-6])\b[^>]*>/i

registerAgentTools([
  {
    name: "frag_recent",
    level: "read",
    description:
      "The latest captures in Frag's queue and outbox, newest first: title (or first line), when, and state (queued, sending, sent, failed with the reason).",
    input_schema: object({
      max: { type: "integer", description: "How many (default 20, up to 100)." },
    }),
    run: async (input) => {
      const max =
        typeof input.max === "number" ? Math.max(1, Math.min(100, Math.floor(input.max))) : 20
      const items = await frag.list()
      const sorted = [...items].sort((a, b) => b.sequence - a.sequence)
      return {
        total: sorted.length,
        captures: sorted.slice(0, max).map((c) => ({
          id: c.captureID,
          title: firstLine(c.content),
          when: c.createdAt,
          ...stateOf(c),
          ...(c.tags.length ? { tags: c.tags } : {}),
        })),
      }
    },
  },
  {
    name: "frag_capture",
    level: "write",
    description:
      "Save a note to Frag (the owner's Fragments Engine) the way the capture box does: queued in Basalisk and delivered behind it, with the moment's context. Markdown or plain text, not HTML. A title becomes the first line as a heading; a source address is added at the end. The note is marked as captured by the agent (a closing line and the tag agent-generated); do not add that yourself.",
    input_schema: object(
      {
        text: str("The note, in markdown or plain text."),
        title: str("A title, if it has one."),
        source_url: str("The page or place it came from (http or https)."),
      },
      ["text"],
    ),
    describe: (input) => {
      try {
        const title = typeof input.title === "string" ? input.title.trim() : ""
        const text = typeof input.text === "string" ? input.text.replace(/\s+/g, " ").trim() : ""
        const shown = title || (text.length > 60 ? `${text.slice(0, 59)}…` : text)
        return `Capture “${shown}” to Frag`
      } catch {
        return "Capture a note to Frag"
      }
    },
    run: async (input, desk, ctx) => {
      const text = typeof input.text === "string" ? input.text.trim() : ""
      if (!text) throw new Error("text is empty: give the note.")
      if (text.length > 100_000)
        throw new Error("text is over 100,000 characters; capture a shorter note.")
      if (HTML.test(text)) throw new Error("text looks like HTML. Send markdown or plain text.")
      const title = typeof input.title === "string" ? input.title.trim().replace(/\s+/g, " ") : ""
      const url = typeof input.source_url === "string" ? input.source_url.trim() : ""
      if (url && !/^https?:\/\/\S+$/i.test(url))
        throw new Error("source_url must be a full http:// or https:// address.")
      const content = [title ? `# ${title}` : "", text, url ? `Source: <${url}>` : "", AGENT_LINE]
        .filter(Boolean)
        .join("\n\n")
      const at = await moment(desk).catch(() => ({}) as Moment)
      if (ctx.signal.aborted) throw new Error("The run was stopped; nothing was captured.")
      const d = captureIdFor({ ...newDraft(), content })
      let c: FragCapture
      try {
        c = await frag.capture({
          captureId: d.captureId,
          content,
          tags: [AGENT_TAG],
          draftAt: d.draftAt,
          submittedAt: new Date().toISOString(),
          context: {
            draft: at,
            save: { window: at.window },
          },
        })
      } catch (e) {
        throw new Error(
          `The note was not queued: ${e instanceof ApiError || e instanceof Error ? e.message : String(e)}`,
        )
      }
      const state = await frag.state().catch(() => null)
      return {
        id: c.captureID,
        ...stateOf(c),
        ...(state && state.mode !== "exchange" && !state.integration
          ? {
              note: "queued in Basalisk; it is delivered once a Fragments Engine integration is set up in Settings",
            }
          : {}),
      }
    },
  },
])
