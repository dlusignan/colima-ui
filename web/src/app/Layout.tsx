import { NavLink, Outlet, useLocation } from 'react-router-dom'
import { ThemeSwitcher } from '@/components/ThemeSwitcher'
import { EventStreamProvider } from '@/lib/events/EventStreamProvider'
import { AuthGate } from './AuthGate'
import { ErrorBoundary } from './ErrorBoundary'

export function Layout() {
  const { pathname } = useLocation()
  return (
    <div className="min-h-screen bg-background text-foreground">
      <header className="flex items-center justify-between gap-4 border-b border-border px-6 py-3">
        <div className="flex items-center gap-6">
          <h1 className="text-lg font-semibold">Colima UI</h1>
          <nav aria-label="Main">
            <NavLink
              to="/"
              end
              className="text-sm text-muted-foreground aria-[current=page]:text-foreground hover:text-foreground"
            >
              Dashboard
            </NavLink>
          </nav>
        </div>
        <ThemeSwitcher />
      </header>
      <main className="mx-auto max-w-5xl p-6">
        <AuthGate>
          <EventStreamProvider>
            <ErrorBoundary key={pathname}>
              <Outlet />
            </ErrorBoundary>
          </EventStreamProvider>
        </AuthGate>
      </main>
    </div>
  )
}
