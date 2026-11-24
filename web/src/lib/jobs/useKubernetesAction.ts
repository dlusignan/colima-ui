import { useMutation, useQueryClient } from '@tanstack/react-query'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import type { JobRef, KubernetesAction, ProfileList } from '@/lib/api/types'
import type { JobTracker } from './useJobTracker'

function call(action: KubernetesAction, name: string): Promise<JobRef> {
  switch (action) {
    case 'kubernetes-start':
      return api.kubernetesStart(name)
    case 'kubernetes-stop':
      return api.kubernetesStop(name)
    case 'kubernetes-reset':
      return api.kubernetesReset(name)
    case 'kubernetes-delete':
      return api.kubernetesDelete(name)
  }
}

/** Short verb for messages: `kubernetes-reset` becomes `reset`. */
export function kubernetesVerb(action: KubernetesAction): string {
  return action.replace('kubernetes-', '')
}

interface Description {
  message: string
  refresh: boolean
  trackJobId?: string
}

/** Text for a failed Kubernetes action. Only `code`, `status` and `details` are read, never the server's message. */
export function describeKubernetesError(error: unknown, action: KubernetesAction, profile: string): Description {
  const verb = kubernetesVerb(action)
  if (!(error instanceof ApiError)) return { message: `Could not ${verb} Kubernetes on ${profile}.`, refresh: false }
  if (error.status === 0) return { message: 'Could not reach the server.', refresh: false }
  if (error.status === 401) return { message: '', refresh: false }
  switch (error.code) {
    case 'profile_busy': {
      const id = error.details.jobId
      return {
        message: `Another job is already running on ${profile}.`,
        refresh: true,
        trackJobId: typeof id === 'string' ? id : undefined,
      }
    }
    case 'invalid_state':
      if (error.details.reason === 'kubernetes_not_enabled') {
        return { message: `Kubernetes is not enabled on ${profile}.`, refresh: true }
      }
      return { message: `${profile} is not running, so Kubernetes cannot be changed.`, refresh: true }
    case 'profile_not_found':
      return { message: `${profile} no longer exists.`, refresh: true }
    case 'colima_failed':
      return { message: 'Colima could not run the command.', refresh: false }
    case 'shutting_down':
      return { message: 'The server is shutting down.', refresh: false }
    default:
      return { message: `Could not ${verb} Kubernetes on ${profile}.`, refresh: false }
  }
}

/**
 * Runs one Kubernetes action for a profile. Like the profile actions: on 202 the
 * job is adopted by the tracker and written into the cached list as
 * `activeJobId`, so every button disables at once. `pending` covers the gap
 * between click and 202, and a second request is ignored while it is set.
 */
export function useKubernetesAction(
  profile: string,
  tracker: Pick<JobTracker, 'adopt'>,
  onError?: (message: string) => void,
) {
  const queryClient = useQueryClient()
  const mutation = useMutation({
    mutationFn: ({ action }: { action: KubernetesAction }) => call(action, profile),
    onSuccess: ({ jobId }) => {
      queryClient.setQueryData<ProfileList>(queryKeys.profiles, (list) =>
        list && { ...list, profiles: list.profiles.map((p) => (p.name === profile ? { ...p, activeJobId: jobId } : p)) },
      )
      void queryClient.invalidateQueries({ queryKey: queryKeys.profiles })
    },
  })
  return {
    pending: mutation.isPending,
    run: (action: KubernetesAction) => {
      if (mutation.isPending) return
      mutation.mutate(
        { action },
        {
          onSuccess: ({ jobId }) => tracker.adopt(profile, jobId),
          onError: (error) => {
            const d = describeKubernetesError(error, action, profile)
            if (d.trackJobId) tracker.adopt(profile, d.trackJobId)
            if (d.refresh) {
              void queryClient.invalidateQueries({ queryKey: queryKeys.profiles })
              void queryClient.invalidateQueries({ queryKey: queryKeys.kubernetes(profile) })
            }
            if (d.message) onError?.(d.message)
          },
        },
      )
    },
  }
}
