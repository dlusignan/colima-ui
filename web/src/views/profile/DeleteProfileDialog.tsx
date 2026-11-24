import { useRef, useState } from 'react'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Button } from '@/components/ui/button'
import type { Profile } from '@/lib/api/types'

interface DeleteProfileDialogProps {
  profile: Profile
  /** A job is running, or a request is in flight, for this profile. */
  busy: boolean
  /** Colima could not be queried: the real state is unknown. */
  unavailable: boolean
  onDelete: (deleteData: boolean) => void
}

/** Why the button is off, or null when the profile can be deleted. */
function deleteBlockedReason(status: Profile['status'], busy: boolean, unavailable: boolean) {
  if (busy) return 'Another job is running on this profile.'
  if (unavailable) return 'Colima is not available.'
  // The list API reports a broken VM as 'unknown'; the server decides for real
  // (stopped and broken only), so 'unknown' stays enabled to clean up a broken one.
  if (status === 'running') return 'Stop the profile first.'
  return null
}

/**
 * Danger button plus the type-the-name confirmation. Cancel has the focus, the
 * Delete button waits for the exact profile name, and the runtime-data box is
 * unchecked every time the dialog opens.
 */
export function DeleteProfileDialog({ profile, busy, unavailable, onDelete }: DeleteProfileDialogProps) {
  const { name } = profile
  const [deleteData, setDeleteData] = useState(false)
  // Set at the first confirm until the dialog opens again, so a double click
  // cannot send two requests.
  const sent = useRef(false)
  const reason = deleteBlockedReason(profile.status, busy, unavailable)

  const button = (
    <Button
      size="xs"
      variant="destructive"
      focusableWhenDisabled
      disabled={reason !== null}
      aria-label={`Delete profile ${name}`}
    >
      Delete profile
    </Button>
  )

  return (
    <div className="flex flex-wrap items-center gap-3">
      {reason !== null ? (
        button
      ) : (
        <ConfirmDialog
          trigger={button}
          title={`Delete profile ${name}?`}
          description={`This removes the ${name} virtual machine and its configuration. It cannot be undone.`}
          confirmLabel="Delete"
          destructive
          requireText={name}
          onOpenChange={(open) => {
            if (open) sent.current = false // a new dialog may confirm once
            else setDeleteData(false)
          }}
          onConfirm={() => {
            if (sent.current) return
            sent.current = true
            onDelete(deleteData)
          }}
        >
          <div className="flex flex-col gap-1 text-sm">
            <label className="flex items-center gap-2">
              <input
                type="checkbox"
                checked={deleteData}
                onChange={(e) => setDeleteData(e.target.checked)}
              />
              Also delete container runtime data
            </label>
            <p className="text-muted-foreground">
              The images, volumes and containers stored inside the virtual machine are removed too.
              Without this box they stay on disk and a profile created later with the same name
              can use them again.
            </p>
          </div>
        </ConfirmDialog>
      )}
      {reason !== null && <span className="text-sm text-muted-foreground">{reason}</span>}
    </div>
  )
}
