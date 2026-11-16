import { type GenuiTransport, parseEnvelope, parseRecovery } from "@hollis-labs/genui"

/** Discovery contains routing metadata only. Canonical surfaces are fetched as original bytes. */
export interface GenuiEntry {
  id: string
  title: string
  actionId?: string
}

async function text(response: Response, limit: number): Promise<string> {
  if (!response.body) throw new Error("The host returned no content.")
  const reader = response.body.getReader()
  const chunks: Uint8Array[] = []
  let size = 0
  try {
    for (;;) {
      const next = await reader.read()
      if (next.done) break
      size += next.value.byteLength
      if (size > limit) throw new Error("The host response exceeds the supported limit.")
      chunks.push(next.value)
    }
  } finally {
    await reader.cancel()
    reader.releaseLock()
  }
  const bytes = new Uint8Array(size)
  let offset = 0
  for (const chunk of chunks) {
    bytes.set(chunk, offset)
    offset += chunk.byteLength
  }
  return new TextDecoder("utf-8", { fatal: true }).decode(bytes)
}

async function raw(
  path: string,
  signal: AbortSignal,
  body?: string,
  protocol = false,
  limit = 256 << 10,
): Promise<string> {
  const response = await fetch(`/api/genui/${path}`, {
    method: body === undefined ? "GET" : "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", "X-Requested-With": "XMLHttpRequest" },
    body,
    signal,
  })
  if (response.status === 401) window.dispatchEvent(new Event("basalisk:unauthorized"))
  const value = await text(response, limit)
  // Native protocol errors may use a non-2xx HTTP status. The shared controller validates
  // the entire response, correlation and outcome; a status alone never grants success.
  if (!response.ok && !protocol) throw new Error("The host could not load this workspace.")
  return value
}

function record(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value)
}
function identifier(value: unknown): value is string {
  return typeof value === "string" && value.length > 0 && [...value].length <= 256
}

export async function listGenui(signal: AbortSignal): Promise<GenuiEntry[]> {
  const value: unknown = JSON.parse(await raw("surfaces", signal))
  if (
    !record(value) ||
    Object.keys(value).some((key) => key !== "surfaces") ||
    !Array.isArray(value.surfaces) ||
    value.surfaces.length > 256
  )
    throw new Error("Invalid workspace discovery response.")
  const seen = new Set<string>()
  return value.surfaces.map((item: unknown) => {
    if (
      !record(item) ||
      Object.keys(item).some((key) => !["id", "title", "actionId"].includes(key)) ||
      !identifier(item.id) ||
      typeof item.title !== "string" ||
      item.title.length > 1024 ||
      (item.actionId !== undefined && !identifier(item.actionId)) ||
      seen.has(item.id)
    )
      throw new Error("Invalid workspace entry.")
    seen.add(item.id)
    return {
      id: item.id,
      title: item.title,
      ...(item.actionId === undefined ? {} : { actionId: item.actionId }),
    }
  })
}

export async function loadGenui(id: string, signal: AbortSignal): Promise<string> {
  const result = await raw(`surfaces/${encodeURIComponent(id)}`, signal)
  if (parseEnvelope(result).id !== id) throw new Error("The host returned a different surface.")
  return result
}

export async function showPreferences(signal: AbortSignal): Promise<string> {
  const result = await raw("surfaces", signal, JSON.stringify({ pattern: "form", params: {} }))
  parseEnvelope(result)
  return result
}

export const genuiTransport: GenuiTransport = {
  submit: (call, signal) => raw("actions", signal, call, true, 64 << 10),
  recover: async (request, signal) =>
    parseRecovery(
      await raw(
        `actions/${encodeURIComponent(request.actionId)}?${new URLSearchParams({ functionCallId: request.functionCallId })}`,
        signal,
        undefined,
        true,
      ),
      request.functionCallId,
    ),
  confirm: async (request, signal) => {
    // Explicit kit confirmation button invokes this authenticated host acknowledgment.
    // This is not reauthentication or MFA. The server reloads the exact stored challenge.
    const value: unknown = JSON.parse(await raw("confirm", signal, request.call, false, 16 << 10))
    if (
      !record(value) ||
      Object.keys(value).length !== 1 ||
      typeof value.confirmation !== "string" ||
      !value.confirmation ||
      value.confirmation.length > 8192
    )
      throw new Error("The confirmation could not be verified.")
    return value.confirmation
  },
}
