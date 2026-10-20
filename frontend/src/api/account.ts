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
export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
  ) {
    super(message)
  }
}
export async function request<T>(path: string, method = "GET", body?: unknown): Promise<T> {
  const response = await fetch(`/api/${path}`, {
    method,
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", "X-Basalisk-Request": "1" },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (response.status === 401 && path !== "login")
    window.dispatchEvent(new Event("basalisk:unauthorized"))
  const data = await response.json()
  if (!response.ok)
    throw new ApiError(data.error || "The request could not be completed.", response.status)
  return data as T
}
