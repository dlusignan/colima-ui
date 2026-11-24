import { useState } from 'react'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Button } from '@/components/ui/button'
import { useCachePrune } from './useCachePrune'

/**
 * Dashboard card for jobs that belong to no profile. For now one action:
 * clearing Colima's download cache (`colima prune`). The confirm dialog says
 * the next create or start may download again, and offers Lima's own assets
 * (`--all`) as an unchecked box that resets every time the dialog opens.
 */
export function MaintenanceCard() {
  const prune = useCachePrune()
  const [includeLima, setIncludeLima] = useState(false)

  const button = (
    <Button size="sm" variant="outline" focusableWhenDisabled disabled={prune.running}>
      Clear download cache
    </Button>
  )

  return (
    <section aria-labelledby="maintenance-heading" className="flex flex-col gap-2 rounded-lg border border-border px-3 py-3">
      <h3 id="maintenance-heading" className="text-sm font-semibold">
        Maintenance
      </h3>
      <p className="text-sm text-muted-foreground">
        Colima keeps the VM images it downloaded on disk. Clearing them frees space; the next create or
        start may download again.
      </p>
      {prune.running ? (
        <div className="flex flex-wrap items-center gap-3 text-sm" role="status">
          <div className="flex min-w-0 flex-1 flex-col">
            <span>{prune.cancelling ? 'Cancelling…' : 'Clearing the download cache…'}</span>
            {prune.lastLine && (
              <span className="truncate font-mono text-xs text-muted-foreground">{prune.lastLine}</span>
            )}
          </div>
          <Button
            size="sm"
            variant="outline"
            disabled={prune.cancelling}
            focusableWhenDisabled
            onClick={prune.cancel}
          >
            {prune.cancelling ? 'Cancelling…' : 'Cancel'}
          </Button>
        </div>
      ) : (
        <div>
          <ConfirmDialog
            trigger={button}
            title="Clear the download cache?"
            description="This deletes the VM images Colima has cached. Profiles keep working, but the next time you create or start one, Colima may download its image again, which can take several minutes."
            confirmLabel="Clear cache"
            destructive
            onOpenChange={(open) => {
              if (!open) setIncludeLima(false)
            }}
            onConfirm={() => prune.run(includeLima)}
          >
            <div className="flex flex-col gap-1 text-sm">
              <label className="flex items-center gap-2">
                <input
                  type="checkbox"
                  checked={includeLima}
                  onChange={(e) => setIncludeLima(e.target.checked)}
                />
                Also clear Lima assets
              </label>
              <p className="text-muted-foreground">
                Lima is the layer under Colima. Its cached assets are removed too, so more may need to
                be downloaded again. If you cancel while it runs, some files may already be gone.
              </p>
            </div>
          </ConfirmDialog>
        </div>
      )}
    </section>
  )
}
