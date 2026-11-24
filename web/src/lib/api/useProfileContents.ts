import { useQuery } from '@tanstack/react-query'
import { api } from './endpoints'
import { queryKeys } from './keys'

/**
 * What a running profile's Docker holds (docs/api.md 3.37). One request per
 * mount (mount it when the delete dialog opens), never polled, never retried
 * on its own, nothing kept after unmount: the answer is a snapshot.
 */
export function useProfileContents(profile: string, enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.contents(profile),
    queryFn: ({ signal }) => api.getContents(profile, signal),
    enabled,
    retry: false,
    gcTime: 0,
    staleTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  })
}
