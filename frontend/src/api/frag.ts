import { request } from "./account"

/** How owner captures reach the Fragments Engine (GET /api/frag). */
export interface FragState {
  mode: "exchange" | "personal" | "off"
  integration: { id: string; name: string } | null
}

export interface FragCapture {
  sequence: number
  captureID: string
  state: "queued" | "needs_attention" | "saved" | "discarded"
  code: string
  createdAt: string
  fragmentID?: string
  fragmentURL?: string
  delivering: boolean
  content: string
  tags: string[]
  context?: unknown
}

export const frag = {
  state: () => request<FragState>("frag"),
  capture: (body: {
    captureId: string
    content: string
    tags: string[]
    draftAt: string
    submittedAt: string
    context?: unknown
  }) => request<FragCapture>("frag/captures", "POST", body),
  list: () => request<{ items: FragCapture[] }>("frag/captures").then((r) => r.items),
}
