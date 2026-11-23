import { queryOptions, useQuery } from '@tanstack/react-query'
import { api } from './endpoints'
import { queryKeys } from './keys'
import type { ProfileList } from './types'

/** Polling interval while no job runs. Each call runs `colima list` server side. */
export const IDLE_REFETCH_MS = 5_000
/** Polling interval while any profile has a job. */
export const BUSY_REFETCH_MS = 1_500

export function profilesRefetchInterval(data: ProfileList | undefined): number {
  return data?.profiles.some((p) => p.activeJobId) ? BUSY_REFETCH_MS : IDLE_REFETCH_MS
}

export const profilesQueryOptions = queryOptions({
  queryKey: queryKeys.profiles,
  queryFn: ({ signal }) => api.listProfiles(signal),
  refetchInterval: (query) => profilesRefetchInterval(query.state.data),
  refetchOnWindowFocus: true,
})

/** The profile list, polled; background tabs do not poll. */
export function useProfiles() {
  return useQuery(profilesQueryOptions)
}
