import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import type { ProfileAction, Profile } from '@/lib/api/types'
import { actionsDisabled, CONFIRM } from './profileActionRules'

interface ProfileActionsProps {
  profile: Profile
  /** A job is running, or a request is in flight, for this profile. */
  busy: boolean
  unavailable: boolean
  onAction: (action: ProfileAction) => void
  /** Adds Update runtime, which is on the profile page only. */
  showUpdate?: boolean
}

/** Start, Stop and Restart for one row. Stop and Restart ask for confirmation. */
export function ProfileActions({ profile, busy, unavailable, onAction, showUpdate }: ProfileActionsProps) {
  const { name, status } = profile
  const disabled = actionsDisabled(status, busy || unavailable)

  const confirmed = (action: 'stop' | 'restart' | 'update') => {
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
        destructive={action !== 'update'}
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
      {showUpdate && confirmed('update')}
    </div>
  )
}
