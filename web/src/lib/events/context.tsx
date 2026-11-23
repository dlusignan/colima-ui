import { useEffect, useMemo, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { queryKeys } from '@/lib/api/keys'
import { EventStreamContext, type EventStreamContextValue } from './eventStreamContext'
import { JobOutputStore, MetricStore } from './stores'
import type { EventStreamSource } from './types'

/**
 * Wires a stream source to the stores and the query cache. Does not open the
 * connection itself. The value is stable, so it never re-renders the tree.
 */
export function EventStreamBridge({
  client,
  children,
}: {
  client: EventStreamSource
  children: ReactNode
}) {
  const queryClient = useQueryClient()
  const value = useMemo<EventStreamContextValue>(
    () => ({ client, jobs: new JobOutputStore(), metrics: new MetricStore() }),
    [client],
  )

  useEffect(() => {
    const offs = [
      client.subscribe('job.output', (e) => value.jobs.output(e)),
      client.subscribe('job.finished', (e) => {
        value.jobs.finish(e)
        void queryClient.invalidateQueries({
          queryKey: queryKeys.job(e.jobId),
        })
        void queryClient.invalidateQueries({ queryKey: queryKeys.profiles })
      }),
      client.subscribe('metric', (e) => value.metrics.record(e)),
      // Events are not replayed after a drop, so reload what REST can give.
      client.onResync(() => {
        void queryClient.invalidateQueries({ queryKey: queryKeys.profiles })
        void queryClient.invalidateQueries({ queryKey: ['jobs'] })
      }),
    ]
    return () => offs.forEach((off) => off())
  }, [client, value, queryClient])

  return <EventStreamContext.Provider value={value}>{children}</EventStreamContext.Provider>
}
