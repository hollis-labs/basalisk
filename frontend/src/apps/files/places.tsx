import { useEffect, useRef, useState } from "react"
import { type FileRoot, files } from "../../api/files"

/** Places changed (added or removed): every Files window reads them again. */
export const ROOTS_EVENT = "basalisk:files-roots"

/** Add a remote place: a host from the ssh config and a folder there. */
export function AddPlace({
  onAdded,
  onCancel,
}: {
  onAdded: (r: FileRoot) => void
  onCancel: () => void
}) {
  const [hosts, setHosts] = useState<string[]>([])
  const [host, setHost] = useState("")
  const [path, setPath] = useState("~/")
  const [label, setLabel] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const hostRef = useRef<HTMLInputElement>(null)
  useEffect(() => {
    hostRef.current?.focus()
    void files
      .hosts()
      .then(setHosts)
      .catch(() => undefined)
  }, [])
  const add = async () => {
    if (!host.trim() || !path.trim()) return
    setBusy(true)
    setError("")
    try {
      onAdded(await files.addRoot(host.trim(), path.trim(), label.trim()))
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not add it.")
    } finally {
      setBusy(false)
    }
  }
  return (
    <form
      className="files-add-place"
      onSubmit={(e) => {
        e.preventDefault()
        void add()
      }}
      onKeyDown={(e) => {
        e.stopPropagation()
        if (e.key === "Escape") onCancel()
      }}
    >
      <input
        ref={hostRef}
        className="desk-input"
        list="files-ssh-hosts"
        value={host}
        placeholder="host (from ~/.ssh/config)"
        aria-label="Host"
        spellCheck={false}
        onChange={(e) => setHost(e.target.value)}
      />
      <datalist id="files-ssh-hosts">
        {hosts.map((h) => (
          <option key={h} value={h} />
        ))}
      </datalist>
      <input
        className="desk-input"
        value={path}
        aria-label="Folder"
        placeholder="~/dev"
        spellCheck={false}
        onChange={(e) => setPath(e.target.value)}
      />
      <input
        className="desk-input"
        value={label}
        aria-label="Name (optional)"
        placeholder="name (optional)"
        onChange={(e) => setLabel(e.target.value)}
      />
      {error && <p className="files-add-error">{error}</p>}
      <div className="files-add-actions">
        <button type="button" className="desk-button" onClick={onCancel}>
          Cancel
        </button>
        <button
          type="submit"
          className="desk-button"
          disabled={busy || !host.trim() || !path.trim()}
        >
          Add place
        </button>
      </div>
    </form>
  )
}
