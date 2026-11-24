import { ApiError } from '@/lib/api/client'
import type { ProfileAction } from '@/lib/api/types'

export interface ActionErrorDescription {
  message: string
  /** The screen was stale or the state changed: refetch the profile list. */
  refresh: boolean
  /** A job already running on the profile that the row should adopt. */
  trackJobId?: string
}

/**
 * Turns a failed start, stop, restart or delete into text for the user. Server
 * `message` text is never parsed; only `code`, `status` and `details`.
 */
export function describeActionError(
  error: unknown,
  action: ProfileAction | 'delete',
  profile: string,
): ActionErrorDescription {
  if (!(error instanceof ApiError)) {
    return { message: `Could not ${action} ${profile}.`, refresh: false }
  }
  if (error.status === 0) {
    return { message: 'Could not reach the server.', refresh: false }
  }
  if (error.status === 401) {
    // markTokenRejected() already swaps the view for the auth notice.
    return { message: '', refresh: false }
  }
  switch (error.code) {
    case 'profile_busy': {
      const id = error.details.jobId
      return {
        message: `Another job is already running on ${profile}.`,
        refresh: true,
        trackJobId: typeof id === 'string' ? id : undefined,
      }
    }
    case 'invalid_state': {
      if (action === 'delete') {
        // Fixed texts keyed on the reason; server text is never shown.
        switch (error.details.reason) {
          case 'docker_not_empty':
            return { message: 'Docker elements must be removed before this VM can be deleted.', refresh: false }
          case 'cannot_verify':
            return { message: 'Start the profile first so its Docker contents can be checked.', refresh: true }
          case 'task_running':
            return { message: `A task is running on ${profile}. Wait for it to finish, then delete it.`, refresh: true }
          default:
            return { message: `${profile} is not in a state that allows deleting.`, refresh: true }
        }
      }
      const status = error.details.status
      return {
        message:
          typeof status === 'string'
            ? action === 'update'
              ? `${profile} is ${status}, so its runtime cannot be updated.`
              : `${profile} is already ${status}.`
            : `${profile} is not in a state that allows ${action}.`,
        refresh: true,
      }
    }
    case 'confirmation_required':
      return { message: `The name typed does not match ${profile}.`, refresh: false }
    case 'profile_not_found':
      return { message: `${profile} no longer exists.`, refresh: true }
    case 'colima_failed':
      return {
        message:
          'Colima could not run the command. See the log for details if a job was created.',
        refresh: false,
      }
    case 'docker_unavailable':
      return {
        message: `The Docker contents of ${profile} could not be read, so it was not deleted.`,
        refresh: false,
      }
    case 'shutting_down':
      return { message: 'The server is shutting down.', refresh: false }
    default:
      return { message: error.message, refresh: false }
  }
}
