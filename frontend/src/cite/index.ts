/**
 * Cite: references in any text (a Torque task id, a Tesseract item, a URL) become chips with a
 * preview card, everywhere. Detection is cite/core (lifted from Tachyon); resolution goes to
 * our backend (/api/cite), which reads the records through the Tether gateway.
 *
 * This module is the one instance the whole desktop shares: the kind registry, the resolver
 * cache, the hover card's state, and what "open" means.
 */
import { useSyncExternalStore } from "react"
import { request } from "../api/account"
import { openLink } from "../api/browser"
import { detect, detectRefs } from "./core/detect"
import { createPathKind } from "./core/kinds/path"
import { createTesseractKinds } from "./core/kinds/tesseract"
import { createTetherKinds } from "./core/kinds/tether"
import { createTorqueKinds } from "./core/kinds/torque"
import { createUrlKind } from "./core/kinds/url"
import { createRefRegistry } from "./core/registry"
import { createRefResolverClient, type RefResolverClient } from "./core/resolver"
import type {
  KindResolver,
  RefEntity,
  RefIdentity,
  RefState,
  ResolveOutcome,
  Segment,
} from "./core/types"
import { createFragmentKind } from "./fragment"

export type { RefEntity, RefIdentity, RefState, Segment }

/** An entity as /api/cite/record returns it: the preview plus the full text. */
export interface CiteRecord extends RefEntity {
  body?: string
}

export const registry = createRefRegistry()
for (const kind of [
  createUrlKind(),
  ...createTesseractKinds(),
  ...createTorqueKinds(),
  ...createTetherKinds(),
  createPathKind(),
  createFragmentKind(), // fragment.ts
]) {
  registry.register(kind)
}

function liveResolver(kind: string): KindResolver {
  return {
    async resolveMany(canonicals) {
      const q = `cite/resolve?kind=${encodeURIComponent(kind)}&ids=${canonicals.map(encodeURIComponent).join(",")}`
      const out = await request<{ results: Record<string, RefEntity | null> }>(q)
      const mapped: Record<string, ResolveOutcome | undefined> = {}
      for (const id of canonicals) {
        const entity = out.results[id]
        mapped[id] = entity ? { status: "found", entity } : { status: "not-found" }
      }
      return mapped
    },
  }
}

// Until the server says which kinds it resolves, nothing does: chips link, without a preview.
let client: RefResolverClient = createRefResolverClient({ resolvers: {} })
let resolvable = new Set<string>()
let version = 0
const listeners = new Set<() => void>()
const emit = () => {
  version++
  for (const fn of listeners) fn()
}
let unsubscribe = client.subscribe(emit)
let started: Promise<void> | null = null

/** Ask the server what it can resolve. Called once when the desktop mounts. */
export function initCite(): Promise<void> {
  started ??= request<{ enabled: boolean; kinds: string[] }>("cite")
    .then((state) => {
      if (!state.enabled) return
      resolvable = new Set(state.kinds)
      unsubscribe()
      client.dispose()
      client = createRefResolverClient({
        resolvers: Object.fromEntries(state.kinds.map((k) => [k, liveResolver(k)])),
      })
      unsubscribe = client.subscribe(emit)
      emit()
    })
    .catch(() => undefined)
  return started
}

/** Whether a kind has a record behind it that can be previewed and opened. */
export const canResolve = (kind: string) => resolvable.has(kind)

const subscribe = (fn: () => void) => {
  listeners.add(fn)
  return () => {
    listeners.delete(fn)
  }
}

/** The state of one reference; asks for it to be resolved when `fetch` is set. */
export function useRefState(kind: string, canonical: string, fetch: boolean): RefState {
  useSyncExternalStore(subscribe, () => version)
  const state = client.get(kind, canonical)
  if (fetch && state.status === "idle") queueMicrotask(() => client.request([{ kind, canonical }]))
  return state
}

/** Text split into plain and reference segments. */
export const segments = (text: string): Segment[] => detect(text, registry)

/** The references in a string. `resolvableOnly` keeps those with a record behind them. */
export function refsIn(text: string, resolvableOnly = false) {
  const refs = detectRefs(text, registry)
  return resolvableOnly ? refs.filter((r) => canResolve(r.kind)) : refs
}

export const kindLabel = (kind: string) => registry.get(kind)?.label ?? kind

export const loadRecord = (ref: RefIdentity) =>
  request<CiteRecord>(
    `cite/record?kind=${encodeURIComponent(ref.kind)}&id=${encodeURIComponent(ref.canonical)}`,
  )

// ---- What activating a reference does. The desktop installs this when it mounts. ----

let opener: (ref: RefIdentity) => void = () => undefined
export function setCiteOpener(fn: (ref: RefIdentity) => void) {
  opener = fn
}

/** Open a reference: its record in a viewer window, a URL in the browser, a path copied. */
export function openCite(ref: RefIdentity) {
  hideCard(true)
  if (ref.kind === "url") {
    openLink(ref.canonical)
    return
  }
  if (canResolve(ref.kind)) {
    opener(ref)
    return
  }
  void navigator.clipboard?.writeText(ref.canonical).catch(() => undefined)
}

// ---- The hover card: one for the whole desktop, shown beside whatever is hovered. ----

export interface CardAnchor {
  left: number
  top: number
  right: number
  bottom: number
}
export interface CardState {
  ref: RefIdentity
  anchor: CardAnchor
}

let card: CardState | null = null
let cardTimer = 0
const cardListeners = new Set<() => void>()
const setCard = (next: CardState | null) => {
  card = next
  for (const fn of cardListeners) fn()
}

/** Show the card for a reference after a short pause (or at once). */
export function showCard(ref: RefIdentity, anchor: CardAnchor, delay = 320) {
  window.clearTimeout(cardTimer)
  cardTimer = window.setTimeout(() => {
    client.request([{ kind: ref.kind, canonical: ref.canonical }])
    setCard({ ref, anchor })
  }, delay)
}

/** Hide the card, after a grace period that lets the pointer travel into it. */
export function hideCard(now = false) {
  window.clearTimeout(cardTimer)
  if (now) setCard(null)
  else cardTimer = window.setTimeout(() => setCard(null), 180)
}

/** The pointer is over the card itself: keep it. */
export function holdCard() {
  window.clearTimeout(cardTimer)
}

export const useCard = () =>
  useSyncExternalStore(
    (fn) => {
      cardListeners.add(fn)
      return () => {
        cardListeners.delete(fn)
      }
    },
    () => card,
  )
