import { ApiError } from '@/lib/api/client'

export const noteClass = 'rounded-lg border border-border px-3 py-3 text-sm text-muted-foreground'

/** Error box with its own Retry; Retry is hidden when Docker is simply not up yet. */
export function SectionError({
  error,
  fallback,
  onRetry,
}: {
  error: unknown
  fallback: string
  onRetry: () => void
}) {
  let text = fallback
  let unavailable = false
  if (error instanceof ApiError) {
    if (error.code === 'docker_unavailable') {
      text = 'Docker is not reachable in this VM yet.'
      unavailable = true
    } else if (error.status === 0) {
      text = 'Could not reach the server.'
    }
  }
  return (
    <div role="alert" className="flex items-center justify-between gap-2 rounded-lg border border-destructive px-3 py-2 text-sm">
      <span>{text}</span>
      {!unavailable && (
        <button type="button" className="underline underline-offset-4" onClick={onRetry}>
          Retry
        </button>
      )}
    </div>
  )
}
