import type { ReactNode } from 'react'
import { EventStreamBridge } from './context'
import type { EventStreamSource } from './types'

/** Needs a QueryClientProvider above it. */
export function EventStreamTestProvider({
  client,
  children,
}: {
  client: EventStreamSource
  children: ReactNode
}) {
  return <EventStreamBridge client={client}>{children}</EventStreamBridge>
}
