import { createContext, useContext } from 'react'
import type { JobOutputStore, MetricStore } from './stores'
import type { EventStreamSource } from './types'

export interface EventStreamContextValue {
  client: EventStreamSource
  jobs: JobOutputStore
  metrics: MetricStore
}

export const EventStreamContext = createContext<EventStreamContextValue | null>(null)

export function useEventStreamContext(): EventStreamContextValue {
  const ctx = useContext(EventStreamContext)
  if (!ctx) throw new Error('Event stream hooks need an EventStreamProvider.')
  return ctx
}
