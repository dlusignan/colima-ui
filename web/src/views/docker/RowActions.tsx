import { ActionButton, type DockerAction } from './ActionButton'

/**
 * The actions of one table row, as a labelled group of buttons (real buttons,
 * so Tab, Enter and Space work with no extra handling). Every accessible name
 * is "<label> <object>", for example "Remove synthetic-data". A disabled
 * action stays focusable and shows its reason as text beside it, not only in a
 * tooltip. Put it in the last cell of the row.
 */
export function RowActions({ object, actions }: { object: string; actions: DockerAction[] }) {
  if (actions.length === 0) return null
  return (
    <div role="group" aria-label={`Actions for ${object}`} className="flex flex-wrap items-start gap-2">
      {actions.map((a) => (
        <ActionButton key={a.id} action={a} accessibleName={`${a.label} ${object}`} size="xs" />
      ))}
    </div>
  )
}
