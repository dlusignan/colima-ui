import { useQuery } from '@tanstack/react-query'
import { api } from './endpoints'
import { queryKeys } from './keys'

export const IMAGES_REFETCH_MS = 30_000

/** Images of a profile, refetched every 30 s while the page is visible and on focus. Idle while `enabled` is false. */
export function useImages(profile: string, enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.images(profile),
    queryFn: ({ signal }) => api.getImages(profile, signal),
    enabled,
    refetchInterval: IMAGES_REFETCH_MS,
    refetchOnWindowFocus: true,
    retry: false,
  })
}
