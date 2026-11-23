import { useState } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'
import { CommandPalette } from '@/components/CommandPalette'
import { ShortcutsDialog } from '@/components/ShortcutsDialog'
import { Button } from '@/components/ui/button'
import { ThemeSwitcher } from '@/components/ThemeSwitcher'
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
          <div className="flex items-center gap-3">
            <Button size="sm" variant="outline" onClick={() => setHelpOpen(true)}>
              Shortcuts
            </Button>
            <ThemeSwitcher />
          </div>
        </header>
        <main className="mx-auto max-w-5xl p-6">
          <AuthGate>
            <EventStreamProvider>
              <CreateProfileProvider>
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
