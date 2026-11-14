/**
 * Getting a page ready for fe-clipper's extraction, where the page alone would give it less
 * than it shows. Each function runs inside the page (sent as its source text, so it must not
 * refer to anything outside itself); it starts its work and records the outcome under
 * window.__BASALISK_CLIP_PREP__, which clip.ts polls. Anything that fails or takes too long
 * leaves the page as it was: the clip is then what it would have been without this.
 *
 * - YouTube: after navigating within YouTube (a click from search, a recommendation), the
 *   page's serialized state (ytInitialPlayerResponse, ytInitialData) still describes the page
 *   first loaded, so the adapter finds no captions for the video shown. When that state is
 *   not for the current video, the current watch address is fetched from the page (same
 *   origin), its two state objects are taken out of the HTML as JSON, checked to be for this
 *   video, and put first in <head> as non-executing JSON script elements, which the adapter
 *   reads by id. cleanupPage removes them.
 *   The transcript: the page's caption files answer empty to any fetch without the player's
 *   own token (for this server and for the page alike), so the text comes from YouTube's
 *   transcript panel, opened for the clip and closed again (see transcript() below).
 * - Reddit: on a post's page, comments render as the post scrolls in; wait briefly for them.
 * - The selection: WebKit empties a Browser page's selection the moment the page loses the
 *   keyboard, which a click on the window's clip button does before the clip runs.
 *   keepSelection (installed in each page as it loads, clip.ts) keeps the last selection
 *   when it was emptied by that and not by the person; preparePage puts it back when the
 *   page has none, so the extraction reads it as the highlight.
 *
 * fe-clipper's own adapter could do the YouTube part (a note is filed with its owner); when it
 * does, this part can go.
 */

/** Starts the preparation for the page; the outcome lands in window.__BASALISK_CLIP_PREP__. */
export function preparePage(): void {
  const w = window as unknown as Record<string, unknown>
  const KEY = "__BASALISK_CLIP_PREP__"
  const MAX_STATE = 2_000_000 // fe-clipper's MAX_SERIALIZED_STATE_LENGTH
  // A selection emptied by the page losing the keyboard (keepSelection), put back.
  let restored = false
  try {
    const kept = w.__BASALISK_KEEP_SEL__ as { range: Range | null; at: number } | undefined
    const sel = window.getSelection()
    if (
      sel &&
      kept?.range &&
      (sel.isCollapsed || !sel.toString().trim()) &&
      Date.now() - kept.at < 120_000 &&
      kept.range.startContainer.isConnected
    ) {
      sel.removeAllRanges()
      sel.addRange(kept.range)
      restored = sel.toString().trim().length > 0
    }
  } catch {
    // The page's own selection, or none.
  }
  w[KEY] = { done: false, restoredSelection: restored }
  const finish = (r: Record<string, unknown>) => {
    w[KEY] = { done: true, restoredSelection: restored, ...r }
  }

  /** The first JSON object after each `name` in text, parsed (strings and escapes respected). */
  const objectAfter = (text: string, name: string): Record<string, unknown> | null => {
    let from = 0
    for (let tries = 0; tries < 12; tries++) {
      const at = text.indexOf(name, from)
      if (at < 0) return null
      from = at + name.length
      const open = text.indexOf("{", from)
      // The object must follow the name closely ("name = {", "name": {).
      if (open < 0 || open - from > 8) continue
      let depth = 0
      let inString = false
      let escaped = false
      for (let i = open; i < text.length && i - open < 4_000_000; i++) {
        const c = text[i]
        if (inString) {
          if (escaped) escaped = false
          else if (c === "\\") escaped = true
          else if (c === '"') inString = false
          continue
        }
        if (c === '"') inString = true
        else if (c === "{") depth++
        else if (c === "}" && --depth === 0) {
          try {
            const v = JSON.parse(text.slice(open, i + 1))
            if (v && typeof v === "object" && !Array.isArray(v)) return v as Record<string, unknown>
          } catch {
            // Not this one.
          }
          break
        }
      }
    }
    return null
  }
  const videoIdOf = (player: Record<string, unknown> | null) => {
    const details = player?.videoDetails as Record<string, unknown> | undefined
    return typeof details?.videoId === "string" ? details.videoId : ""
  }

  try {
    const u = new URL(location.href)
    const host = u.hostname.toLowerCase()

    if (/(^|\.)reddit\.com$/.test(host) && u.pathname.includes("/comments/")) {
      const started = Date.now()
      const wait = () => {
        if (document.querySelector("shreddit-comment, .commentarea .comment"))
          return finish({ reddit: "comments rendered" })
        if (Date.now() - started > 3000) return finish({ reddit: "no comments rendered" })
        setTimeout(wait, 150)
      }
      wait()
      return
    }

    if (!/(^|\.)youtube\.com$/.test(host) || u.pathname !== "/watch") {
      finish({ skipped: true })
      return
    }
    const id = u.searchParams.get("v") ?? ""
    if (!/^[A-Za-z0-9_-]{6,20}$/.test(id)) {
      finish({ skipped: true })
      return
    }
    const ownLanguage = (player: Record<string, unknown> | null) => {
      // The video's own caption language: the default audio track's default caption track,
      // else the auto-generated track (it is in the spoken language).
      const r = (player?.captions as Record<string, unknown> | undefined)
        ?.playerCaptionsTracklistRenderer as Record<string, unknown> | undefined
      const tracks = (r?.captionTracks as Record<string, unknown>[] | undefined) ?? []
      const audio = (r?.audioTracks as Record<string, unknown>[] | undefined) ?? []
      const i = audio[Number(r?.defaultAudioTrackIndex ?? 0)]?.defaultCaptionTrackIndex
      const pick = typeof i === "number" ? tracks[i] : tracks.find((t) => t.kind === "asr")
      return typeof pick?.languageCode === "string" ? pick.languageCode : ""
    }

    // 1. The state: the one the adapter reads first, refreshed when it is for another video.
    const state = async (): Promise<{
      note: Record<string, unknown>
      player: Record<string, unknown> | null
    }> => {
      for (const s of Array.from(document.scripts)) {
        const text = s.textContent ?? ""
        if (!text.includes("ytInitialPlayerResponse")) continue
        const player =
          s.id === "ytInitialPlayerResponse"
            ? objectAfter(`ytInitialPlayerResponse=${text}`, "ytInitialPlayerResponse")
            : objectAfter(text, "ytInitialPlayerResponse")
        if (player) {
          if (videoIdOf(player) === id) return { note: { youtube: "state is current" }, player }
          break
        }
      }
      const control = new AbortController()
      const timer = setTimeout(() => control.abort(), 4000)
      try {
        const r = await fetch(u.href, {
          credentials: "same-origin",
          signal: control.signal,
          headers: { Accept: "text/html" },
        })
        if (!r.ok) throw new Error(`answered ${r.status}`)
        const html = await r.text()
        const player = objectAfter(html, "ytInitialPlayerResponse")
        if (videoIdOf(player) !== id)
          return { note: { youtube: "the fetched state is not for this video" }, player: null }
        const states: [string, Record<string, unknown> | null][] = [
          ["ytInitialPlayerResponse", player],
          ["ytInitialData", objectAfter(html, "ytInitialData")],
        ]
        let inserted = 0
        for (const [name, value] of states.reverse()) {
          if (!value) continue
          const json = JSON.stringify(value)
          if (json.length > MAX_STATE) continue
          const el = document.createElement("script")
          el.type = "application/json" // never executed
          el.id = name
          el.setAttribute("data-basalisk-clip", "")
          el.textContent = json
          document.head.insertBefore(el, document.head.firstChild)
          inserted++
        }
        return { note: { youtube: "state refreshed", inserted }, player }
      } catch (e) {
        return {
          note: { youtube: `kept the page's state (${String(e).slice(0, 80)})` },
          player: null,
        }
      } finally {
        clearTimeout(timer)
      }
    }

    // 2. The transcript: YouTube's own transcript panel, opened if it is not, read as the
    // adapter reads a rendered transcript. YouTube's current markup is not one the adapter
    // knows, so each segment is marked with the adapter's own data-youtube-transcript-*
    // hooks (cleanupPage removes the marks, and closes the panel if this opened it).
    const transcript = async (lang: string): Promise<string> => {
      const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))
      const panelSel = 'ytd-engagement-panel-section-list-renderer[target-id*="transcript"]'
      const SEG = "transcript-segment-view-model"
      let segs = Array.from(document.querySelectorAll(SEG))
      if (!segs.length) {
        const open = document
          .querySelector(panelSel)
          ?.getAttribute("visibility")
          ?.endsWith("EXPANDED")
        const button = document.querySelector<HTMLElement>(
          "ytd-video-description-transcript-section-renderer button",
        )
        if (!button) return "no transcript on this page"
        button.click()
        if (!open) w.__BASALISK_CLIP_PANEL__ = true
        const started = Date.now()
        while (Date.now() - started < 5000) {
          segs = Array.from(document.querySelectorAll(SEG))
          if (segs.length) break
          await sleep(150)
        }
        // The list arrives in one go; give it a moment to finish rendering.
        await sleep(250)
        segs = Array.from(document.querySelectorAll(SEG))
      }
      if (!segs.length) return "the transcript did not load in time"
      const mark = (el: Element | null | undefined, name: string) => {
        if (!el) return
        el.setAttribute(name, "")
        el.setAttribute("data-basalisk-clip-mark", name)
      }
      for (const seg of segs) {
        mark(seg, "data-youtube-transcript-segment")
        mark(
          seg.querySelector('[class*="TranscriptSegmentViewModelTimestamp"]:not([class*="A11y"])'),
          "data-youtube-transcript-timestamp",
        )
        mark(
          seg.querySelector('[role="text"], [class*="ytAttributedStringHost"]'),
          "data-youtube-transcript-text",
        )
      }
      const panel = document.querySelector(panelSel)
      if (panel && lang) {
        panel.setAttribute("data-youtube-transcript-language", lang)
        panel.setAttribute("data-basalisk-clip-mark", "data-youtube-transcript-language")
      }
      return `${segs.length} segments`
    }

    void (async () => {
      const st = await state()
      let tx = ""
      try {
        tx = await transcript(ownLanguage(st.player))
      } catch (e) {
        tx = `failed (${String(e).slice(0, 60)})`
      }
      finish({ ...st.note, ownLanguage: ownLanguage(st.player), transcript: tx })
    })()
  } catch (e) {
    finish({ failed: String(e).slice(0, 80) })
  }
}

/**
 * Installed in each Browser page as it loads: keeps the last selection, and keeps it when it
 * is emptied in the same moment as the page loses the keyboard (WebKit empties it then, a
 * selectionchange and a blur together), not when the person clears it in the page.
 */
export function keepSelection(): void {
  const w = window as unknown as Record<string, unknown>
  if (w.__BASALISK_KEEP_SEL__) return
  const st: { range: Range | null; at: number; blurAt: number } = { range: null, at: 0, blurAt: 0 }
  w.__BASALISK_KEEP_SEL__ = st
  window.addEventListener("blur", () => {
    st.blurAt = Date.now()
  })
  document.addEventListener("selectionchange", () => {
    const sel = window.getSelection()
    if (sel && sel.rangeCount > 0 && !sel.isCollapsed && sel.toString().trim()) {
      st.range = sel.getRangeAt(0).cloneRange()
      st.at = Date.now()
      return
    }
    if (!st.range) return
    const emptiedAt = Date.now()
    // The blur comes in the same moment when the keyboard left; otherwise the person cleared it.
    setTimeout(() => {
      if (st.blurAt < emptiedAt - 5) st.range = null
    }, 60)
  })
}

/** Removes what preparePage put in the page, and closes a transcript panel it opened. */
export function cleanupPage(): void {
  for (const el of Array.from(document.querySelectorAll("script[data-basalisk-clip]"))) el.remove()
  for (const el of Array.from(document.querySelectorAll("[data-basalisk-clip-mark]"))) {
    const name = el.getAttribute("data-basalisk-clip-mark")
    if (name) el.removeAttribute(name)
    el.removeAttribute("data-basalisk-clip-mark")
  }
  const w = window as unknown as Record<string, unknown>
  if (w.__BASALISK_CLIP_PANEL__) {
    delete w.__BASALISK_CLIP_PANEL__
    const panel = document.querySelector(
      'ytd-engagement-panel-section-list-renderer[target-id*="transcript"]',
    )
    const close = panel?.querySelector<HTMLElement>(
      '#visibility-button button, button[aria-label*="Close" i]',
    )
    if (close) close.click()
    else panel?.setAttribute("visibility", "ENGAGEMENT_PANEL_VISIBILITY_HIDDEN")
  }
  delete (window as unknown as Record<string, unknown>).__BASALISK_CLIP_PREP__
}

/** A function as a statement to evaluate in the page. */
export const asScript = (fn: () => void) => `(${fn.toString()})();0`
