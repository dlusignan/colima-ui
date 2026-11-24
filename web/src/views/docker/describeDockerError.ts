import { ApiError } from '@/lib/api/client'

/**
 * Turns a failed Docker manager call into text for a toast. Only `status` and
 * `code` are read; the server `message` is shown only for codes not listed
 * here. Returns an empty string for a rejected token, because the auth notice
 * already replaces the view (callers show no toast for it).
 */
export function describeDockerError(error: unknown, fallback: string): string {
  if (!(error instanceof ApiError)) return fallback
  if (error.status === 0) return 'Could not reach the server.'
  if (error.status === 401) return ''
  if (error.code === 'docker_unavailable') return 'Docker is not reachable in this VM yet.'
  if (error.code === 'shutting_down') return 'The server is shutting down.'
  if (error.status === 409) return 'That is in use or changed since the list loaded. Refresh the list and try again.'
  if (error.status === 422) return 'Docker rejected the request. Check the name or value and try again.'
  return error.message || fallback
}
