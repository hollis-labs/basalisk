import { useSurface } from "@hollis-labs/ade-react"
import { Button } from "@hollis-labs/design-components"
import { GenuiController, GenuiSurface, SurfaceFrame } from "@hollis-labs/genui"
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react"
import { hub } from "../../ade"
import {
  type GenuiEntry,
  genuiTransport,
  listGenui,
  loadGenui,
  showPreferences,
} from "../../api/genui"
import { useDesk } from "../../desktop/context"
import type { WindowProps } from "../../desktop/registry"
import { admitGenui } from "./binding"
import { genuiAdeSurface } from "./surface"

function CurrentSurface({
  controller,
  onNext,
  onPhase,
}: {
  controller: GenuiController
  onNext(id: string): void
  onPhase(phase: string): void
}) {
  const snapshot = useSyncExternalStore(
    controller.subscribe,
    controller.getSnapshot,
    controller.getSnapshot,
  )
  const routed = useRef(false)
  useEffect(() => onPhase(snapshot.phase), [onPhase, snapshot.phase])
  useEffect(() => {
    const observation = snapshot.observation
    if (routed.current || observation?.kind !== "known" || observation.outcome.status !== "ok")
      return
    const result = observation.outcome.result
    if (
      result &&
      typeof result === "object" &&
      !Array.isArray(result) &&
      typeof result.surfaceId === "string" &&
      result.surfaceId
    ) {
      routed.current = true
      onNext(result.surfaceId)
    }
  }, [snapshot, onNext])
  return (
    <SurfaceFrame mode="inline" title="Reviewed preferences" open onClose={() => {}}>
      <GenuiSurface controller={controller} />
    </SurfaceFrame>
  )
}

export function GenuiWindow({ winId, args }: WindowProps) {
  const desk = useDesk()
  const root = useRef<HTMLDivElement>(null)
  const active = useRef<GenuiController | null>(null)
  const loadingRequest = useRef<AbortController | null>(null)
  const mounted = useRef(false)
  const selected = useRef("")
  const initialId = useRef(typeof args.surfaceId === "string" ? args.surfaceId : "")
  const [entries, setEntries] = useState<GenuiEntry[]>([])
  const [controller, setController] = useState<GenuiController | null>(null)
  const [selectedId, setSelectedId] = useState("")
  const [phase, setPhase] = useState("empty")
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [discoveryError, setDiscoveryError] = useState(false)

  const openSurface = useCallback(
    async (id: string, supplied?: string) => {
      loadingRequest.current?.abort()
      const request = new AbortController()
      loadingRequest.current = request
      active.current?.dispose()
      active.current = null
      setController(null)
      setLoading(true)
      setError("")
      let next: GenuiController | undefined
      try {
        const current = await listGenui(request.signal)
        const entry = current.find((item) => item.id === id)
        if (!entry) throw new Error("This surface is no longer available.")
        const raw = supplied ?? (await loadGenui(id, request.signal))
        const envelope = admitGenui(raw)
        if (envelope.id !== id) throw new Error("The host returned a different surface.")
        next = new GenuiController(raw, genuiTransport)
        const pending = next
        request.signal.addEventListener("abort", () => pending.dispose(), { once: true })
        // Do not render ready controls for a recorded action while reconnecting.
        if (entry.actionId) {
          await next.recover(entry.actionId)
          if (next.getSnapshot().phase === "ready")
            throw new Error("The recorded action does not match this surface.")
        }
        if (!mounted.current || request.signal.aborted) {
          next.dispose()
          return
        }
        active.current = next
        selected.current = id
        setSelectedId(id)
        setEntries(current)
        setController(next)
        setPhase(next.getSnapshot().phase)
        setDiscoveryError(false)
        // The Desktop persists args. Keep only a routing ID, never the envelope or proof.
        desk.setArgs(winId, { surfaceId: id })
      } catch {
        next?.dispose()
        if (mounted.current && !request.signal.aborted) {
          setError(
            "This review could not be loaded or verified. Refresh the host state before acting.",
          )
          setPhase("unavailable")
        }
      } finally {
        if (mounted.current && !request.signal.aborted) setLoading(false)
      }
    },
    [desk, winId],
  )

  useEffect(() => {
    mounted.current = true
    let request: AbortController | undefined
    let running = false
    const refresh = async () => {
      if (running) return
      running = true
      request = new AbortController()
      try {
        const current = await listGenui(request.signal)
        if (request.signal.aborted) return
        setEntries(current)
        setDiscoveryError(false)
        if (!selected.current && !loadingRequest.current) {
          const id = initialId.current || current[0]?.id
          if (id) void openSurface(id)
          else setLoading(false)
        }
      } catch {
        if (!request.signal.aborted) {
          setDiscoveryError(true)
          if (!selected.current) setLoading(false)
        }
      } finally {
        running = false
      }
    }
    void refresh()
    // Discovery only: this never submits, confirms, or retries an operation.
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") void refresh()
    }, 5000)
    return () => {
      mounted.current = false
      window.clearInterval(timer)
      request?.abort()
      loadingRequest.current?.abort()
      loadingRequest.current = null
      active.current?.dispose()
      active.current = null
    }
  }, [openSurface])

  const selectSurface = useCallback(
    (id: string) => {
      void openSurface(id)
    },
    [openSurface],
  )
  const ade = useSurface(
    () =>
      genuiAdeSurface({
        winId,
        entries,
        selectedId,
        phase,
        root: root.current,
        select: selectSurface,
      }),
    hub,
  )
  useEffect(() => {
    void entries
    void selectedId
    void phase
    ade.changed()
  }, [ade, entries, selectedId, phase])

  const createReview = async () => {
    const request = new AbortController()
    loadingRequest.current?.abort()
    loadingRequest.current = request
    setLoading(true)
    setError("")
    try {
      const raw = await showPreferences(request.signal)
      if (!mounted.current || request.signal.aborted) return
      const envelope = admitGenui(raw)
      await openSurface(envelope.id, raw)
    } catch {
      if (!request.signal.aborted) {
        setError(
          "A new review could not be created. Check the retained surfaces before trying again.",
        )
        setLoading(false)
      }
    }
  }

  return (
    <section
      ref={root}
      tabIndex={-1}
      className="flex h-full min-h-0 flex-col gap-4 overflow-auto bg-bg p-4 text-fg"
      aria-label="GenUI workspace"
    >
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold">GenUI</h1>
          <p className="text-control text-fg-muted">
            Review account theme and timezone changes before the host applies them.
          </p>
        </div>
        <div className="flex gap-2">
          <Button
            type="button"
            variant="outline"
            disabled={loading}
            onClick={() => {
              if (selectedId) void openSurface(selectedId)
              else void createReview()
            }}
          >
            {selectedId ? "Refresh current review" : "Review preferences"}
          </Button>
          {selectedId ? (
            <Button
              type="button"
              disabled={loading}
              onClick={() => {
                void createReview()
              }}
            >
              New preferences review
            </Button>
          ) : null}
        </div>
      </header>
      <p className="text-caption text-fg-muted">
        Confirmation is an authenticated, one-use acknowledgment of the exact review. It is not
        reauthentication or MFA. Closing this window does not cancel a submitted action.
      </p>
      {discoveryError ? (
        <p role="alert">
          The retained surface list is unavailable. Existing outcomes have not been retried.
        </p>
      ) : null}
      <div className="grid min-h-0 gap-4 md:grid-cols-[16rem_minmax(0,1fr)]">
        <nav aria-label="Retained reviews" className="flex flex-col gap-2">
          {entries.map((entry) => (
            <Button
              key={entry.id}
              type="button"
              data-genui-id={entry.id}
              variant={entry.id === selectedId ? "secondary" : "ghost"}
              aria-current={entry.id === selectedId ? "page" : undefined}
              onClick={() => selectSurface(entry.id)}
            >
              {entry.title}
            </Button>
          ))}
          {!entries.length && !loading ? (
            <p role="status" className="text-control text-fg-muted">
              No retained reviews. Create one or ask the agent to show a preferences form.
            </p>
          ) : null}
        </nav>
        <main aria-label="Current review" className="min-w-0 rounded-md border border-border p-4">
          {loading ? (
            <p role="status">Loading the host's current review…</p>
          ) : error ? (
            <p role="alert">{error}</p>
          ) : controller ? (
            <CurrentSurface
              key={selectedId}
              controller={controller}
              onNext={selectSurface}
              onPhase={setPhase}
            />
          ) : (
            <p role="status">Select a retained review or start a new one.</p>
          )}
        </main>
      </div>
    </section>
  )
}
