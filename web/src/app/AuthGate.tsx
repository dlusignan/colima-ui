import type { ReactNode } from 'react'
import { useAuthStatus } from '@/lib/auth/useAuthStatus'

/**
 * Renders children only while the token is usable, so no view mounts and no
 * request goes out without one.
 */
export function AuthGate({ children }: { children: ReactNode }) {
  const status = useAuthStatus()
  if (status === 'ok') return children
  return (
    <div
      role="alert"
      className="flex flex-col gap-2 rounded-lg border border-destructive/40 bg-card p-4"
    >
      <h2 className="text-lg font-semibold">
        {status === 'rejected' ? 'Token no longer valid' : 'Token missing'}
      </h2>
      <p className="text-sm text-muted-foreground">
        {status === 'rejected'
          ? 'The server no longer accepts this token. It changes every time the server starts, so this page is stale. '
          : 'This page has no access token. '}
        Reopen the link printed by the server in its terminal (it ends in{' '}
        <code className="font-mono">#token=...</code>).
      </p>
    </div>
  )
}
