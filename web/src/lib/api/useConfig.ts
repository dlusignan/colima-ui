import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ApiError } from './client'
import { api } from './endpoints'
import { queryKeys } from './keys'
import type { ConfigUpdate } from './types'

/** The editable settings of one profile. A 404 is an answer, not worth retrying. */
export function useConfig(name: string) {
  return useQuery({
    queryKey: queryKeys.config(name),
    queryFn: ({ signal }) => api.getConfig(name, signal),
    retry: (count, error) => !(error instanceof ApiError && error.status === 404) && count < 2,
  })
}

/** Saves changed fields. The list shares cpus and memory with the file, so it is refetched. */
export function useSaveConfig(name: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (update: ConfigUpdate) => api.updateConfig(name, update),
    onSuccess: (result) => {
      queryClient.setQueryData(queryKeys.config(name), result.config)
      void queryClient.invalidateQueries({ queryKey: queryKeys.profiles })
    },
    onError: (error) => {
      if (error instanceof ApiError && error.status === 409) {
        void queryClient.invalidateQueries({ queryKey: queryKeys.profiles })
      }
    },
  })
}
