import { useId } from 'react'
import { Button } from '@hollis-labs/design-components'

export interface NewTokenDisclosureProps {
  /** Ephemeral create response. Host must clear it on dismiss/session change/unmount. */
  readonly value: string | null
  readonly onDismiss: () => void
  /** Explicit user intent only. The host chooses clipboard handling, including errors. */
  readonly onCopy?: () => void
  readonly copied?: boolean
}

/** No state, browser storage, URL, logging or secret-bearing callback payloads. */
export function NewTokenDisclosure({ value, onDismiss, onCopy, copied = false }: NewTokenDisclosureProps) {
  const id = useId()
  if (value === null) return null
  return (
    <section aria-labelledby={id} className="flex min-w-0 flex-col gap-3 rounded-panel border border-warning bg-warning-muted p-4">
      <h2 id={id} className="text-control font-semibold text-fg">Save your new token</h2>
      <p className="text-control text-fg-secondary">Copy this token now. After you close this view, it will not be shown again.</p>
      {/* A text node avoids password-manager/form draft recovery and value attributes. */}
      <pre className="whitespace-pre-wrap rounded-control bg-bg-elevated p-3 font-mono text-control text-fg [overflow-wrap:anywhere]"><code>{value}</code></pre>
      <div className="flex flex-wrap gap-2">
        {onCopy ? <Button type="button" variant="outline" onClick={() => onCopy()}>{copied ? 'Copied' : 'Copy token'}</Button> : null}
        <Button type="button" onClick={() => onDismiss()}>I have saved this token</Button>
      </div>
    </section>
  )
}
