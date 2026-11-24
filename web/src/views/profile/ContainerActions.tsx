import type { ContainerHealth } from '@/lib/containerStatus'
import { RowActions } from '@/views/docker/RowActions'
import type { DockerAction } from '@/views/docker/ActionButton'
import { actionsFor, type ContainerActionName } from './containerActionText'
import type { ContainerActionRunner } from './useContainerActionRunner'

const ACTIONS: Record<ContainerActionName, { label: string }> = {
  start: { label: 'Start' },
  stop: { label: 'Stop' },
  restart: { label: 'Restart' },
}

/**
 * Start, stop and restart for one table row. Renders nothing for a row without a full id (the
 * routes accept only the 64 character id) or a state with no action.
 */
export function ContainerActions({
  name,
  fullId,
  health,
  runner,
  disabledReason,
}: {
  name: string
  fullId: string
  health: ContainerHealth
  runner: ContainerActionRunner
  disabledReason?: string
}) {
  if (fullId === '') return null
  const actions: DockerAction[] = actionsFor(health).map((action) => {
    const { label } = ACTIONS[action]
    const down = action !== 'start'
    return {
      id: action,
      label,
      onSelect: () => runner.run({ id: fullId, name, action }),
      pending: runner.pending && runner.activeId === fullId,
      disabledReason: disabledReason ?? (runner.pending && runner.activeId !== fullId ? 'Another container action is running.' : undefined),
      confirm: {
        title: `${label} ${name}?`,
        description: down
          ? `The container ${name} goes down briefly while it ${action === 'stop' ? 'stops' : 'restarts'}.`
          : `Start the container ${name}.`,
        confirmLabel: label,
        cancelLabel: 'Cancel',
      },
    }
  })
  return <RowActions object={name} actions={actions} />
}
