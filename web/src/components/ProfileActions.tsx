import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import type { JobAction, Profile } from '@/lib/api/types'

interface ProfileActionsProps {
  profile: Profile
  /** A job is running, or a request is in flight, for this profile. */
  busy: boolean
  unavailable: boolean
  onAction: (action: JobAction) => void
}

const CONFIRM: Record<'stop' | 'restart', { description: (n: string) => string; label: string }> = {
  stop: {
    label: 'Stop',
    description: (n) => `Stop ${n}? Its containers stop until you start it again.`,
  },
  restart: {
    label: 'Restart',
    description: (n) =>
      `Restart ${n}? Its containers stop and start again, so anything running in them is interrupted.`,
  },
}

/** Start, Stop and Restart for one row. Stop and Restart ask for confirmation. */
export function ProfileActions({ profile, busy, unavailable, onAction }: ProfileActionsProps) {
  const { name, status } = profile
  const locked = busy || unavailable
  const disabled: Record<JobAction, boolean> = {
    start: locked || status === 'running',
    stop: locked || status === 'stopped',
    restart: locked,
  }

  const confirmed = (action: 'stop' | 'restart') => {
    const button = (
      <Button
        size="xs"
        variant={action === 'stop' ? 'destructive' : 'outline'}
        focusableWhenDisabled
        disabled={disabled[action]}
        aria-label={`${CONFIRM[action].label} ${name}`}
      >
        {CONFIRM[action].label}
      </Button>
    )
    // A disabled trigger must not open the dialog, so it renders on its own.
    if (disabled[action]) return button
    return (
      <ConfirmDialog
        trigger={button}
        title={`${CONFIRM[action].label} ${name}?`}
        description={CONFIRM[action].description(name)}
        confirmLabel={CONFIRM[action].label}
        destructive
        onConfirm={() => onAction(action)}
      />
    )
  }

  return (
    <div className="flex items-center gap-1">
      <Button
        size="xs"
        variant="outline"
        focusableWhenDisabled
        disabled={disabled.start}
        aria-label={`Start ${name}`}
        onClick={() => onAction('start')}
      >
        Start
      </Button>
      {confirmed('stop')}
      {confirmed('restart')}
    </div>
  )
}
