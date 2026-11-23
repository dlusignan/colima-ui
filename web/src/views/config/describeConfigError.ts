import { ApiError } from '@/lib/api/client'
import { FIELD_NAMES, type FieldErrors } from './validateConfig'

/** Field messages from a 422, verbatim from the server. */
export function fieldErrorsFrom(error: unknown): FieldErrors {
  if (!(error instanceof ApiError) || error.status !== 422) return {}
  const fields = error.details.fields
  if (typeof fields !== 'object' || fields === null) return {}
  const out: FieldErrors = {}
  for (const f of FIELD_NAMES) {
    const v = (fields as Record<string, unknown>)[f]
    if (typeof v === 'string' && v) out[f] = v
  }
  return out
}

/** One line for the alert region after a failed save. Empty means show nothing. */
export function describeConfigError(error: unknown): string {
  if (!(error instanceof ApiError)) return 'Could not save the configuration.'
  switch (error.status) {
    case 401:
      return ''
    case 422:
      return 'Some values were rejected. Fix the marked fields and save again.'
    case 409:
      return error.details.jobId
        ? 'A job is running on this profile. Wait for it to finish, then save again.'
        : 'The profile was busy. Try again.'
    case 404:
      return 'This profile has no configuration file.'
    case 0:
      return 'Could not reach the server. Check that it is still running.'
    case 503:
      return error.code === 'shutting_down'
        ? 'The server is shutting down.'
        : 'The server is unavailable. Try again.'
    default:
      return error.message || 'Could not save the configuration.'
  }
}
