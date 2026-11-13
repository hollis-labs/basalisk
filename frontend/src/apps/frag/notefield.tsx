import { type RefObject, useEffect, useRef, useState } from "react"
import { Markdown } from "../../cite/ui"

const KEY = "basalisk.frag.preview"

/** Whether notes show rendered (⌘⇧P), remembered in this browser. */
export function usePreview(): [boolean, () => void] {
  const [on, setOn] = useState(() => {
    try {
      return window.localStorage.getItem(KEY) === "1"
    } catch {
      return false
    }
  })
  const toggle = () =>
    setOn((v) => {
      try {
        window.localStorage.setItem(KEY, v ? "0" : "1")
      } catch {
        // Not kept.
      }
      return !v
    })
  return [on, toggle]
}

/**
 * A note's text, or its rendering. The rendering is only a view: the text the person typed is
 * what is kept and sent, exactly (markdown or plain text, never the rendered output).
 */
export function NoteField({
  value,
  onChange,
  textRef,
  label,
  placeholder,
  preview,
  className,
  pulse,
}: {
  value: string
  onChange: (v: string) => void
  textRef: RefObject<HTMLTextAreaElement | null>
  label: string
  placeholder: string
  preview: boolean
  className: string
  pulse?: number
}) {
  const viewRef = useRef<HTMLDivElement>(null)
  // The keyboard follows the switch: to the text when writing, to the rendering when previewing.
  // biome-ignore lint/correctness/useExhaustiveDependencies: only when the switch flips
  useEffect(() => {
    if (preview) viewRef.current?.focus()
    else textRef.current?.focus()
  }, [preview])
  return (
    <div className={`frag-field frag-text ${className}`} data-pulse={pulse} data-preview={preview}>
      {preview ? (
        <div
          ref={viewRef}
          className="frag-preview"
          // biome-ignore lint/a11y/noNoninteractiveTabindex: the rendered note keeps the keyboard in the box
          tabIndex={0}
          role="document"
          aria-label={`${label}, rendered`}
        >
          {value.trim() ? (
            <Markdown source={value} />
          ) : (
            <p className="frag-preview-empty">Nothing to preview yet.</p>
          )}
        </div>
      ) : (
        <textarea
          ref={textRef}
          value={value}
          spellCheck
          aria-label={label}
          placeholder={placeholder}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
    </div>
  )
}

/** The footer switch between writing and the rendered view. */
export function PreviewToggle({ on, toggle }: { on: boolean; toggle: () => void }) {
  return (
    <button
      type="button"
      className="frag-preview-toggle"
      data-on={on}
      title="Preview (⌘⇧P)"
      onClick={toggle}
    >
      {on ? "editing ⌘⇧P" : "preview ⌘⇧P"}
    </button>
  )
}
