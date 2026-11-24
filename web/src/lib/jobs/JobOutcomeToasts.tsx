import { useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '@/lib/api/endpoints'
import type { JobFinishedEvent } from '@/lib/api/types'
import { useEventListener } from '@/lib/events/hooks'
import { useToast } from '@/lib/toast/useToast'
import { logPath } from '@/views/log/logPath'

/** Names that read better than the API's action keys. */
const ACTION_LABEL: Record<string, string> = { 'image-pull': 'image pull' }

/** `default: stop succeeded`, `default: start failed (exit 1)`. The action is unknown when the lookup fails. */
function outcomeTitle(event: JobFinishedEvent, action: string | undefined): string {
  const exit = event.state === 'failed' && event.exitCode != null ? ` (exit ${event.exitCode})` : ''
  return `${event.profile}: ${(action && ACTION_LABEL[action]) ?? action ?? 'job'} ${event.state}${exit}`
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
    const path = logPath(event.profile, event.jobId)
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
        action: { label: 'View log', href: path, onClick: () => navigate(path) },
      })
    })()
  })
  return null
}
