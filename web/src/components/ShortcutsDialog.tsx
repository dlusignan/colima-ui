import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { shortcuts } from '@/lib/commands/shortcuts'

interface ShortcutsDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

/** Lists the shortcuts table; Escape closes it. */
export function ShortcutsDialog({ open, onOpenChange }: ShortcutsDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogTitle>Keyboard shortcuts</DialogTitle>
        <DialogDescription>
          Shortcuts that act on a profile use the profile of the open page, the focused table row, or the
          only profile. Otherwise the palette asks you to pick one.
        </DialogDescription>
        <dl className="grid grid-cols-[auto_1fr] items-center gap-x-4 gap-y-2 text-sm">
          {shortcuts.map((s) => (
            <div key={s.id} className="contents">
              <dt>
                <kbd className="rounded border border-border bg-muted px-1.5 py-0.5 font-mono text-xs">
                  {s.keys}
                </kbd>
              </dt>
              <dd>{s.description}</dd>
            </div>
          ))}
        </dl>
      </DialogContent>
    </Dialog>
  )
}
