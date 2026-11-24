import { ApiError } from '@/lib/api/client'
import type { ContainerHealth } from '@/lib/containerStatus'
import { describeDockerError } from '@/views/docker/describeDockerError'

export type ContainerActionName = 'start' | 'stop' | 'restart'

export const PAST: Record<ContainerActionName, string> = { start: 'Started', stop: 'Stopped', restart: 'Restarted' }

/** Fixed error texts for the container action routes; server text is never shown. */
export function describeContainerActionError(error: unknown, fallback: string): string {
  if (error instanceof ApiError) {
    if (error.status === 404) return 'That container no longer exists. The list was refreshed.'
    if (error.code === 'profile_busy') return 'A job is running on this profile. Try again when it finishes.'
    if (error.code === 'invalid_state') return 'The VM is not running.'
    if (error.status === 409) return 'Docker refused to change this container in its current state.'
    if (error.status === 422) return 'That is not a valid container id. Refresh the list and try again.'
    if (error.status !== 0 && error.status !== 401 && error.code !== 'docker_unavailable' && error.code !== 'shutting_down') {
      return fallback
    }
  }
  return describeDockerError(error, fallback)
}

/** Toast title for a finished action; `changed` false means Docker found it already in that state. */
export function containerActionTitle(action: ContainerActionName, name: string, changed: boolean): string {
  if (changed) return `${PAST[action]} ${name}`
  return action === 'start' ? `${name} is already running` : `${name} is already stopped`
}

/** Which actions a container offers: exited or crashed start, running stop or restart, the rest none. */
export function actionsFor(health: ContainerHealth): ContainerActionName[] {
  if (health === 'exited' || health === 'crashed') return ['start']
  if (health === 'running') return ['stop', 'restart']
  return []
}
