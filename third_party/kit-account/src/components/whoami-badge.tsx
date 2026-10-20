import { UserRound } from 'lucide-react'
import { Pill, cn } from '@hollis-labs/design-components'
import type { ReactNode } from 'react'

export type AccountIdentity =
  | { readonly state: 'loading' }
  | { readonly state: 'unknown' }
  | { readonly state: 'error'; readonly message: ReactNode }
  | { readonly state: 'identified'; readonly displayName: string; readonly source?: ReactNode; readonly assurance: 'local' | 'verified' }

export interface WhoamiBadgeProps {
  /** Host asserts identity and assurance; editable profile data must not verify it. */
  readonly identity: AccountIdentity
  readonly className?: string
}

export function WhoamiBadge({ identity, className }: WhoamiBadgeProps) {
  return (
    <div aria-label="Current identity" className={cn('flex min-w-0 items-center gap-2 text-control text-fg', className)}>
      <UserRound className="size-4 shrink-0 text-fg-muted" aria-hidden />
      {identity.state === 'identified' ? (
        <>
          <div className="min-w-0 [overflow-wrap:anywhere]">
            <div>{identity.displayName}</div>
            {identity.source ? <div className="text-caption text-fg-muted">{identity.source}</div> : null}
          </div>
          <Pill tone={identity.assurance === 'verified' ? 'success' : 'neutral'}>{identity.assurance === 'verified' ? 'Verified' : 'Local'}</Pill>
        </>
      ) : identity.state === 'loading' ? <span role="status" className="text-fg-muted">Loading identity…</span>
        : identity.state === 'error' ? <span role="alert" className="text-danger">{identity.message}</span>
          : <span className="text-fg-muted">Identity unavailable</span>}
    </div>
  )
}
