import { useEffect, useState, type ReactNode } from 'react'
import { useAuthStatus } from '@/lib/auth/useAuthStatus'
import { EventStreamBridge } from './context'
import { EventStreamClient } from './EventStreamClient'

/**
 * Owns the one per-tab event stream connection. Layout mounts it inside
 * AuthGate, so it opens once the token is ok and closes on unmount or when
 * the token stops being ok.
 */
export function EventStreamProvider({ children }: { children: ReactNode }) {
  const [client] = useState(() => new EventStreamClient())
  const auth = useAuthStatus()

  useEffect(() => {
    if (auth !== 'ok') {
      client.close()
      return
    }
    client.connect()
    return () => client.close()
  }, [client, auth])

  return <EventStreamBridge client={client}>{children}</EventStreamBridge>
}
