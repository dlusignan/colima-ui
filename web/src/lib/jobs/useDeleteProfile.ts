import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import type { JobRef, ProfileList } from '@/lib/api/types'

/**
 * Deletes one profile (docs/api.md 3.31). The caller has already made the user
 * type the profile name, so `confirm` is that name. Pessimistic like
 * useProfileAction: on 202 the job id goes into the cached list as
 * `activeJobId` so every button disables at once, and the list is refetched.
 */
export function useDeleteProfile(profile: string) {
  const queryClient = useQueryClient()
  return useMutation<JobRef, unknown, { deleteData: boolean }>({
    mutationFn: ({ deleteData }) => api.deleteProfile(profile, { confirm: profile, deleteData }),
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
