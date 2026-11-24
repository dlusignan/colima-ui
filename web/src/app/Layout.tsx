import { useState } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'
import { CommandPalette } from '@/components/CommandPalette'
import { ShortcutsDialog } from '@/components/ShortcutsDialog'
import { Button } from '@/components/ui/button'
import { ThemeSwitcher } from '@/components/ThemeSwitcher'
import { JobOutcomeToasts } from '@/lib/jobs/JobOutcomeToasts'
import { EventStreamProvider } from '@/lib/events/EventStreamProvider'
import { Toaster } from '@/lib/toast/Toaster'
import { ToastProvider } from '@/lib/toast/ToastProvider'
import { CreateProfileProvider } from '@/views/create/CreateProfileProvider'
import { AuthGate } from './AuthGate'
import { ErrorBoundary } from './ErrorBoundary'

export function Layout() {
  const { pathname } = useLocation()
  const [helpOpen, setHelpOpen] = useState(false)
  return (
    <ToastProvider>
      <div className="min-h-screen bg-background text-foreground">
        <header className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-border px-4 py-3 sm:px-6 xl:px-8">
          <div className="flex flex-wrap items-center gap-x-6 gap-y-1">
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
          <div className="flex flex-wrap items-center gap-3">
            <Button size="sm" variant="outline" onClick={() => setHelpOpen(true)}>
              Shortcuts
            </Button>
            <ThemeSwitcher />
          </div>
        </header>
        <main className="min-w-0 p-4 sm:p-6 xl:px-8">
          <AuthGate>
            <EventStreamProvider>
              <CreateProfileProvider>
                <JobOutcomeToasts />
                <CommandPalette onOpenHelp={() => setHelpOpen(true)} />
                <ErrorBoundary key={pathname}>
                  <Outlet />
                </ErrorBoundary>
              </CreateProfileProvider>
            </EventStreamProvider>
          </AuthGate>
        </main>
        <ShortcutsDialog open={helpOpen} onOpenChange={setHelpOpen} />
        <Toaster />
      </div>
    </ToastProvider>
  )
}
