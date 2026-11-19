/**
 * Approvals from Gatecrasher (internal/gatecrasher): the cards that wait on the owner, kept
 * on the server as Gatecrasher's stream brings them, and read here by a long poll so a new
 * one shows within a moment. Decisions go back through the server, which holds the owner's
 * Gatecrasher credential.
 *
 * A request_secret card is different: its value is posted from this page straight to the
 * card's intake URL (provideSecret). It never goes to Basalisk's server, the agent, a log or
 * any store, and the field is cleared as soon as it is sent.
 */
import { useSyncExternalStore } from "react"
import { ApiError, request } from "../api/account"

export interface GateAction {
  kind: "approve" | "deny" | "choose" | "answer" | "provide_secret"
  label: string
  outcome?: "approved" | "denied"
  decide?: string
}

export interface GateCardBody {
  version: string
  approval_id: string
  type: "approve" | "choose" | "answer" | "request_secret"
  status: string
  tier: "ground" | "elevated" | "critical"
  kind: string
  source: string
  title: string
  summary?: string
  principal: { sub: string; kind: string }
  requested?: { audience: string; scopes: string[]; ttl_seconds: number }
  evidence?: Record<string, unknown>
  prompt?: { question: string; choices?: { id: string; label: string }[] }
  expires_at: string
  deciders: string
  actions?: GateAction[]
  secret?: {
    intake_id: string
    post_url: string
    field: string
    expires_at: string
    write_only: boolean
  }
  outcome?: {
    status: string
    decider?: { sub: string; kind: string }
    response?: { choice?: string; answer?: string; secret_ref?: string }
  }
}

export interface GateCard {
  id: string
  status: string
  updated: string
  card: GateCardBody
}

interface GateState {
  /** False when this Basalisk has no Gatecrasher configured. */
  on: boolean
  /** Whether the stream from Gatecrasher is up. */
  online: boolean
  cards: GateCard[]
  waiting: number
}

let current: GateState = { on: false, online: false, cards: [], waiting: 0 }
let version = -1
const listeners = new Set<() => void>()
const tell = () => {
  for (const fn of listeners) fn()
}

let started = false
/** Begin following the cards (once, when the desktop is up). */
export function watchGate() {
  if (started) return
  started = true
  void loop()
}

async function loop() {
  for (;;) {
    try {
      const r = await request<{
        cards: GateCard[]
        version: number
        online: boolean
        waiting: number
      }>(version < 0 ? "gate/cards" : `gate/cards?v=${version}`)
      version = r.version
      current = { on: true, online: r.online, cards: r.cards ?? [], waiting: r.waiting ?? 0 }
      tell()
    } catch (err) {
      // 404: no Gatecrasher configured here. Look again now and then, in case it is set.
      const off = err instanceof ApiError && err.status === 404
      if (off && current.on) {
        current = { on: false, online: false, cards: [], waiting: 0 }
        tell()
      }
      await new Promise((r) => setTimeout(r, off ? 60_000 : 5_000))
    }
  }
}

const subscribe = (fn: () => void) => {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

export const useGate = () => useSyncExternalStore(subscribe, () => current)

/** Listen for changes outside React (the Dock badge). */
export const onGate = subscribe
export const gateWaiting = () => current.waiting

const put = (c: GateCard) => {
  const at = current.cards.findIndex((x) => x.id === c.id)
  const cards = at < 0 ? [c, ...current.cards] : current.cards.map((x) => (x.id === c.id ? c : x))
  current = { ...current, cards, waiting: cards.filter((x) => x.status === "pending").length }
  tell()
}

/** Decide a card. Returns "" or what went wrong. */
export async function decide(
  id: string,
  body: { outcome: "approved" | "denied"; choice?: string; answer?: string; note?: string },
): Promise<string> {
  try {
    put(await request<GateCard>(`gate/cards/${encodeURIComponent(id)}/decide`, "POST", body))
    return ""
  } catch (err) {
    void request<GateCard>(`gate/cards/${encodeURIComponent(id)}/refresh`, "POST", {})
      .then(put)
      .catch(() => undefined)
    return err instanceof ApiError ? err.message : "The decision could not be sent."
  }
}

/**
 * Post a secret straight to the card's intake, from this page. The intake is another
 * origin and need not let this page read its answer, so the post is sent without reading
 * one; the card closes when the producer tells Gatecrasher the secret arrived. It is never
 * sent twice: an intake takes one value.
 */
export async function provideSecret(
  form: NonNullable<GateCardBody["secret"]>,
  value: string,
): Promise<string> {
  if (!/^https?:\/\//.test(form.post_url)) return "The card's intake address is not a web address."
  const body = new URLSearchParams()
  body.set(form.field || "value", value)
  try {
    await fetch(form.post_url, {
      method: "POST",
      mode: "no-cors",
      credentials: "omit",
      cache: "no-store",
      referrerPolicy: "no-referrer",
      body,
    })
    return ""
  } catch {
    return "The intake could not be reached. Nothing was kept here; ask the producer for a new request."
  }
}
