import { useId, type ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { ConfirmDialog, type ConfirmDialogCopy } from '@/components/ConfirmDialog'

/** One action of a toolbar or a table row. */
export interface DockerAction {
  /** Stable key. */
  id: string
  /** Visible label ("Remove"). The row adds the object to the accessible name. */
  label: string
  onSelect: () => void
  /** Disables the action and shows this text next to it. Never leave a disabled action unexplained. */
  disabledReason?: string
  /** True while the action runs: disables it and keeps it visible. */
  pending?: boolean
  /** Opens a confirm dialog first; `onSelect` runs only after confirming. */
  confirm?: ConfirmDialogCopy
}

/**
 * A button for a DockerAction. A disabled action stays focusable so keyboard
 * and screen reader users reach it, is described by its visible reason, and
 * does nothing when activated.
 */
export function ActionButton({
  action,
  accessibleName,
  size = 'sm',
  sharedReasonId,
}: {
  action: DockerAction
  /** Full accessible name; defaults to the label. */
  accessibleName?: string
  size?: 'xs' | 'sm' | 'default'
  /** Id of a reason shown elsewhere (the toolbar's); disables the button and describes it, adds no text. */
  sharedReasonId?: string
}): ReactNode {
  const reasonId = useId()
  const blocked = Boolean(action.disabledReason) || Boolean(sharedReasonId) || Boolean(action.pending)
  const name = accessibleName ?? action.label
  const button = (
    <Button
      type="button"
      variant={action.confirm?.destructive ? 'destructive' : 'outline'}
      size={size}
      aria-label={name}
      aria-describedby={action.disabledReason ? reasonId : sharedReasonId}
      disabled={blocked}
      focusableWhenDisabled
      onClick={action.confirm ? undefined : action.onSelect}
    >
      {action.pending ? `${action.label}…` : action.label}
    </Button>
  )
  return (
    <span className="inline-flex flex-col items-start gap-0.5">
      {action.confirm ? (
        <ConfirmDialog {...action.confirm} trigger={button} onConfirm={action.onSelect} />
      ) : (
        button
      )}
      {action.disabledReason && (
        <span id={reasonId} className="text-xs text-muted-foreground">
          {action.disabledReason}
        </span>
      )}
    </span>
  )
}
