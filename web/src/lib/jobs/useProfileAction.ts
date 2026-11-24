import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import type { ProfileAction, JobRef, ProfileList } from '@/lib/api/types'

function call(action: ProfileAction, name: string): Promise<JobRef> {
  switch (action) {
    case 'start':
      return api.startProfile(name)
    case 'stop':
      return api.stopProfile(name)
    case 'restart':
      return api.restartProfile(name)
    case 'update':
      return api.updateProfile(name)
  }
}

/**
 * Start, stop or restart one profile. Pessimistic: the status never changes on
 * a guess. On 202 the returned job id is written into the cached list as
 * `activeJobId`, so buttons disable at once, and the list is refetched.
 * `mutation.isPending` covers the gap between click and 202.
 */
export function useProfileAction(profile: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ action }: { action: ProfileAction }) => call(action, profile),
    onSuccess: ({ jobId }) => {
      queryClient.setQueryData<ProfileList>(queryKeys.profiles, (list) =>
        list && {
          ...list,
          profiles: list.profiles.map((p) =>
            p.name === profile ? { ...p, activeJobId: jobId } : p,
          ),
        },
      )
      void queryClient.invalidateQueries({ queryKey: queryKeys.profiles })
    },
  })
}
