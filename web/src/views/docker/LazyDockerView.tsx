import { lazy, Suspense } from 'react'

// The Docker tab stays out of the main bundle.
const DockerView = lazy(() => import('./DockerView').then((m) => ({ default: m.DockerView })))

export function LazyDockerView() {
  return (
    <Suspense fallback={<p className="text-sm text-muted-foreground">Loading...</p>}>
      <DockerView />
    </Suspense>
  )
}
