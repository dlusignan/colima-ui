import { lazy, Suspense } from 'react'

// The xterm chunk stays out of the main bundle.
const LogView = lazy(() => import('./LogView').then((m) => ({ default: m.LogView })))

export function LazyLogView() {
  return (
    <Suspense fallback={<p className="text-sm text-muted-foreground">Loading...</p>}>
      <LogView />
    </Suspense>
  )
}
