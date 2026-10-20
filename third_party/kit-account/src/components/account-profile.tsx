import { useId } from 'react'
import type { ReactNode } from 'react'
import { Button, Input } from '@hollis-labs/design-components'

export interface AccountProfileValue {
  readonly displayName: string
  readonly email: string
}

export interface AccountProfileProps {
  readonly value: AccountProfileValue
  readonly onValueChange: (value: AccountProfileValue) => void
  /** Host handles validation, transport, persistence and success/error state. */
  readonly onSave: (value: AccountProfileValue) => void
  readonly saving?: boolean
  readonly readOnly?: boolean
  readonly canSave?: boolean
  readonly error?: ReactNode
  readonly notice?: ReactNode
}

/** Per-app editable details. A profile name does not assert authenticated identity. */
export function AccountProfile({ value, onValueChange, onSave, saving = false, readOnly = false, canSave = true, error, notice }: AccountProfileProps) {
  const id = useId()
  return (
    <form aria-labelledby={`${id}-title`} aria-busy={saving} className="flex min-w-0 flex-col gap-4 rounded-panel border border-border bg-bg-elevated p-4" onSubmit={(event) => {
      event.preventDefault()
      if (!saving && !readOnly && canSave && value.displayName.trim()) onSave(value)
    }}>
      <div>
        <h2 id={`${id}-title`} className="text-control font-semibold text-fg">Profile</h2>
        <p className="text-control text-fg-muted">Details stored by this app.</p>
      </div>
      <div className="flex flex-col gap-1">
        <label htmlFor={`${id}-name`} className="text-label text-fg-secondary">Display name</label>
        <Input id={`${id}-name`} value={value.displayName} onChange={(event) => onValueChange({ ...value, displayName: event.target.value })} required disabled={saving} readOnly={readOnly} autoComplete="name" className="text-control md:text-control text-fg" />
      </div>
      <div className="flex flex-col gap-1">
        <label htmlFor={`${id}-email`} className="text-label text-fg-secondary">Email (optional)</label>
        <Input id={`${id}-email`} type="email" value={value.email} onChange={(event) => onValueChange({ ...value, email: event.target.value })} disabled={saving} readOnly={readOnly} autoComplete="email" className="text-control md:text-control text-fg" />
      </div>
      {error ? <div role="alert" className="text-control text-danger">{error}</div> : null}
      {notice ? <div role="status" className="text-control text-success">{notice}</div> : null}
      {!readOnly ? <Button type="submit" className="self-start" disabled={saving || !canSave || !value.displayName.trim()}>{saving ? 'Saving profile…' : 'Save profile'}</Button> : null}
    </form>
  )
}
