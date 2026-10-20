import { useId } from 'react'
import type { ReactNode } from 'react'
import { Button, Pill } from '@hollis-labs/design-components'

/** UI metadata only, never OAuth access/refresh tokens or provider secrets. */
export interface ConnectedAccount {
  readonly id: string
  readonly provider: string
  readonly status: string
  readonly accountLabel?: ReactNode
  readonly detail?: ReactNode
  readonly canConnect: boolean
  readonly canDisconnect: boolean
}

export interface ConnectedAccountsProps {
  readonly accounts: readonly ConnectedAccount[]
  readonly onConnect: (id: string) => void
  /** Intent only. The host owns confirmation and any remote grant revocation. */
  readonly onDisconnect: (id: string) => void
  readonly loading?: boolean
  readonly error?: ReactNode
  readonly disabled?: boolean
}

export function ConnectedAccounts({ accounts, onConnect, onDisconnect, loading = false, error, disabled = false }: ConnectedAccountsProps) {
  const id = useId()
  return (
    <section aria-labelledby={id} aria-busy={loading} className="flex min-w-0 flex-col gap-4 rounded-panel border border-border bg-bg-elevated p-4">
      <h2 id={id} className="text-control font-semibold text-fg">Connected accounts</h2>
      {loading ? <div role="status" className="text-control text-fg-muted">Loading connected accounts…</div> : null}
      {error ? <div role="alert" className="text-control text-danger">{error}</div> : null}
      <ul className="flex min-w-0 flex-col gap-3">
        {accounts.map((account) => {
          const pending = account.status === 'connecting' || account.status === 'disconnecting'
          const canConnect = account.canConnect && (account.status === 'disconnected' || account.status === 'error')
          const canDisconnect = account.canDisconnect && (account.status === 'connected' || account.status === 'error')
          return (
            <li key={account.id} aria-busy={pending} className="flex min-w-0 flex-col gap-2 border-b border-divider pb-3 sm:flex-row sm:items-start sm:justify-between">
              <div className="min-w-0 [overflow-wrap:anywhere]">
                <div className="flex flex-wrap items-center gap-2 text-control text-fg"><span>{account.provider}</span><Pill tone={account.status === 'connected' ? 'success' : account.status === 'error' ? 'danger' : 'neutral'}>{account.status}</Pill></div>
                {account.accountLabel ? <div className="text-control text-fg-secondary">{account.accountLabel}</div> : null}
                {account.detail ? <div className="text-caption text-fg-muted">{account.detail}</div> : null}
                {pending ? <span role="status" className="text-caption text-fg-muted">{account.status === 'connecting' ? 'Connecting…' : 'Disconnecting…'}</span> : null}
              </div>
              <div className="flex min-w-0 flex-wrap gap-2">
                {canConnect ? <Button type="button" size="sm" className="h-auto max-w-full whitespace-normal text-left [overflow-wrap:anywhere]" disabled={disabled || loading} onClick={() => onConnect(account.id)}>Connect {account.provider}</Button> : null}
                {canDisconnect ? <Button type="button" variant="ghost" size="sm" className="h-auto max-w-full whitespace-normal text-left [overflow-wrap:anywhere]" disabled={disabled || loading} onClick={() => onDisconnect(account.id)}>Disconnect {account.provider}</Button> : null}
              </div>
            </li>
          )
        })}
      </ul>
      {!loading && accounts.length === 0 ? <p className="text-control text-fg-muted">No connected-account providers.</p> : null}
    </section>
  )
}
