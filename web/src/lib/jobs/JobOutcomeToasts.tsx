import { useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '@/lib/api/endpoints'
import type { JobFinishedEvent } from '@/lib/api/types'
import { useEventListener } from '@/lib/events/hooks'
import { useToast } from '@/lib/toast/useToast'
import { logPath } from '@/views/log/logPath'

/** Names that read better than the API's action keys. */
const ACTION_LABEL: Record<string, string> = {
  'image-pull': 'image pull',
  'cache-prune': 'download cache clear',
  'kubernetes-start': 'kubernetes start',
  'kubernetes-stop': 'kubernetes stop',
  'kubernetes-reset': 'kubernetes reset',
  'kubernetes-delete': 'kubernetes delete',
}

/** `default: stop succeeded`, `default: start failed (exit 1)`. The action is unknown when the lookup fails. */
function outcomeTitle(event: JobFinishedEvent, action: string | undefined): string {
  const exit = event.state === 'failed' && event.exitCode != null ? ` (exit ${event.exitCode})` : ''
  // A global job (cache prune) has no profile: no prefix.
  const prefix = event.profile ? `${event.profile}: ` : ''
  return `${prefix}${(action && ACTION_LABEL[action]) ?? action ?? 'job'} ${event.state}${exit}`
}

/**
 * Shows one toast per finished job, whatever page is open. Renders nothing.
 * `job.finished` carries no action name, so the job is read once from
 * `GET /api/jobs/{id}`. Only events received after mount count, so a reload
 * never replays old jobs. Needs the toast provider, the router and the event
 * stream above it.
 */
export function JobOutcomeToasts(): null {
  const toast = useToast()
  const navigate = useNavigate()
  const seen = useRef(new Set<string>())

  useEventListener('job.finished', (event) => {
    if (seen.current.has(event.jobId)) return
    seen.current.add(event.jobId)
    // The log view is per profile, so a global job has no log link.
    const path = event.profile ? logPath(event.profile, event.jobId) : null
    void (async () => {
      let action: string | undefined
      try {
        action = (await api.getJob(event.jobId)).action
      } catch {
        // Show the outcome without the action rather than nothing.
      }
      const show =
        event.state === 'succeeded' ? toast.success : event.state === 'failed' ? toast.error : toast.info
      show({
        title: outcomeTitle(event, action),
        key: event.jobId,
        ...(path && { action: { label: 'View log', href: path, onClick: () => navigate(path) } }),
      })
    })()
  })
  return null
}
