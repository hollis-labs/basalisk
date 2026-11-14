/**
 * The agent's tools for Browser windows. Each one goes through /api/browser (the route an
 * agent uses, which writes what it does to the shell log), never the page's own channel. The
 * scripts are fixed; what the model gives (a query, a selector, a value) goes in as JSON
 * data. There is no general eval tool.
 */

import { registerAgentTools } from "../../agent/tools"
import { openLink } from "../../api/browser"
import { type ResultCard, registerResultCards, desk as theDesk } from "../../cite/cards"

const STOPPED = "The run was stopped; nothing was changed."

import { ApiError, request } from "../../api/account"
import type { BrowserState } from "../../api/browser"
import type { DeskAPI } from "../../desktop/registry"

/** How much page text one read returns. */
export const READ_LIMIT = 40_000

const object = (properties: Record<string, unknown>, required: string[] = []) => ({
  type: "object",
  properties,
  required,
  additionalProperties: false,
})
const str = (description: string) => ({ type: "string", description })
const WINDOW = str(
  "A Browser window id from desktop_windows. Leave out for the focused Browser window, else the frontmost one.",
)

/** One operation on a window's page, through the agent's (logged) route. */
async function command<T = unknown>(
  id: string,
  op: string,
  body?: Record<string, unknown>,
): Promise<T> {
  try {
    const r = await request<{ result: T }>(`browser/${encodeURIComponent(id)}/${op}`, "POST", body)
    return r.result
  } catch (e) {
    if (e instanceof ApiError && e.status === 404)
      throw new Error(
        op === "eval" || op === "state"
          ? `Window ${id} has no page loaded (it may still be opening).`
          : `Window ${id} is not a Browser window with a page.`,
      )
    throw e
  }
}

/** Runs a fixed script that returns JSON text, and parses it. */
async function script<T>(id: string, tool: string, body: string): Promise<T> {
  // The leading comment is what the shell log shows for the call.
  const raw = await command<unknown>(id, "eval", { js: `/* ${tool} */ ${body}` })
  if (typeof raw !== "string") throw new Error("The page did not answer.")
  return JSON.parse(raw) as T
}

/** The Browser window a call means: the one named, else the focused one, else the frontmost. */
export async function browserWindow(
  desk: DeskAPI,
  input: Record<string, unknown>,
): Promise<string> {
  const enabled = await request<{ enabled: boolean }>("browser").catch(() => ({ enabled: false }))
  if (!enabled.enabled)
    throw new Error(
      "Browser windows exist only in the Basalisk desktop app, and it is not running here.",
    )
  const st = desk.wmState()
  const browsers = st.wins.filter((w) => w.app === "browser")
  const named = typeof input.window_id === "string" ? input.window_id : ""
  if (named) {
    const w = st.wins.find((x) => x.id === named)
    if (!w) throw new Error(`There is no window ${named}. desktop_windows lists them.`)
    if (w.app !== "browser") throw new Error(`Window ${named} is ${w.app}, not a Browser window.`)
    return named
  }
  const focused = browsers.find((w) => w.id === st.focus)
  if (focused) return focused.id
  const front = [...browsers].sort((a, b) => b.z - a.z)[0]
  if (!front) throw new Error("No Browser window is open. Open one with browser_navigate.")
  return front.id
}

const state = (id: string) => command<BrowserState>(id, "state")

/** Waits for the page to finish loading (up to `ms`), and returns its state. */
async function settle(id: string, ms = 20_000): Promise<BrowserState & { timedOut?: boolean }> {
  const until = Date.now() + ms
  // A navigation may not have started loading yet when this is first called.
  await new Promise((r) => setTimeout(r, 300))
  for (;;) {
    let s: BrowserState | null = null
    try {
      s = await state(id)
    } catch {
      // The view may still be opening.
    }
    if (s && !s.loading && s.url) return s
    if (Date.now() > until) return { ...(s ?? ({} as BrowserState)), timedOut: true }
    await new Promise((r) => setTimeout(r, 250))
  }
}

const cut = (text: string, limit: number) =>
  text.length > limit
    ? { text: text.slice(0, limit), note: `cut at ${limit} of ${text.length} characters` }
    : { text }

// ---- browser_read ----

let clipBundle: Promise<string> | null = null
const loadClip = () => {
  clipBundle ??= fetch("/clip/extract.js", { credentials: "same-origin" }).then((r) => {
    if (!r.ok) throw new Error("missing")
    return r.text()
  })
  clipBundle.catch(() => {
    clipBundle = null
  })
  return clipBundle
}

const PLAIN = `JSON.stringify({title: document.title, url: location.href, text: (document.body && document.body.innerText) || ""})`
const CAPTURED = `(()=>{const c=window.__FE_CLIPPER_CAPTURE__;return c?JSON.stringify({title:c.document&&c.document.title,markdown:c.document&&c.document.contentMarkdown,url:c.identity&&(c.identity.canonicalUrl||c.identity.submittedUrl)}):null})()`

/** The page as markdown (fe-clipper's extraction, as Clip uses), else its plain text. */
async function readPage(id: string, format: string) {
  const s = await state(id)
  if (format !== "text" && /^https?:/i.test(s.url)) {
    try {
      const bundle = await loadClip()
      await command(id, "eval", {
        js: `/* browser_read: page extraction */ delete window.__FE_CLIPPER_CAPTURE__;\n${bundle}\n;0`,
      })
      for (let i = 0; i < 40; i++) {
        const raw = await command<unknown>(id, "eval", { js: `/* browser_read */ ${CAPTURED}` })
        if (typeof raw === "string") {
          const c = JSON.parse(raw) as { title?: string; markdown?: string; url?: string }
          if (c.markdown?.trim())
            return {
              title: c.title || s.title,
              url: s.url,
              format: "markdown",
              content: c.markdown,
            }
          break
        }
        await new Promise((r) => setTimeout(r, 200))
      }
    } catch {
      // Fall back to the page's text.
    }
  }
  const p = await script<{ title: string; url: string; text: string }>(id, "browser_read", PLAIN)
  return { title: p.title, url: p.url, format: "text", content: p.text }
}

// ---- browser_find ----

const FIND = (q: string, max: number) => `(() => {
  const q = ${JSON.stringify(q.toLowerCase())}, max = ${max}
  const text = ((document.body && document.body.innerText) || "").replace(/[ \\t]+/g, " ")
  const low = text.toLowerCase(), out = []
  let total = 0
  for (let i = low.indexOf(q); i >= 0; i = low.indexOf(q, i + q.length)) {
    total++
    if (out.length < max) {
      const a = Math.max(0, i - 80), b = Math.min(text.length, i + q.length + 80)
      out.push((a > 0 ? "…" : "") + text.slice(a, b).replace(/\\s*\\n\\s*/g, " ⏎ ") + (b < text.length ? "…" : ""))
    }
  }
  return JSON.stringify({ total, matches: out })
})()`

// ---- browser_links ----

const LINKS = (max: number, filter: string) => `(() => {
  const f = ${JSON.stringify(filter.toLowerCase())}, max = ${max}, out = [], seen = new Set()
  let total = 0
  for (const a of document.querySelectorAll("a[href]")) {
    const href = a.href
    if (!/^https?:|^mailto:/i.test(href)) continue
    const text = (a.innerText || a.getAttribute("aria-label") || a.title || "").replace(/\\s+/g, " ").trim().slice(0, 160)
    if (f && !text.toLowerCase().includes(f) && !href.toLowerCase().includes(f)) continue
    const key = text + " " + href
    if (seen.has(key)) continue
    seen.add(key)
    total++
    if (out.length < max) out.push({ text, href })
  }
  return JSON.stringify({ total, links: out })
})()`

// ---- browser_click / browser_fill: finding the element ----

/** A page-side function: the element meant by a CSS selector, or by its visible text/label. */
const FINDER = `
  const norm = (s) => (s || "").replace(/\\s+/g, " ").trim().toLowerCase()
  const visible = (el) => { const r = el.getBoundingClientRect(); return r.width > 0 && r.height > 0 && getComputedStyle(el).visibility !== "hidden" }
  const describe = (el) => {
    const tag = el.tagName.toLowerCase()
    const field = /^(INPUT|SELECT|TEXTAREA)$/.test(el.tagName)
    const named = norm(el.getAttribute("aria-label") || (el.labels && el.labels[0] && el.labels[0].innerText) || el.getAttribute("placeholder") || el.name || el.title)
    const label = (field ? named || norm(el.value) : norm(el.innerText || el.value) || named).slice(0, 80)
    const href = el.href ? " → " + el.href : ""
    return tag + (el.id ? "#" + el.id : "") + (label ? ' "' + label + '"' : "") + href
  }
  const pick = (selector, text, kinds, labelOf) => {
    if (selector) {
      let all
      try { all = [...document.querySelectorAll(selector)] } catch (e) { return { error: "That is not a valid CSS selector: " + e.message } }
      const shown = all.filter(visible)
      if (!all.length) return { error: "Nothing on the page matches the selector " + selector + "." }
      if (!shown.length) return { error: "The selector matches " + all.length + " element(s), none of them visible." }
      return { el: shown[0], count: shown.length }
    }
    const t = norm(text)
    const cands = [...document.querySelectorAll(kinds)].filter(visible)
    const exact = cands.filter((el) => labelOf(el).some((l) => l === t))
    const part = exact.length ? exact : cands.filter((el) => labelOf(el).some((l) => l.includes(t)))
    if (!part.length) return { error: 'Nothing visible on the page is labelled "' + text + '". Try a CSS selector, or browser_read or browser_links to see what is there.' }
    return { el: part[0], count: part.length }
  }`

const CLICK = (selector: string, text: string) => `(() => {
  ${FINDER}
  const kinds = "a[href], button, [role=button], [role=link], [role=tab], [role=menuitem], input[type=submit], input[type=button], input[type=checkbox], input[type=radio], summary, label"
  const labels = (el) => [norm(el.innerText), norm(el.value), norm(el.getAttribute("aria-label")), norm(el.title)].filter(Boolean)
  const r = pick(${JSON.stringify(selector)}, ${JSON.stringify(text)}, kinds, labels)
  if (r.error) return JSON.stringify(r)
  const what = describe(r.el)
  r.el.scrollIntoView({ block: "center" })
  r.el.click()
  return JSON.stringify({ clicked: what, others: r.count - 1 })
})()`

const FILL = (selector: string, field: string, value: string) => `(() => {
  ${FINDER}
  const kinds = "input:not([type=hidden]):not([type=submit]):not([type=button]), textarea, select, [contenteditable=true], [contenteditable='']"
  const labels = (el) => {
    const out = [norm(el.getAttribute("aria-label")), norm(el.placeholder), norm(el.name), norm(el.id), norm(el.title)]
    for (const l of el.labels || []) out.push(norm(l.innerText))
    const by = el.getAttribute("aria-labelledby")
    if (by) for (const id of by.split(/\\s+/)) out.push(norm(document.getElementById(id)?.innerText))
    return out.filter(Boolean)
  }
  const r = pick(${JSON.stringify(selector)}, ${JSON.stringify(field)}, kinds, labels)
  if (r.error) return JSON.stringify(r)
  const el = r.el, value = ${JSON.stringify(value)}
  const what = describe(el)
  if (el.disabled || el.readOnly) return JSON.stringify({ error: "The field " + what + " cannot be edited." })
  el.focus()
  if (el.isContentEditable) {
    el.textContent = value
    el.dispatchEvent(new InputEvent("input", { bubbles: true }))
  } else if (el.tagName === "SELECT") {
    const opt = [...el.options].find((o) => norm(o.value) === norm(value) || norm(o.text) === norm(value))
    if (!opt) return JSON.stringify({ error: "The list " + what + " has no option " + JSON.stringify(value) + ". Options: " + [...el.options].map((o) => o.text.trim()).slice(0, 30).join(", ") })
    el.value = opt.value
  } else if (el.type === "checkbox" || el.type === "radio") {
    const on = /^(true|yes|on|1|checked)$/i.test(value)
    if (el.checked !== on) el.click()
  } else {
    // The native setter, so frameworks that track the value see the change.
    const proto = el.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype
    Object.getOwnPropertyDescriptor(proto, "value").set.call(el, value)
  }
  el.dispatchEvent(new Event("input", { bubbles: true }))
  el.dispatchEvent(new Event("change", { bubbles: true }))
  const now = el.isContentEditable ? el.textContent : el.tagName === "SELECT" ? el.options[el.selectedIndex].text : el.type === "checkbox" || el.type === "radio" ? String(el.checked) : el.type === "password" ? "(hidden)" : el.value
  return JSON.stringify({ filled: what, value: now, others: r.count - 1 })
})()`

const int = (v: unknown, def: number, lo: number, hi: number) =>
  typeof v === "number" && Number.isFinite(v) ? Math.max(lo, Math.min(hi, Math.round(v))) : def

/** The target of a click or fill: exactly one of a selector or a text. */
function target(input: Record<string, unknown>, textKey: string): [string, string] {
  const selector = typeof input.selector === "string" ? input.selector.trim() : ""
  const text = typeof input[textKey] === "string" ? (input[textKey] as string).trim() : ""
  if (!selector === !text) throw new Error(`Give exactly one of selector or ${textKey}.`)
  return [selector, text]
}

// ---- One line per call, for the permission card and the call's row ----

const short = (v: unknown, n = 60) => {
  const t = typeof v === "string" ? v.replace(/\s+/g, " ").trim() : ""
  return t.length > n ? `${t.slice(0, n - 1)}…` : t
}
const hostName = (url: unknown) => {
  try {
    return new URL(String(url)).host || short(url)
  } catch {
    return short(url)
  }
}
/** The page a call means, by its window's title (sync and best effort: never throws). */
function pageName(input: Record<string, unknown>): string {
  try {
    const desk = (globalThis as { basalisk?: { desk?: DeskAPI } }).basalisk?.desk
    const st = desk?.wmState()
    if (!st) return "the page"
    const id = typeof input.window_id === "string" ? input.window_id : ""
    const browsers = st.wins.filter((w) => w.app === "browser")
    const w = id
      ? st.wins.find((x) => x.id === id)
      : (browsers.find((x) => x.id === st.focus) ?? [...browsers].sort((a, b) => b.z - a.z)[0])
    return w?.title ? `“${short(w.title, 40)}”` : "the page"
  } catch {
    return "the page"
  }
}
const target_ = (input: Record<string, unknown>, key: string) =>
  typeof input[key] === "string" && input[key]
    ? `“${short(input[key])}”`
    : `${short(input.selector) || "?"}`

registerAgentTools([
  {
    name: "browser_read",
    level: "read",
    description: `Read the page a Browser window shows: its title, address and content, as markdown (the readable part of a web page) or plain text. Long pages are cut at ${READ_LIMIT} characters; use offset to read on.`,
    input_schema: object({
      window_id: WINDOW,
      format: {
        type: "string",
        enum: ["markdown", "text"],
        description: "markdown (default) or text: all the visible text of the page.",
      },
      offset: {
        type: "integer",
        description: "Start this many characters in (to read past a cut).",
      },
    }),
    run: async (input, desk) => {
      const id = await browserWindow(desk, input)
      const page = await readPage(id, input.format === "text" ? "text" : "markdown")
      const offset = int(input.offset, 0, 0, Number.MAX_SAFE_INTEGER)
      const part = cut(page.content.slice(offset), READ_LIMIT)
      return {
        window_id: id,
        title: page.title,
        url: page.url,
        format: page.format,
        ...(offset ? { offset } : {}),
        content: part.text,
        ...(part.note ? { note: `${part.note}; read on with offset ${offset + READ_LIMIT}` } : {}),
      }
    },
  },
  {
    name: "browser_find",
    level: "read",
    description:
      "Find text on the page a Browser window shows (case-insensitive). Returns how many times it occurs and each match with some text around it.",
    input_schema: object(
      {
        window_id: WINDOW,
        query: str("The text to find."),
        max: { type: "integer", description: "At most this many matches (default 20, up to 100)." },
      },
      ["query"],
    ),
    run: async (input, desk) => {
      const q = typeof input.query === "string" ? input.query.trim() : ""
      if (!q) throw new Error("query is empty: give the text to find.")
      if (q.length > 500) throw new Error("query is too long (500 characters at most).")
      const id = await browserWindow(desk, input)
      const r = await script<{ total: number; matches: string[] }>(
        id,
        "browser_find",
        FIND(q, int(input.max, 20, 1, 100)),
      )
      return { window_id: id, query: q, ...r }
    },
  },
  {
    name: "browser_links",
    level: "read",
    description:
      "List the links on the page a Browser window shows: text and address, in page order. Optionally only those whose text or address contains a filter.",
    input_schema: object({
      window_id: WINDOW,
      filter: str("Only links whose text or address contains this (case-insensitive)."),
      max: { type: "integer", description: "At most this many links (default 100, up to 300)." },
    }),
    run: async (input, desk) => {
      const id = await browserWindow(desk, input)
      const filter = typeof input.filter === "string" ? input.filter.trim() : ""
      const max = int(input.max, 100, 1, 300)
      const r = await script<{ total: number; links: { text: string; href: string }[] }>(
        id,
        "browser_links",
        LINKS(max, filter),
      )
      return {
        window_id: id,
        ...r,
        ...(r.total > r.links.length
          ? { note: `showing ${r.links.length} of ${r.total}; narrow with filter` }
          : {}),
      }
    },
  },
  {
    name: "browser_navigate",
    level: "act",
    description:
      "Go to a web address in a Browser window, or open a new Browser window at it (new_window, or when none is open). Waits for the page to load and returns the address it ended at and its title.",
    input_schema: object(
      {
        url: str("The address, with http:// or https://."),
        window_id: WINDOW,
        new_window: {
          type: "boolean",
          description: "Open a new Browser window instead of using one.",
        },
      },
      ["url"],
    ),
    describe: (input) =>
      `Go to ${hostName(input.url)}${input.new_window === true ? " in a new window" : ""}`,
    run: async (input, desk) => {
      const url = typeof input.url === "string" ? input.url.trim() : ""
      if (!/^https?:\/\/\S+$/i.test(url))
        throw new Error("url must be a full http:// or https:// address.")
      const st = desk.wmState()
      const hasBrowser = st.wins.some((w) => w.app === "browser")
      let id: string
      if (input.new_window === true || (!hasBrowser && typeof input.window_id !== "string")) {
        const enabled = await request<{ enabled: boolean }>("browser").catch(() => ({
          enabled: false,
        }))
        if (!enabled.enabled)
          throw new Error(
            "Browser windows exist only in the Basalisk desktop app, and it is not running here.",
          )
        const before = new Set(st.wins.map((w) => w.id))
        desk.open("browser", { args: { url } })
        await new Promise((r) => setTimeout(r, 100))
        const made = desk.wmState().wins.find((w) => w.app === "browser" && !before.has(w.id))
        if (!made) throw new Error("The Browser window did not open.")
        id = made.id
      } else {
        id = await browserWindow(desk, input)
        await command(id, "navigate", { url })
      }
      const s = await settle(id)
      return {
        window_id: id,
        url: s.url,
        title: s.title,
        ...(s.timedOut ? { note: "still loading after 20 seconds" } : {}),
      }
    },
  },
  {
    name: "browser_click",
    level: "write",
    description:
      "Click a link, button or control on the page a Browser window shows, found by its visible text (or label) or by a CSS selector. It acts in the owner's signed-in session. Returns what was clicked and where the page is after.",
    input_schema: object({
      window_id: WINDOW,
      text: str(
        "The visible text or label of what to click (exact match preferred, else contains).",
      ),
      selector: str("A CSS selector instead of text."),
    }),
    describe: (input) => `Click ${target_(input, "text")} on ${pageName(input)}`,
    run: async (input, desk, ctx) => {
      const [selector, text] = target(input, "text")
      const id = await browserWindow(desk, input)
      const before = await state(id)
      if (ctx.signal.aborted) throw new Error(STOPPED)
      const r = await script<{ error?: string; clicked?: string; others?: number }>(
        id,
        "browser_click",
        CLICK(selector, text),
      )
      if (r.error) throw new Error(r.error)
      const after = await settle(id, 10_000)
      return {
        window_id: id,
        clicked: r.clicked,
        ...(r.others
          ? { note: `${r.others} other element(s) also matched; the first was clicked` }
          : {}),
        url: after.url,
        title: after.title,
        navigated: after.url !== before.url,
      }
    },
  },
  {
    name: "browser_fill",
    level: "write",
    description:
      "Type a value into a field on the page a Browser window shows (text box, text area, list, checkbox), found by its label, placeholder or name, or by a CSS selector. It does not submit; click the button for that. It acts in the owner's signed-in session.",
    input_schema: object(
      {
        window_id: WINDOW,
        field: str("The field's label, placeholder or name."),
        selector: str("A CSS selector instead of field."),
        value: str(
          "The value. For a checkbox: true or false. For a list: the option's text or value.",
        ),
      },
      ["value"],
    ),
    // The value is not shown: it may be a password.
    describe: (input) => `Fill ${target_(input, "field")} on ${pageName(input)}`,
    run: async (input, desk, ctx) => {
      const [selector, field] = target(input, "field")
      if (typeof input.value !== "string") throw new Error("value must be a string.")
      const id = await browserWindow(desk, input)
      if (ctx.signal.aborted) throw new Error(STOPPED)
      const r = await script<{ error?: string; filled?: string; value?: string; others?: number }>(
        id,
        "browser_fill",
        FILL(selector, field, input.value),
      )
      if (r.error) throw new Error(r.error)
      return {
        window_id: id,
        filled: r.filled,
        value: r.value,
        ...(r.others
          ? { note: `${r.others} other field(s) also matched; the first was filled` }
          : {}),
      }
    },
  },
])

// The page read or opened shows as a card in the agent panel (cite/cards.tsx). Open brings
// its window forward, or opens the address again when that window is gone.
const pageCard = (_in: Record<string, unknown>, r: unknown): ResultCard | null => {
  const o = (r ?? {}) as Record<string, unknown>
  const url = typeof o.url === "string" ? o.url : ""
  if (!/^https?:\/\//i.test(url)) return null
  let host = url
  try {
    host = new URL(url).host
  } catch {
    // As given.
  }
  const win = typeof o.window_id === "string" ? o.window_id : ""
  const facts: ResultCard["facts"] = [{ label: "At", value: host }]
  if (typeof o.note === "string" && o.note.startsWith("still loading"))
    facts.push({ label: "State", value: "still loading" })
  return {
    kind: "page",
    title: (typeof o.title === "string" && o.title.trim()) || url,
    facts,
    open: () => {
      const d = theDesk()
      if (d && win && d.wmState().wins.some((w) => w.id === win)) d.wm({ type: "focus", id: win })
      else openLink(url)
    },
  }
}
registerResultCards({
  browser_read: pageCard,
  browser_navigate: pageCard,
  browser_links: (_in, r) => {
    const o = (r ?? {}) as { total?: unknown; links?: unknown }
    if (!Array.isArray(o.links)) return null
    const rows = (o.links as Record<string, unknown>[])
      .filter((l) => typeof l?.href === "string" && /^https?:\/\//i.test(l.href as string))
      .map((l) => {
        const href = l.href as string
        let host = ""
        try {
          host = new URL(href).host
        } catch {
          // Shown without.
        }
        const label = typeof l.text === "string" ? l.text.replace(/\s+/g, " ").trim() : ""
        return { title: label || href, fact: host, open: () => openLink(href) }
      })
    return {
      list: true,
      kind: "page",
      heading: "Links",
      total: typeof o.total === "number" ? o.total : rows.length,
      rows,
    }
  },
})
