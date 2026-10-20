import { useId } from 'react'
import type { ReactNode } from 'react'
import { Button } from '@hollis-labs/design-components'

export interface AccountPreferencesProps {
  /** Host-rendered controlled fields. No schema or manifest protocol is assumed. */
  readonly children: ReactNode
  readonly onSave: () => void
  readonly saving?: boolean
  readonly readOnly?: boolean
  readonly canSave?: boolean
  readonly error?: ReactNode
  readonly notice?: ReactNode
  readonly description?: ReactNode
}

/** Future kit-settings renderers can compose here without changing this surface. */
export function AccountPreferences({ children, onSave, saving = false, readOnly = false, canSave = true, error, notice, description = 'Preferences stored by this app.' }: AccountPreferencesProps) {
  const id = useId()
  return (
    <form aria-labelledby={id} aria-busy={saving} className="flex min-w-0 flex-col gap-4 rounded-panel border border-border bg-bg-elevated p-4" onSubmit={(event) => {
      event.preventDefault()
      if (!saving && !readOnly && canSave) onSave()
    }}>
      <div>
        <h2 id={id} className="text-control font-semibold text-fg">Preferences</h2>
        <div className="text-control text-fg-muted">{description}</div>
      </div>
      <fieldset disabled={saving || readOnly} className="flex min-w-0 flex-col gap-3">{children}</fieldset>
      {error ? <div role="alert" className="text-control text-danger">{error}</div> : null}
      {notice ? <div role="status" className="text-control text-success">{notice}</div> : null}
      {!readOnly ? <Button type="submit" className="self-start" disabled={saving || !canSave}>{saving ? 'Saving preferences…' : 'Save preferences'}</Button> : null}
    </form>
  )
}
