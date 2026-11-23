import type { QueryClient } from '@tanstack/react-query'
import { AppProviders } from '@/app/AppProviders'
import { AppRoutes } from '@/app/routes'

export default function App({ client }: { client?: QueryClient }) {
  return (
    <AppProviders client={client}>
      <AppRoutes />
    </AppProviders>
  )
}
