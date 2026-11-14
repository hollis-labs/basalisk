import {
  Bot,
  Brain,
  CircleDot,
  Copy,
  ExternalLink,
  FileText,
  Layers,
  Link2,
  ListChecks,
  type LucideIcon,
  Maximize2,
  Zap,
} from "lucide-react"
import {
  Children,
  cloneElement,
  type ElementType,
  isValidElement,
  type ReactNode,
  useLayoutEffect,
  useRef,
  useState,
} from "react"
import ReactMarkdown, { type Components } from "react-markdown"
import remarkGfm from "remark-gfm"
import { openLink } from "../api/browser"
import { composeMessage, URN } from "../desktop/message"
import { parseTachHref } from "./core/tach"
import { CodeRef, FileLink, placeOf, splitFiles, usePlaces } from "./files"
import { parseFragmentHref } from "./fragment"
import {
  canResolve,
  hideCard,
  holdCard,
  kindLabel,
  openCite,
  type RefIdentity,
  type RefState,
  segments,
  showCard,
  useCard,
  useRefState,
} from "./index"

const ICONS: Record<string, LucideIcon> = {
  "torque.task": ListChecks,
  "torque.epic": Layers,
  "torque.project": Layers,
  "torque.sprint": Layers,
  "torque.plan": Layers,
  "tesseract.item": Brain,
  "tesseract.key": Brain,
  "tether.agent": Bot,
  "tether.plan": CircleDot,
  path: FileText,
  url: Link2,
  "fragments.fragment": Zap,
}
const iconFor = (kind: string): LucideIcon => ICONS[kind] ?? Link2

const clip = (s: string, n: number) => (s.length > n ? `${s.slice(0, n - 1)}…` : s)

function ago(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime()
  if (Number.isNaN(ms)) return ""
  const m = Math.round(ms / 60000)
  if (m < 1) return "just now"
  if (m < 60) return `${m}m ago`
  const h = Math.round(m / 60)
  if (h < 48) return `${h}h ago`
  return `${Math.round(h / 24)}d ago`
}

/**
 * A reference, inline: an icon and a label. Once resolved the label is the record's own
 * ("Task: Fix the thing"); until then it is what was written. Hovering shows the card;
 * activating opens the record.
 */
export function CiteChip({
  refId,
  raw,
  label,
}: {
  refId: RefIdentity
  raw?: string
  label?: string
}) {
  const resolvable = canResolve(refId.kind)
  const state = useRefState(refId.kind, refId.canonical, resolvable)
  const Icon = iconFor(refId.kind)
  const text =
    state.status === "found" && state.entity.chipLabel
      ? clip(state.entity.chipLabel, 56)
      : (label ?? raw ?? refId.canonical)
  const anchorOf = (el: HTMLElement) => {
    const r = el.getBoundingClientRect()
    return { left: r.left, top: r.top, right: r.right, bottom: r.bottom }
  }
  return (
    <button
      type="button"
      className="cite-chip"
      data-kind={refId.kind}
      data-state={state.status}
      title={resolvable ? undefined : refId.canonical}
      onClick={(e) => {
        e.preventDefault()
        e.stopPropagation()
        openCite(refId)
      }}
      onPointerEnter={(e) => refId.kind !== "url" && showCard(refId, anchorOf(e.currentTarget))}
      onPointerLeave={() => hideCard()}
      onFocus={(e) => refId.kind !== "url" && showCard(refId, anchorOf(e.currentTarget))}
      onBlur={() => hideCard()}
    >
      <Icon aria-hidden="true" size={12} strokeWidth={1.7} />
      <span>{text}</span>
    </button>
  )
}

/** Plain text with its references turned into chips (file references into links: files.tsx). */
export function CiteText({ text }: { text: string }) {
  const places = usePlaces()
  return (
    <>
      {splitFiles(text, places).map((part, i) =>
        typeof part === "string" ? (
          // biome-ignore lint/suspicious/noArrayIndexKey: the runs of one text, in order
          <CiteSegments key={`t:${i}`} text={part} places={places} />
        ) : (
          <FileLink key={`f:${part.start}`} fileRef={part.ref}>
            {part.raw}
          </FileLink>
        ),
      )}
    </>
  )
}

function CiteSegments({ text, places }: { text: string; places: ReturnType<typeof usePlaces> }) {
  return (
    <>
      {segments(text).map((s) => {
        const file = s.type !== "text" && s.kind === "path" ? placeOf(s.canonical, places) : null
        return s.type === "text" ? (
          s.text
        ) : file ? (
          <FileLink key={`${s.start}:${s.canonical}`} fileRef={file}>
            {s.raw}
          </FileLink>
        ) : (
          <CiteChip
            key={`${s.start}:${s.canonical}`}
            refId={{ kind: s.kind, canonical: s.canonical, meta: s.meta }}
            raw={s.raw}
          />
        )
      })}
    </>
  )
}

/** A Tether address (msg://…): click to write to it. */
export function UrnLink({ urn }: { urn: string }) {
  return (
    <button
      type="button"
      className="cite-urn"
      title="Write a message to this address"
      onClick={() => composeMessage({ to: urn })}
    >
      {urn}
    </button>
  )
}

/** Prose with its references linkified and its Tether addresses made clickable. */
function UrnText({ text }: { text: string }) {
  if (!text.includes("msg://")) return <CiteText text={text} />
  const out: ReactNode[] = []
  let at = 0
  for (const m of text.matchAll(URN)) {
    const i = m.index ?? 0
    if (i > at) out.push(<CiteText key={`t${at}`} text={text.slice(at, i)} />)
    out.push(<UrnLink key={`u${i}`} urn={m[0]} />)
    at = i + m[0].length
  }
  if (at < text.length) out.push(<CiteText key={`t${at}`} text={text.slice(at)} />)
  return <>{out}</>
}

/** Linkifies the string children of a markdown element, leaving nested elements alone. */
function linkified(children: ReactNode): ReactNode {
  return Children.map(children, (child) => {
    if (typeof child === "string") return <UrnText text={child} />
    if (
      isValidElement<{ children?: ReactNode }>(child) &&
      typeof child.type === "string" &&
      child.type !== "code" &&
      child.type !== "a"
    ) {
      return cloneElement(child, undefined, linkified(child.props.children))
    }
    return child
  })
}

const wrap =
  (Tag: ElementType) =>
  ({ children, node: _node, ...rest }: { children?: ReactNode; node?: unknown }) => (
    <Tag {...rest}>{linkified(children)}</Tag>
  )

const MARKDOWN: Components = {
  p: wrap("p"),
  li: wrap("li"),
  td: wrap("td"),
  th: wrap("th"),
  h1: wrap("h1"),
  h2: wrap("h2"),
  h3: wrap("h3"),
  h4: wrap("h4"),
  h5: wrap("h5"),
  h6: wrap("h6"),
  strong: wrap("strong"),
  em: wrap("em"),
  del: wrap("del"),
  blockquote: wrap("blockquote"),
  // Inline code that is a file reference opens it (files.tsx); fenced code is left alone.
  // An address in backticks is still an address.
  code: ({ node: _node, ...props }) => {
    const text = typeof props.children === "string" ? props.children : ""
    return /^msg:\/\/[A-Za-z0-9][A-Za-z0-9._~/-]*$/.test(text) ? (
      <UrnLink urn={text} />
    ) : (
      <CodeRef {...props} />
    )
  },
  // [label](tach:service/id) is a reference with the author's label; other links open outside.
  a({ href, children }) {
    const ref = parseTachHref(href) ?? parseFragmentHref(href)
    if (ref)
      return <CiteChip refId={ref} label={typeof children === "string" ? children : undefined} />
    return (
      <a
        href={href}
        onClick={(e) => {
          e.preventDefault()
          if (href && /^https?:/i.test(href)) openLink(href)
        }}
      >
        {children}
      </a>
    )
  },
}

// react-markdown drops unknown URL schemes; keep tach: so the link above can read it.
const keepTach = (url: string) => (/^(https?:|mailto:|tach:|#|\/)/i.test(url) ? url : "")

/** Markdown, with references linkified in prose and left alone in code. */
export function Markdown({ source }: { source: string }) {
  return (
    <div className="cite-markdown">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={MARKDOWN} urlTransform={keepTach}>
        {source}
      </ReactMarkdown>
    </div>
  )
}

function StatusPill({
  status,
}: {
  status: NonNullable<Extract<RefState, { status: "found" }>["entity"]["status"]>
}) {
  return (
    <span className="cite-status" data-tone={status.tone}>
      {status.label}
    </span>
  )
}

/** The preview card. One instance, rendered by the desktop, placed beside what is hovered. */
export function CiteCardLayer() {
  const card = useCard()
  const el = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null)
  const [copied, setCopied] = useState(false)

  // biome-ignore lint/correctness/useExhaustiveDependencies: re-place whenever a different reference is shown
  useLayoutEffect(() => {
    setCopied(false)
    if (!card || !el.current) {
      setPos(null)
      return
    }
    const box = el.current.getBoundingClientRect()
    const a = card.anchor
    const below = a.bottom + 8 + box.height <= window.innerHeight
    setPos({
      left: Math.max(8, Math.min(a.left, window.innerWidth - box.width - 8)),
      top: below ? a.bottom + 8 : Math.max(8, a.top - box.height - 8),
    })
  }, [card?.ref.kind, card?.ref.canonical])

  if (!card) return null
  return (
    <CardBody
      innerRef={el}
      refId={card.ref}
      style={pos ? { left: pos.left, top: pos.top } : { left: -9999, top: -9999 }}
      copied={copied}
      onCopied={() => setCopied(true)}
    />
  )
}

function CardBody({
  refId,
  innerRef,
  style,
  copied,
  onCopied,
}: {
  refId: RefIdentity
  innerRef: React.RefObject<HTMLDivElement | null>
  style: React.CSSProperties
  copied: boolean
  onCopied: () => void
}) {
  const resolvable = canResolve(refId.kind)
  const state = useRefState(refId.kind, refId.canonical, resolvable)
  const Icon = iconFor(refId.kind)
  const entity =
    state.status === "found"
      ? state.entity
      : state.status === "loading" || state.status === "error"
        ? state.stale
        : undefined
  return (
    <div
      ref={innerRef}
      className="cite-card"
      role="tooltip"
      style={style}
      onPointerEnter={holdCard}
      onPointerLeave={() => hideCard()}
    >
      <header>
        <Icon aria-hidden="true" size={13} strokeWidth={1.7} />
        <span className="cite-card-kind">{kindLabel(refId.kind)}</span>
        {entity?.status && <StatusPill status={entity.status} />}
      </header>
      <h4>
        {entity?.title ??
          (state.status === "loading" || state.status === "idle"
            ? "Looking it up…"
            : state.status === "not-found"
              ? "Not found"
              : state.status === "error"
                ? "Could not be looked up"
                : refId.canonical)}
      </h4>
      <code className="cite-card-id">{refId.canonical}</code>
      {state.status === "not-found" && (
        <p className="cite-card-note">It may have been deleted, or it may not be readable.</p>
      )}
      {state.status === "error" && <p className="cite-card-note">{state.message}</p>}
      {state.status === "unsupported" && (
        <p className="cite-card-note">No preview for this kind yet.</p>
      )}
      {entity?.description && <p className="cite-card-desc">{entity.description}</p>}
      {!!entity?.fields?.length && (
        <dl>
          {entity.fields.map((f) => (
            <div key={f.label}>
              <dt>{f.label}</dt>
              <dd data-mono={!!f.mono}>{f.value}</dd>
            </div>
          ))}
        </dl>
      )}
      <footer>
        {entity?.updatedAt && (
          <span className="cite-card-when">updated {ago(entity.updatedAt)}</span>
        )}
        <button
          type="button"
          onClick={() => {
            void navigator.clipboard?.writeText(refId.canonical).then(onCopied)
          }}
        >
          <Copy aria-hidden="true" size={12} strokeWidth={1.7} />
          {copied ? "Copied" : "Copy id"}
        </button>
        {resolvable && state.status !== "not-found" && (
          <button type="button" data-primary="true" onClick={() => openCite(refId)}>
            <Maximize2 aria-hidden="true" size={12} strokeWidth={1.7} />
            Open
          </button>
        )}
        {refId.kind === "url" && (
          <button type="button" data-primary="true" onClick={() => openCite(refId)}>
            <ExternalLink aria-hidden="true" size={12} strokeWidth={1.7} />
            Open
          </button>
        )}
      </footer>
    </div>
  )
}
