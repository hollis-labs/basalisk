import { Crosshair, FilePen, MapPin } from "lucide-react"
import {
  type PointerEvent as ReactPointerEvent,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react"
import {
  COLLAB_CHANGED,
  type CollabAnchor,
  type CollabDoc,
  type CollabMark,
  collab,
} from "../../api/collab"
import { files } from "../../api/files"
import { collabOpen } from "../../collab/agent"
import { MarkComposer, MarksPanel } from "../../collab/marks"
import { useDesk } from "../../desktop/context"
import type { WindowProps } from "../../desktop/registry"
import "../../collab/collab.css"

const ICON = { size: 14, strokeWidth: 1.6, "aria-hidden": true } as const

/** Files the Viewer shows. */
export const REVIEWABLE = /\.(png|jpe?g|gif|webp|avif|svg|mp4|webm|mov|m4v|mp3|wav|m4a|pdf|html?)$/i

type Media = "image" | "video" | "audio" | "page" | "other"
function mediaOf(path: string): Media {
  const ext = path.split(".").pop()?.toLowerCase() ?? ""
  if (/^(png|jpe?g|gif|webp|avif|svg)$/.test(ext)) return "image"
  if (/^(mp4|webm|mov|m4v)$/.test(ext)) return "video"
  if (/^(mp3|wav|m4a)$/.test(ext)) return "audio"
  if (/^(pdf|html?)$/.test(ext)) return "page"
  return "other"
}

interface FileRef {
  root: string
  path: string
}
const refOf = (f: FileRef) => `file:${f.root}:${f.path}`
function fileOf(ref?: string): FileRef | null {
  const m = ref?.match(/^file:([^:]+):(.+)$/)
  return m ? { root: m[1], path: m[2] } : null
}
const clock = (t: number) => `${Math.floor(t / 60)}:${String(Math.floor(t % 60)).padStart(2, "0")}`

/**
 * The Viewer window: the artifact in the middle, its marks on the right. Drag a box on a
 * picture (or click for a pin) and say what about it. A video or a page is marked the same
 * way after "Mark", which lays a sheet over it so the drag is not taken by its controls.
 */
export function ViewerWindow({ winId, args }: WindowProps) {
  const desk = useDesk()
  const argFile = args.file as FileRef | undefined
  const [docs, setDocs] = useState<CollabDoc[]>([])
  const [name, setName] = useState(typeof args.name === "string" ? args.name : "")
  const [doc, setDoc] = useState<CollabDoc | null>(null)
  const [file, setFile] = useState<FileRef | null>(argFile ?? null)
  const [marks, setMarks] = useState<CollabMark[]>([])
  const [picked, setPicked] = useState<CollabAnchor | null>(null)
  const [drag, setDrag] = useState<{ x0: number; y0: number; x1: number; y1: number } | null>(null)
  const [marking, setMarking] = useState(false)
  const [active, setActive] = useState("")
  const [now, setNow] = useState(0)
  const [error, setError] = useState("")
  const stage = useRef<HTMLDivElement>(null)
  const player = useRef<HTMLVideoElement & HTMLAudioElement>(null)
  const nameRef = useRef(name)
  nameRef.current = name

  const fail = useCallback(
    (e: unknown) => setError(e instanceof Error ? e.message : "That did not work."),
    [],
  )
  const loadList = useCallback(() => collab.list("artifact").then(setDocs).catch(fail), [fail])

  const open = useCallback(
    async (which: string) => {
      try {
        const r = await collab.get(which)
        if (nameRef.current !== which) return
        setDoc(r.doc)
        setMarks(r.marks)
        setFile(fileOf(r.doc.ref))
        setError("")
      } catch (e) {
        fail(e)
      }
    },
    [fail],
  )

  // Opened on a file: find the artifact that already points at it, if there is one.
  // biome-ignore lint/correctness/useExhaustiveDependencies: once, for the file the window was opened with
  useEffect(() => {
    void (async () => {
      const list = await collab.list("artifact").catch(() => [] as CollabDoc[])
      setDocs(list)
      if (!argFile || name) return
      const base = argFile.path.split("/").pop() ?? ""
      for (const d of list.filter((x) => x.title === base)) {
        const r = await collab.get(d.name).catch(() => null)
        if (r && r.doc.ref === refOf(argFile)) {
          setName(d.name)
          return
        }
      }
    })()
  }, [])
  useEffect(() => {
    if (!name) return
    setPicked(null)
    void open(name)
  }, [name, open])
  useEffect(() => {
    const onChange = (e: Event) => {
      void loadList()
      if ((e as CustomEvent<string>).detail === nameRef.current && nameRef.current)
        void open(nameRef.current)
    }
    window.addEventListener(COLLAB_CHANGED, onChange)
    return () => window.removeEventListener(COLLAB_CHANGED, onChange)
  }, [open, loadList])

  const title = doc?.title ?? file?.path.split("/").pop() ?? "Viewer"
  useEffect(() => {
    if (doc)
      collabOpen.set(winId, {
        name: doc.name,
        rev: doc.rev,
        openMarks: marks.filter((m) => m.state === "open").length,
      })
    desk.setTitle(winId, title)
    return () => {
      collabOpen.delete(winId)
    }
  }, [doc, marks, winId, desk, title])

  const media: Media = file ? mediaOf(file.path) : "other"
  const src = file ? files.contentURL(file.root, file.path) : ""
  const timed = media === "video" || media === "audio"
  // A picture is always ready to be marked; a video or a page only after "Mark".
  const sheet = media === "image" || marking

  const frac = (e: ReactPointerEvent) => {
    const box = stage.current?.getBoundingClientRect()
    if (!box) return { x: 0, y: 0 }
    return {
      x: Math.min(1, Math.max(0, (e.clientX - box.left) / box.width)),
      y: Math.min(1, Math.max(0, (e.clientY - box.top) / box.height)),
    }
  }
  const down = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return
    e.currentTarget.setPointerCapture(e.pointerId)
    const p = frac(e)
    setDrag({ x0: p.x, y0: p.y, x1: p.x, y1: p.y })
  }
  const move = (e: ReactPointerEvent) => {
    if (!drag) return
    const p = frac(e)
    setDrag({ ...drag, x1: p.x, y1: p.y })
  }
  const up = () => {
    if (!drag) return
    const r3 = (n: number) => Number(n.toFixed(4))
    const x = Math.min(drag.x0, drag.x1)
    const y = Math.min(drag.y0, drag.y1)
    const w = Math.abs(drag.x1 - drag.x0)
    const h = Math.abs(drag.y1 - drag.y0)
    const pin = w < 0.01 && h < 0.01
    setPicked({
      x: r3(pin ? drag.x0 : x),
      y: r3(pin ? drag.y0 : y),
      w: pin ? 0 : r3(w),
      h: pin ? 0 : r3(h),
      ...(timed ? { time: Number((player.current?.currentTime ?? 0).toFixed(1)) } : {}),
    })
    setDrag(null)
  }

  const addMark = async (m: { kind: CollabMark["kind"]; text: string }) => {
    if (!file || !picked) return
    try {
      // The artifact is made when it is first marked.
      let d = doc
      if (!d) {
        d = await collab.create({ kind: "artifact", title, ref: refOf(file), body: "" })
        setDoc(d)
        nameRef.current = d.name
        setName(d.name)
        void loadList()
      }
      setMarks(
        await collab.mark(d.name, "add", {
          rev: d.rev,
          kind: m.kind,
          text: m.text,
          anchor: picked,
        }),
      )
      setPicked(null)
      setMarking(false)
    } catch (e) {
      fail(e)
    }
  }
  const setState = (m: CollabMark, state: CollabMark["state"]) =>
    doc && void collab.mark(doc.name, "update", { id: m.id, state }).then(setMarks).catch(fail)
  const remove = (m: CollabMark) =>
    doc && void collab.mark(doc.name, "remove", { id: m.id }).then(setMarks).catch(fail)
  const goTo = (m: CollabMark) => {
    setActive(m.id)
    if (m.anchor.time !== undefined && player.current) {
      player.current.currentTime = m.anchor.time
      void player.current.pause()
    }
  }
  const asDraft = async () => {
    if (!file) return
    try {
      const text = await (await fetch(src, { credentials: "same-origin" })).text()
      const d = await collab.create({
        kind: "draft",
        title,
        lang: "text",
        body: text.slice(0, 2_000_000),
      })
      desk.open("drafts", { args: { name: d.name }, key: d.name, title: d.title })
    } catch (e) {
      fail(e)
    }
  }

  // The boxes to draw: open marks with a place; on a video, those near the current moment.
  const boxes = useMemo(
    () =>
      marks
        .filter((m) => m.state === "open" && m.anchor.x !== undefined)
        .map((m, i) => ({ m, n: i + 1 }))
        .filter(
          ({ m }) => !timed || m.anchor.time === undefined || Math.abs(m.anchor.time - now) < 2,
        ),
    [marks, timed, now],
  )
  const what = picked
    ? `${picked.w ? "a region" : "a point"}${picked.time !== undefined ? ` at ${clock(picked.time)}` : ""}`
    : ""

  return (
    <div className="collab">
      <nav className="collab-list">
        <header>
          <strong>Under review</strong>
        </header>
        {docs.map((d) => (
          <button
            key={d.name}
            type="button"
            data-on={d.name === name}
            onClick={() => setName(d.name)}
          >
            <span>{d.title || d.name}</span>
          </button>
        ))}
        {!docs.length && (
          <p className="collab-none">
            Nothing marked up yet. Open a picture or a video from Files, or use “Review: mark up the
            file this window shows” on a PDF or an HTML mock.
          </p>
        )}
      </nav>
      {file ? (
        <section className="collab-main">
          <header className="collab-bar">
            <span className="collab-title">{title}</span>
            <span className="collab-rev" title={refOf(file)}>
              {file.root}:{file.path}
            </span>
            {media !== "image" && media !== "other" && media !== "audio" && (
              <button
                type="button"
                className="viewer-tool"
                data-on={marking}
                title="Lay a sheet over it and drag a box (or click a point)"
                onClick={() => {
                  player.current?.pause()
                  setMarking((v) => !v)
                }}
              >
                <Crosshair {...ICON} /> {marking ? "Marking…" : "Mark"}
              </button>
            )}
            {timed && (
              <button
                type="button"
                className="viewer-tool"
                title="Mark this moment"
                onClick={() => {
                  player.current?.pause()
                  setPicked({ time: Number((player.current?.currentTime ?? 0).toFixed(1)) })
                }}
              >
                <MapPin {...ICON} /> Mark {clock(now)}
              </button>
            )}
            <button
              type="button"
              className="viewer-tool"
              title="Mark the whole thing"
              onClick={() => setPicked({})}
            >
              Note
            </button>
          </header>
          {error && (
            <p className="collab-status" data-tone="warn" role="alert">
              {error}
            </p>
          )}
          <div className="viewer-room">
            {media === "other" ? (
              <div className="viewer-other">
                <p>This kind of file is not shown here.</p>
                <button type="button" className="collab-primary" onClick={() => void asDraft()}>
                  <FilePen {...ICON} /> Open its text as a draft
                </button>
              </div>
            ) : (
              <div className="viewer-stage" data-media={media} ref={stage}>
                {media === "image" && <img src={src} alt={title} draggable={false} />}
                {media === "video" && (
                  // biome-ignore lint/a11y/useMediaCaption: a file under review, as it is
                  <video
                    ref={player}
                    src={src}
                    controls
                    onTimeUpdate={(e) => setNow(e.currentTarget.currentTime)}
                  />
                )}
                {media === "audio" && (
                  // biome-ignore lint/a11y/useMediaCaption: a file under review, as it is
                  <audio
                    ref={player}
                    src={src}
                    controls
                    onTimeUpdate={(e) => setNow(e.currentTarget.currentTime)}
                  />
                )}
                {media === "page" && <iframe src={src} title={title} sandbox="" />}
                {media !== "audio" && (
                  <div
                    className="viewer-sheet"
                    data-on={sheet}
                    onPointerDown={sheet ? down : undefined}
                    onPointerMove={move}
                    onPointerUp={up}
                  >
                    {boxes.map(({ m, n }) => (
                      <button
                        key={m.id}
                        type="button"
                        className="viewer-box"
                        data-pin={!m.anchor.w}
                        data-active={m.id === active}
                        data-by={m.by}
                        title={m.text}
                        style={{
                          left: `${(m.anchor.x ?? 0) * 100}%`,
                          top: `${(m.anchor.y ?? 0) * 100}%`,
                          width: `${(m.anchor.w ?? 0) * 100}%`,
                          height: `${(m.anchor.h ?? 0) * 100}%`,
                        }}
                        onPointerDown={(e) => e.stopPropagation()}
                        onClick={() => setActive(m.id)}
                      >
                        <i>{n}</i>
                      </button>
                    ))}
                    {(drag || picked?.x !== undefined) && (
                      <span
                        className="viewer-box viewer-draft"
                        data-pin={drag ? false : !picked?.w}
                        style={
                          drag
                            ? {
                                left: `${Math.min(drag.x0, drag.x1) * 100}%`,
                                top: `${Math.min(drag.y0, drag.y1) * 100}%`,
                                width: `${Math.abs(drag.x1 - drag.x0) * 100}%`,
                                height: `${Math.abs(drag.y1 - drag.y0) * 100}%`,
                              }
                            : {
                                left: `${(picked?.x ?? 0) * 100}%`,
                                top: `${(picked?.y ?? 0) * 100}%`,
                                width: `${(picked?.w ?? 0) * 100}%`,
                                height: `${(picked?.h ?? 0) * 100}%`,
                              }
                        }
                      />
                    )}
                  </div>
                )}
              </div>
            )}
          </div>
        </section>
      ) : (
        <section className="collab-main collab-empty">
          <p>{name ? "Opening…" : "Pick something under review, or open a file here."}</p>
        </section>
      )}
      {file && (
        <div className="collab-side">
          {picked && (
            <MarkComposer
              what={what || "the whole thing"}
              canSuggest={false}
              onAdd={(m) => void addMark(m)}
              onCancel={() => setPicked(null)}
            />
          )}
          <MarksPanel
            marks={marks}
            active={active}
            onPick={goTo}
            onState={setState}
            onRemove={remove}
          />
        </div>
      )}
    </div>
  )
}
