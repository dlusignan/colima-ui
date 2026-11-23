import { lazy, Suspense } from 'react'

// The uPlot chunk stays out of the main bundle.
const CapacityView = lazy(() =>
  import('./CapacityView').then((m) => ({ default: m.CapacityView })),
)

export function LazyCapacityView() {
  return (
    <Suspense fallback={<p className="text-sm text-muted-foreground">Loading...</p>}>
      <CapacityView />
    </Suspense>
  )
}
