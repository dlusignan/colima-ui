// Shared by the dashboard buttons and the command palette, so both follow the same
// enable rules and show the same confirmation wording.
import type { ProfileAction, Profile } from '@/lib/api/types'

/** Confirmation wording for Stop and Restart; the command palette reuses it. */
export const CONFIRM: Record<'stop' | 'restart', { description: (n: string) => string; label: string }> = {
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

/** Which actions are off: all while locked, Start when running, Stop when stopped. */
export function actionsDisabled(
  status: Profile['status'],
  locked: boolean,
): Record<ProfileAction, boolean> {
  return {
    start: locked || status === 'running',
    stop: locked || status === 'stopped',
    restart: locked,
  }
}
