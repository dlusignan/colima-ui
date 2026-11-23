import type { ReactNode } from 'react'

/**
 * Pass-through stub. The SSE issue replaces this body with the single
 * per-tab event stream connection. Layout mounts it inside AuthGate, so it
 * only ever runs once the token is ok.
 */
export function EventStreamProvider({ children }: { children: ReactNode }) {
  return children
}
