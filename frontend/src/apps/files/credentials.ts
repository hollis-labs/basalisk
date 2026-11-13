/**
 * Files the agent never reads (files_read, files_grep): what it reads goes to the model's
 * provider, so anything that looks like a credential stays on this machine. No imports, so it
 * can be checked on its own.
 */

const SECRET_NAMES = new Set([
  ".env",
  ".netrc",
  ".npmrc",
  ".pypirc",
  "htpasswd",
  ".htpasswd",
  "agent-key",
  "fe-api-token",
])
const SECRET_EXTS = [".pem", ".key", ".p12", ".pfx", ".keystore", ".token"]
const SECRET_PREFIXES = [".env.", "id_rsa", "id_ed25519", "id_ecdsa", "credentials"]
const SECRET_WORDS = ["token", "secret"]
const SECRET_DIRS = [
  "/.ssh/",
  "/.gnupg/",
  "/.aws/",
  "/.kube/",
  "/.docker/",
  "/.config/basalisk/",
  "/.config/station/",
  "/library/keychains/",
  "/var/lib/fabric/",
]

/**
 * Why a file must not be read by the agent (it looks like a credential), or null. `base` is
 * the place's own folder, so a path is judged where it really is.
 */
export function credentialReason(path: string, base = ""): string | null {
  const name = (path.split("/").pop() ?? "").toLowerCase()
  const full = `/${[base, path].filter(Boolean).join("/")}/`.replace(/\/+/g, "/").toLowerCase()
  if (SECRET_NAMES.has(name)) return `its name (${name})`
  if (SECRET_PREFIXES.some((p) => name.startsWith(p))) return `its name (${name})`
  if (SECRET_EXTS.some((e) => name.endsWith(e))) return `its name (${name})`
  if (SECRET_WORDS.some((w) => name.includes(w))) return `its name (${name})`
  const dir = SECRET_DIRS.find((d) => full.includes(d))
  if (dir) return `where it is (under ${dir.slice(1, -1)})`
  return null
}
