import { cn } from '@/lib/utils'
import type { ProfileStatus } from '@/lib/api/types'

const DOT: Record<ProfileStatus, string> = {
  running: 'bg-success',
  stopped: 'bg-muted-foreground',
  unknown: 'bg-warning',
}

interface StatusDotProps {
  status: ProfileStatus
  /** Replaces the visible text while a job runs: starting, stopping, restarting. */
  label?: string
  className?: string
}

/** A dot plus its text, so colour is never the only signal. */
export function StatusDot({ status, label, className }: StatusDotProps) {
  return (
    <span className={cn('inline-flex items-center gap-1.5', className)} data-status={status}>
      <span aria-hidden="true" className={cn('size-2 shrink-0 rounded-full', DOT[status])} />
      <span>{label ?? status}</span>
    </span>
  )
}
