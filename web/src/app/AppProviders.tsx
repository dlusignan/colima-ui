import { QueryClientProvider, type QueryClient } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { ErrorBoundary } from './ErrorBoundary'
import { queryClient } from './queryClient'

export function AppProviders({
  children,
  client = queryClient,
}: {
  children: ReactNode
  client?: QueryClient
}) {
  return (
    <ErrorBoundary>
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </ErrorBoundary>
  )
}
