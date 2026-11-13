import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react"
import { createPortal } from "react-dom"
import { terminal as api } from "../api/shell"
import { TerminalDeck } from "./deck"

/**
 * One terminal deck for the whole app. Its DOM lives in a node that is never destroyed and
 * is moved to wherever the terminal is shown: the Terminal window, or the drop-down overlay
 * (the "Toggle terminal" command). Moving the node, instead of mounting a second deck, keeps every xterm, its
 * scrollback and its socket across navigation.
 */
interface TerminalHostValue {
  /** null while unknown; false in a deployment without a terminal. */
  enabled: boolean | null
  /** Place the deck in this element (null returns it to parking). */
  adopt: (owner: string, el: HTMLElement | null) => void
  overlayOpen: boolean
  setOverlayOpen: (open: boolean) => void
}

const Ctx = createContext<TerminalHostValue | null>(null)

export function useTerminalHost(): TerminalHostValue {
  const value = useContext(Ctx)
  if (!value) throw new Error("useTerminalHost outside TerminalHost")
  return value
}

export function TerminalHost({ children }: { children: ReactNode }) {
  const [node] = useState(() => {
    const el = document.createElement("div")
    el.className = "terminal-root"
    return el
  })
  const parking = useRef<HTMLDivElement>(null)
  const owners = useRef(new Map<string, HTMLElement>())
  const [enabled, setEnabled] = useState<boolean | null>(null)
  const [overlayOpen, setOverlayOpen] = useState(false)
  const [pageMounted, setPageMounted] = useState(false)
  const [epoch, setEpoch] = useState(0)
  const [started, setStarted] = useState(false)

  useEffect(() => {
    parking.current?.appendChild(node)
    void api
      .state()
      .then((s) => setEnabled(s.enabled))
      .catch(() => setEnabled(false))
  }, [node])

  // The page wins over the overlay when both want the deck.
  const place = useCallback(() => {
    const target = owners.current.get("page") ?? owners.current.get("overlay") ?? parking.current
    if (target && node.parentElement !== target) target.appendChild(node)
    if (target && target !== parking.current) {
      setStarted(true)
      setEpoch((e) => e + 1)
    }
  }, [node])

  const adopt = useCallback(
    (owner: string, el: HTMLElement | null) => {
      if (el) owners.current.set(owner, el)
      else owners.current.delete(owner)
      if (owner === "page") setPageMounted(!!el)
      place()
    },
    [place],
  )

  const value = useMemo(
    () => ({ enabled, adopt, overlayOpen, setOverlayOpen }),
    [enabled, adopt, overlayOpen],
  )
  const onEmpty = useCallback(() => setOverlayOpen(false), [])

  return (
    <Ctx.Provider value={value}>
      {children}
      <div ref={parking} className="terminal-parking" aria-hidden="true" />
      {enabled && <TerminalOverlay open={overlayOpen && !pageMounted} />}
      {enabled && started && createPortal(<TerminalDeck epoch={epoch} onEmpty={onEmpty} />, node)}
    </Ctx.Provider>
  )
}

/** A place the deck can be shown in. The element is given to the host while mounted. */
export function TerminalSlot({
  owner,
  className,
}: {
  owner: "page" | "overlay"
  className?: string
}) {
  const { adopt } = useTerminalHost()
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    adopt(owner, ref.current)
    return () => adopt(owner, null)
  }, [adopt, owner])
  return <div ref={ref} className={className} />
}

function TerminalOverlay({ open }: { open: boolean }) {
  const { setOverlayOpen } = useTerminalHost()
  // The slot stays mounted through the closing animation, then gives the deck back.
  const [mounted, setMounted] = useState(open)
  useEffect(() => {
    if (open) {
      setMounted(true)
      return
    }
    const timer = window.setTimeout(() => setMounted(false), 180)
    return () => window.clearTimeout(timer)
  }, [open])
  if (!mounted && !open) return null
  return (
    <div className="terminal-overlay" data-open={open}>
      <button
        type="button"
        className="terminal-overlay-scrim"
        aria-label="Close terminal"
        tabIndex={-1}
        onClick={() => setOverlayOpen(false)}
      />
      <section className="terminal-overlay-panel" role="dialog" aria-label="Terminal">
        <TerminalSlot owner="overlay" className="terminal-slot" />
      </section>
    </div>
  )
}
