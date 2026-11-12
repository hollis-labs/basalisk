export interface Profile {
  username: string
  displayName: string
  email: string
  theme: "dark" | "light" | "system"
  timezone: string
  revision: number
}
export interface Integration {
  id: string
  name: string
  provider: string
  baseUrl: string
  updatedAt: string
  credentialPresent: boolean
}

/** Raised for any non-2xx answer. `code` is the flat apierr code, when the body carried one. */
export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
    public code = "",
  ) {
    super(message)
  }
}

/** Codes where a 401 means "wrong credentials", not "your session ended". */
const CREDENTIAL_CODES = new Set(["invalid_login"])

/**
 * JSON over the same-origin cookie session. Errors are the flat `{code, message, details?}`
 * body. Every request carries X-Requested-With, which the backend requires on non-GET calls.
 * An empty body (204) resolves to undefined.
 */
export async function request<T>(path: string, method = "GET", body?: unknown): Promise<T> {
  const response = await fetch(`/api/${path}`, {
    method,
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", "X-Requested-With": "XMLHttpRequest" },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await response.text()
  let data: { code?: string; message?: string } | null = null
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = null
    }
  }
  if (!response.ok) {
    const code = data?.code ?? ""
    if (response.status === 401 && !CREDENTIAL_CODES.has(code))
      window.dispatchEvent(new Event("basalisk:unauthorized"))
    throw new ApiError(
      data?.message || "The request could not be completed.",
      response.status,
      code,
    )
  }
  return data as T
}

export const loadAccount = () => request<Profile>("account")
