import { useEffect, useRef, useState } from "react"
import { TerminalSlot, useTerminalHost } from "../terminal/host"

/** The Terminal page: the shared deck, filling what is left of the window. */
export function TerminalPage() {
  const { enabled } = useTerminalHost()
  const frame = useRef<HTMLDivElement>(null)
  const [height, setHeight] = useState(480)

  // The page frame scrolls and has no fixed height, so the terminal measures the space
  // below its own top edge.
  useEffect(() => {
    const measure = () => {
      const el = frame.current
      if (!el) return
      setHeight(Math.max(240, Math.floor(window.innerHeight - el.getBoundingClientRect().top - 16)))
    }
    measure()
    window.addEventListener("resize", measure)
    const observer = new ResizeObserver(measure)
    observer.observe(document.body)
    return () => {
      window.removeEventListener("resize", measure)
      observer.disconnect()
    }
  }, [])

  if (enabled === null) return <p className="text-fg-muted">Opening the terminal…</p>
  if (!enabled)
    return (
      <div className="panel">
        <h2>The terminal runs in the desktop app</h2>
        <p className="text-fg-muted">
          This Basalisk is a server deployment, which does not hand out a shell. Open Basalisk.app
          on your Mac to use the terminal.
        </p>
      </div>
    )
  return (
    <div ref={frame} className="terminal-page" style={{ height }}>
      <TerminalSlot owner="page" className="terminal-slot" />
    </div>
  )
}
