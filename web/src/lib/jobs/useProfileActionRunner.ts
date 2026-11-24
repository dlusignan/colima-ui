import { useQueryClient } from '@tanstack/react-query'
import { queryKeys } from '@/lib/api/keys'
import type { ProfileAction } from '@/lib/api/types'
import { describeActionError } from './describeActionError'
import type { JobTracker } from './useJobTracker'
import { useDeleteProfile } from './useDeleteProfile'
import { useProfileAction } from './useProfileAction'

/**
 * Runs start, stop, restart or delete for one profile and wires the follow-up: the new
 * job is adopted by the tracker; a failure adopts a busy job, refreshes a stale
 * list and reports the user-facing text through `onError` (never empty).
 */
export function useProfileActionRunner(
  profile: string,
  tracker: Pick<JobTracker, 'adopt'>,
  onError?: (message: string) => void,
) {
  const queryClient = useQueryClient()
  const mutation = useProfileAction(profile)
  const deletion = useDeleteProfile(profile)
  return {
    pending: mutation.isPending || deletion.isPending,
    /** Delete after the dialog confirmed (typed name, Docker contents checked). */
    runDelete: (deleteData: boolean) => {
      if (deletion.isPending) return
      deletion.mutate(
        { deleteData },
        {
          onSuccess: ({ jobId }) => tracker.adopt(profile, jobId),
          onError: (error) => {
            const d = describeActionError(error, 'delete', profile)
            if (d.trackJobId) tracker.adopt(profile, d.trackJobId)
            if (d.refresh) void queryClient.invalidateQueries({ queryKey: queryKeys.profiles })
            if (d.message) onError?.(d.message)
          },
        },
      )
    },
    run: (action: ProfileAction) =>
      mutation.mutate(
        { action },
        {
          onSuccess: ({ jobId }) => tracker.adopt(profile, jobId),
          onError: (error) => {
            const d = describeActionError(error, action, profile)
            if (d.trackJobId) tracker.adopt(profile, d.trackJobId)
            if (d.refresh) void queryClient.invalidateQueries({ queryKey: queryKeys.profiles })
            if (d.message) onError?.(d.message)
          },
        },
      ),
  }
}
