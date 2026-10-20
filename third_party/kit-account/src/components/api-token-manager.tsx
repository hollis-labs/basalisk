import { useId } from 'react'
import type { ReactNode } from 'react'
import { Button, Checkbox, ConfirmDialog, Input, Pill } from '@hollis-labs/design-components'

/** Display metadata only. Never put the token value/prefix/hash in this model. */
export interface ApiTokenRecord {
  readonly id: string
  readonly name: string
  readonly scopes: readonly string[]
  readonly status: string
  readonly canRevoke: boolean
  readonly detail?: ReactNode
}

export interface ApiTokenDraft {
  readonly name: string
  readonly scopes: readonly string[]
}

export interface ApiTokenScope {
  readonly id: string
  readonly label: string
  readonly description?: ReactNode
}

export interface ApiTokenManagerProps {
  readonly tokens: readonly ApiTokenRecord[]
  readonly scopeOptions: readonly ApiTokenScope[]
  readonly draft: ApiTokenDraft
  readonly onDraftChange: (draft: ApiTokenDraft) => void
  readonly onCreate: (draft: ApiTokenDraft) => void
  readonly canCreate: boolean
  readonly creating?: boolean
  readonly loading?: boolean
  /** Sanitized host message; raw transport errors may contain credentials. */
  readonly error?: ReactNode
  readonly revokeTargetId: string | null
  readonly onRevokeTargetChange: (id: string | null) => void
  readonly onRevoke: (id: string) => void
  readonly revoking?: boolean
  readonly revokeError?: ReactNode
}

export function ApiTokenManager({ tokens, scopeOptions, draft, onDraftChange, onCreate, canCreate, creating = false, loading = false, error, revokeTargetId, onRevokeTargetChange, onRevoke, revoking = false, revokeError }: ApiTokenManagerProps) {
  const id = useId()
  const target = tokens.find((token) => token.id === revokeTargetId && token.status === 'active' && token.canRevoke)
  const blocked = loading || creating || !canCreate
  const validScopes = draft.scopes.length > 0 && draft.scopes.every((scope) => scopeOptions.some((option) => option.id === scope))
  return (
    <section aria-labelledby={`${id}-title`} aria-busy={loading} className="flex min-w-0 flex-col gap-4 rounded-panel border border-border bg-bg-elevated p-4">
      <h2 id={`${id}-title`} className="text-control font-semibold text-fg">API tokens</h2>
      {loading ? <div role="status" className="text-control text-fg-muted">Loading tokens…</div> : null}
      {error ? <div role="alert" className="text-control text-danger">{error}</div> : null}
      <ul className="flex min-w-0 flex-col gap-3">
        {tokens.map((token) => (
          <li key={token.id} className="flex min-w-0 flex-col gap-2 border-b border-divider pb-3 sm:flex-row sm:items-start sm:justify-between">
            <div className="min-w-0 [overflow-wrap:anywhere]">
              <div className="flex flex-wrap items-center gap-2 text-control text-fg">
                <span>{token.name}</span>
                <Pill tone={token.status === 'active' ? 'success' : 'neutral'}>{token.status}</Pill>
              </div>
              <div className="text-caption text-fg-muted">Scopes: {token.scopes.join(', ') || 'None'}</div>
              {token.detail ? <div className="text-caption text-fg-muted">{token.detail}</div> : null}
            </div>
            {token.status === 'active' && token.canRevoke ? <Button type="button" variant="ghost" size="sm" disabled={loading || revoking} className="h-auto max-w-full self-start whitespace-normal text-left [overflow-wrap:anywhere]" onClick={() => onRevokeTargetChange(token.id)}>Revoke {token.name}</Button> : null}
          </li>
        ))}
      </ul>
      {!loading && tokens.length === 0 ? <p className="text-control text-fg-muted">No API tokens.</p> : null}
      {canCreate ? (
        <form aria-label="Create API token" aria-busy={creating} className="flex flex-col gap-3" onSubmit={(event) => {
          event.preventDefault()
          if (!blocked && draft.name.trim() && validScopes) onCreate(draft)
        }}>
          <label htmlFor={`${id}-name`} className="text-label text-fg-secondary">Token name</label>
          <Input id={`${id}-name`} value={draft.name} onChange={(event) => onDraftChange({ ...draft, name: event.target.value })} required disabled={blocked} autoComplete="off" className="text-control md:text-control text-fg" />
          <fieldset disabled={blocked} className="flex flex-col gap-2">
            <legend className="mb-2 text-label text-fg-secondary">Scopes</legend>
            {scopeOptions.map((scope, index) => (
              <div key={scope.id} className="flex items-start gap-2">
                <Checkbox id={`${id}-scope-${index}`} checked={draft.scopes.includes(scope.id)} disabled={blocked} onCheckedChange={(checked) => onDraftChange({ ...draft, scopes: checked ? [...new Set([...draft.scopes, scope.id])] : draft.scopes.filter((value) => value !== scope.id) })} aria-describedby={scope.description ? `${id}-description-${index}` : undefined} />
                <div className="min-w-0">
                  <label htmlFor={`${id}-scope-${index}`} className="text-control text-fg">{scope.label}</label>
                  {scope.description ? <div id={`${id}-description-${index}`} className="text-caption text-fg-muted">{scope.description}</div> : null}
                </div>
              </div>
            ))}
          </fieldset>
          <p className="text-caption text-fg-muted">Choose at least one available scope. The host enforces access permissions.</p>
          {!validScopes && draft.scopes.length > 0 ? <div role="alert" className="flex flex-col items-start gap-2 text-control text-danger">
            <span>Some selected scopes are unavailable. Choose current scopes.</span>
            <Button type="button" variant="ghost" size="sm" disabled={blocked} onClick={() => onDraftChange({ ...draft, scopes: draft.scopes.filter((scope) => scopeOptions.some((option) => option.id === scope)) })}>Clear unavailable scopes</Button>
          </div> : null}
          <Button type="submit" className="self-start" disabled={blocked || !draft.name.trim() || !validScopes}>{creating ? 'Creating token…' : 'Create token'}</Button>
        </form>
      ) : null}
      <ConfirmDialog open={target !== undefined} onOpenChange={(open) => { if (!open && !revoking) onRevokeTargetChange(null) }} title="Revoke API token?"
        description={<span className="block [overflow-wrap:anywhere]"><span className="mb-2 block text-fg-secondary">{target?.name}</span>This token will stop granting access.{revokeError ? <span role="alert" className="mt-2 block text-danger">{revokeError}</span> : null}</span>}
        confirmLabel="Revoke token" busy={loading || revoking} onConfirm={() => { if (target && !loading && !revoking) onRevoke(target.id) }} />
    </section>
  )
}
