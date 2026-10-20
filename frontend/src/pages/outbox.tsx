import { Button } from "@hollis-labs/design-components"
import { useCallback, useEffect, useRef, useState } from "react"
import { request } from "../api/account"
import { Notice } from "../components/forms"

type Submission = {
  sequence: number
  captureID: string
  deviceID: string
  state: string
  code: string
  createdAt: string
  fragmentID?: string
  fragmentURL?: string
  nextAttempt?: number
  mayHaveSent: boolean
  delivering: boolean
}
type Detail = { content: string; tags: string[] }
type Page = { items: Submission[]; nextBefore: number }

const deliveryMessages: Record<string, string> = {
  delivery_unavailable: "Delivery is awaiting setup. Your note is retained.",
  authorization_required: "Service access needs attention. After renewing access, retry this note.",
  device_authorization_required:
    "This Mac’s capture permission expired or was revoked. Your note is retained.",
  connection_changed:
    "The original connection is no longer authorized. Restore it before retrying.",
  credential_unavailable: "The delivery credential is unavailable. Your note is retained.",
  invalid_issuer_response: "The issued credential does not match the configured service identity.",
  invalid_receipt: "FE returned an unexpected receipt. Delivery has paused for review.",
  capture_conflict: "FE reports a conflicting capture. Automatic delivery has stopped.",
  capture_rejected: "FE could not accept this capture. Your note is retained for review.",
  connection_unavailable: "Could not reach the service. Basalisk will retry automatically.",
  service_unavailable: "The service is unavailable. Basalisk will retry automatically.",
  service_busy: "The service is busy. Basalisk will retry automatically.",
  storage_unavailable: "Delivery could not be recorded. Basalisk will retry safely.",
  owner_stopped_delivery: "Further delivery stopped. FE may already have accepted this note.",
  capture_stopped_delivery:
    "The capture app stopped delivery. FE may already have accepted this note.",
  capture_discarded: "The capture app stopped this unsent note.",
}

export function OutboxPage() {
  const [page, setPage] = useState<Page>({ items: [], nextBefore: 0 })
  const [error, setError] = useState("")
  const [message, setMessage] = useState("")
  const [busy, setBusy] = useState(false)
  const [selected, setSelected] = useState<number | null>(null)
  const [detail, setDetail] = useState<Detail | null>(null)
  const expanded = useRef(false)
  const refresh = useCallback(async (before = 0) => {
    expanded.current = before > 0
    setBusy(true)
    try {
      const next = await request<Page>(`fragment-submissions${before ? `?before=${before}` : ""}`)
      setPage((old) =>
        before ? { items: [...old.items, ...next.items], nextBefore: next.nextBefore } : next,
      )
      setError("")
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not read outbox.")
    } finally {
      setBusy(false)
    }
  }, [])
  useEffect(() => {
    void refresh()
    const timer = window.setInterval(() => {
      if (!expanded.current) void refresh()
    }, 10000)
    return () => window.clearInterval(timer)
  }, [refresh])
  async function open(id: number) {
    setBusy(true)
    setSelected(null)
    setDetail(null)
    try {
      const result = await request<{ submission: Detail | null }>(`fragment-submissions/${id}`)
      setSelected(id)
      setDetail(result.submission)
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not read note.")
    } finally {
      setBusy(false)
    }
  }
  async function retry(id: number) {
    setBusy(true)
    try {
      await request(`fragment-submissions/${id}/retry`, "POST")
      setMessage("Retry requested. Delivery will use the original capture identity.")
      await refresh()
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not retry note.")
    } finally {
      setBusy(false)
    }
  }
  async function discard(item: Submission) {
    if (
      !window.confirm(
        item.mayHaveSent
          ? "Stop further delivery and discard the local note? FE may already have accepted it. This does not delete anything in FE. Copy anything you want to keep first."
          : "Discard this pending note? It will not be sent to Fragments Engine. Copy anything you want to keep first.",
      )
    )
      return
    setBusy(true)
    try {
      await request(`fragment-submissions/${item.sequence}`, "DELETE")
      setSelected(null)
      setDetail(null)
      setMessage(
        item.mayHaveSent
          ? "Further delivery stopped. Any existing FE copy is unchanged."
          : "Note discarded. It will not be sent.",
      )
      await refresh()
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not discard note.")
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="page-content">
      <div className="page-intro">
        <p className="eyebrow">CAPTURE</p>
        <h1>Fragment outbox</h1>
        <p>
          Queued notes are retained here until delivery is confirmed. An active work session is
          optional.
        </p>
      </div>
      <Notice error={error} message={message} />
      <Button onClick={() => void refresh()} disabled={busy}>
        Refresh
      </Button>
      <div className="stack">
        {page.items.length === 0 && (
          <div className="panel">
            <p>No submissions yet.</p>
          </div>
        )}
        {page.items.map((item) => (
          <section className="panel stack" key={item.sequence}>
            <div>
              <strong>
                {item.state === "queued"
                  ? item.delivering
                    ? "Sending · awaiting confirmation"
                    : "Queued · awaiting confirmation"
                  : item.state === "saved"
                    ? "Saved to FE"
                    : item.state === "discarded"
                      ? "Discarded"
                      : "Needs attention"}
              </strong>
              <p className="text-fg-muted">
                {new Date(item.createdAt).toLocaleString()} · {item.captureID}
              </p>
            </div>
            {deliveryMessages[item.code] && <p>{deliveryMessages[item.code]}</p>}
            {item.state === "queued" && !item.delivering && !!item.nextAttempt && (
              <p className="text-fg-muted">
                Next attempt: {new Date(item.nextAttempt * 1000).toLocaleTimeString()}
              </p>
            )}
            {item.state === "saved" && item.fragmentURL && (
              <a href={item.fragmentURL} target="_blank" rel="noreferrer">
                Open saved fragment in FE
              </a>
            )}
            {item.state !== "discarded" && item.state !== "saved" && (
              <div className="flex gap-3">
                <Button onClick={() => void open(item.sequence)} disabled={busy}>
                  Review note
                </Button>
                <Button
                  onClick={() => void retry(item.sequence)}
                  disabled={busy || item.delivering}
                >
                  Retry delivery
                </Button>
                <Button
                  variant="ghost"
                  onClick={() => void discard(item)}
                  disabled={busy || item.delivering}
                >
                  {item.mayHaveSent ? "Stop delivery…" : "Discard…"}
                </Button>
              </div>
            )}
            {selected === item.sequence && detail && (
              <div className="stack">
                <pre className="whitespace-pre-wrap break-words">{detail.content}</pre>
                <p>{detail.tags.join(", ")}</p>
                <Button
                  onClick={() => {
                    void navigator.clipboard.writeText(detail.content).then(
                      () => setMessage("Note copied."),
                      () => setError("Could not copy. Select the note text and copy it manually."),
                    )
                  }}
                >
                  Copy note
                </Button>
                <Button
                  variant="ghost"
                  onClick={() => {
                    setSelected(null)
                    setDetail(null)
                  }}
                >
                  Close preview
                </Button>
              </div>
            )}
          </section>
        ))}
        {page.nextBefore > 0 && (
          <Button onClick={() => void refresh(page.nextBefore)} disabled={busy}>
            Older submissions
          </Button>
        )}
      </div>
    </div>
  )
}
