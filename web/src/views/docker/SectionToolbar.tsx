import { useId } from 'react'
import { ActionButton, type DockerAction } from './ActionButton'

/**
 * The strip above a section's table for section-level buttons (Create, Pull,
 * Prune). Renders nothing when there are no actions, so a section can mount it
 * before it has any. Pass `disabledReason` (for example "Start the profile to
 * use these actions.") to disable every action at once with that text shown
 * once beside them; an action's own `disabledReason` shows next to that button.
 */
export function SectionToolbar({
  label,
  actions,
  disabledReason,
}: {
  /** Accessible name of the toolbar, for example "Volume actions". */
  label: string
  actions: DockerAction[]
  disabledReason?: string
}) {
  const reasonId = useId()
  if (actions.length === 0) return null
  return (
    <div role="toolbar" aria-label={label} className="flex flex-wrap items-center gap-2">
      {actions.map((a) => (
        <ActionButton key={a.id} action={a} size="sm" sharedReasonId={disabledReason ? reasonId : undefined} />
      ))}
      {disabledReason && (
        <span id={reasonId} className="text-xs text-muted-foreground">
          {disabledReason}
        </span>
      )}
    </div>
  )
}
