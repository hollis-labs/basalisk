import { useEffect, useRef, useState } from "react"

export interface MenuItem {
  label: string
  keys?: string
  danger?: boolean
  disabled?: boolean
  run: () => void
}

/** A context menu at a point; it closes on a choice, Escape, or a click elsewhere. */
export function ContextMenu({
  x,
  y,
  items,
  onClose,
}: {
  x: number
  y: number
  items: (MenuItem | "-")[]
  onClose: () => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState({ x, y })
  useEffect(() => {
    const el = ref.current
    if (el) {
      // Keep it on screen.
      const r = el.getBoundingClientRect()
      setPos({
        x: Math.min(x, window.innerWidth - r.width - 8),
        y: Math.min(y, window.innerHeight - r.height - 8),
      })
      el.querySelector<HTMLButtonElement>("button:not(:disabled)")?.focus()
    }
    const away = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) onClose()
    }
    window.addEventListener("mousedown", away, true)
    return () => window.removeEventListener("mousedown", away, true)
  }, [x, y, onClose])
  return (
    <div
      ref={ref}
      className="files-menu"
      role="menu"
      style={{ left: pos.x, top: pos.y }}
      onKeyDown={(e) => {
        const buttons = [
          ...(ref.current?.querySelectorAll<HTMLButtonElement>("button:not(:disabled)") ?? []),
        ]
        const at = buttons.indexOf(document.activeElement as HTMLButtonElement)
        if (e.key === "Escape") onClose()
        else if (e.key === "ArrowDown") buttons[(at + 1) % buttons.length]?.focus()
        else if (e.key === "ArrowUp") buttons[(at - 1 + buttons.length) % buttons.length]?.focus()
        else return
        e.preventDefault()
        e.stopPropagation()
      }}
    >
      {items.map((it, i) =>
        it === "-" ? (
          // biome-ignore lint/suspicious/noArrayIndexKey: separators have no identity
          <hr key={`sep-${i}`} />
        ) : (
          <button
            key={it.label}
            type="button"
            role="menuitem"
            disabled={it.disabled}
            data-danger={it.danger}
            onClick={() => {
              onClose()
              it.run()
            }}
          >
            <span>{it.label}</span>
            {it.keys && <kbd>{it.keys}</kbd>}
          </button>
        ),
      )}
    </div>
  )
}

/** The name field for a rename or a new item: Enter commits, Escape cancels. */
export function NameEditor({
  initial,
  onCommit,
  onCancel,
}: {
  initial: string
  onCommit: (name: string) => void
  onCancel: () => void
}) {
  const [value, setValue] = useState(initial)
  const ref = useRef<HTMLInputElement>(null)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    el.focus()
    // Select the name without its extension, as Finder does.
    const dot = initial.lastIndexOf(".")
    el.setSelectionRange(0, dot > 0 ? dot : initial.length)
  }, [initial])
  const done = useRef(false)
  const commit = () => {
    if (done.current) return
    done.current = true
    const v = value.trim()
    if (v && v !== initial && !/[/\\]/.test(v) && v !== "." && v !== "..") onCommit(v)
    else onCancel()
  }
  return (
    <input
      ref={ref}
      className="files-name-edit"
      value={value}
      spellCheck={false}
      aria-label="Name"
      onChange={(e) => setValue(e.target.value)}
      onBlur={commit}
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        e.stopPropagation()
        if (e.key === "Enter") commit()
        else if (e.key === "Escape") {
          done.current = true
          onCancel()
        }
      }}
    />
  )
}
