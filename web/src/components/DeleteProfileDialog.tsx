import { useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { ApiError } from '@/lib/api/client'
import type { Profile } from '@/lib/api/types'
import { useProfileContents } from '@/lib/api/useProfileContents'

interface DeleteProfileDialogProps {
  profile: Profile
  /** A job is running, or a request is in flight, for this profile. */
  busy: boolean
  /** Colima could not be queried: the real state is unknown. */
  unavailable: boolean
  onDelete: (deleteData: boolean) => void
}

const NOT_EMPTY = 'Docker elements must be removed before this VM can be deleted.'
const START_FIRST = 'Start the profile first so its Docker contents can be checked.'
const CANNOT_CHECK = 'Its Docker contents cannot be checked, so make sure nothing you need is inside.'

/** Why the button is off, or null when the dialog may open. */
function blockedReason(busy: boolean, unavailable: boolean) {
  if (busy) return 'Another job is running on this profile.'
  if (unavailable) return 'Colima is not available.'
  return null
}

/** Fixed text for a failed contents read; server text is never shown. */
function errorText(error: unknown): { text: string; retry: boolean } {
  if (error instanceof ApiError) {
    if (error.status === 0) return { text: 'Could not reach the server.', retry: true }
    if (error.code === 'invalid_state') {
      return error.details.status === 'stopped'
        ? { text: START_FIRST, retry: false }
        : { text: 'The profile is not running, so its Docker contents cannot be checked.', retry: false }
    }
    if (error.code === 'profile_not_found') return { text: 'This profile no longer exists.', retry: true }
    if (error.code === 'docker_unavailable') {
      return { text: 'Docker did not answer, so the profile cannot be shown to be empty.', retry: true }
    }
  }
  return { text: 'The Docker contents could not be checked.', retry: true }
}

function plural(n: number, one: string, many: string) {
  return `${n} ${n === 1 ? one : many}`
}

interface BodyProps {
  profile: Profile
  onClose: () => void
  onDelete: (deleteData: boolean) => void
}

/**
 * Mounted only while the dialog is open, so the contents are read once per
 * opening, the runtime-data box starts unchecked and a double confirm is
 * guarded per opening.
 */
function DeleteDialogBody({ profile, onClose, onDelete }: BodyProps) {
  const { name } = profile
  // A broken VM (listed as unknown) cannot be asked; the server decides.
  const broken = profile.status === 'unknown'
  const query = useProfileContents(name, !broken)
  const [deleteData, setDeleteData] = useState(false)
  const sent = useRef(false)
  const title = `Delete profile ${name}?`

  const contents = query.data
  const empty = contents !== undefined && (contents.containers ?? 0) + (contents.images ?? 0) + (contents.volumes ?? 0) === 0
  const confirmable = broken || (contents !== undefined && (!contents.applicable || empty))

  if (confirmable) {
    return (
      <ConfirmDialog
        open
        onOpenChange={(open) => {
          if (!open) onClose()
        }}
        title={title}
        description={`This removes the ${name} virtual machine and its configuration. It cannot be undone.`}
        confirmLabel="Delete"
        destructive
        requireText={name}
        onConfirm={() => {
          if (sent.current) return
          sent.current = true
          onDelete(deleteData)
        }}
      >
        <div className="flex flex-col gap-1 text-sm">
          {(broken || contents?.applicable === false) && (
            <p role="note" className="text-warning">
              {CANNOT_CHECK}
            </p>
          )}
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={deleteData} onChange={(e) => setDeleteData(e.target.checked)} />
            Also delete container runtime data
          </label>
          <p className="text-muted-foreground">
            The images, volumes and containers stored inside the virtual machine are removed too.
            Without this box they stay on disk and a profile created later with the same name
            can use them again.
          </p>
        </div>
      </ConfirmDialog>
    )
  }

  let content
  if (query.isPending) {
    content = <p className="text-sm text-muted-foreground">Checking what the profile holds…</p>
  } else if (query.isError) {
    const { text, retry } = errorText(query.error)
    content = (
      <div role="alert" className="flex flex-col items-start gap-2 rounded-lg border border-destructive px-3 py-2 text-sm">
        <p>{text}</p>
        {retry && (
          <Button type="button" variant="outline" size="sm" onClick={() => void query.refetch()}>
            Retry
          </Button>
        )}
      </div>
    )
  } else if (contents) {
    content = (
      <div role="alert" className="flex flex-col gap-2 rounded-lg border border-destructive px-3 py-2 text-sm">
        <p>{NOT_EMPTY}</p>
        <p>
          It holds {plural(contents.containers ?? 0, 'container', 'containers')},{' '}
          {plural(contents.images ?? 0, 'image', 'images')} and {plural(contents.volumes ?? 0, 'volume', 'volumes')}.
          Remove them in the{' '}
          <Link className="underline underline-offset-4" to={`/profiles/${encodeURIComponent(name)}/docker`} onClick={onClose}>
            Docker tab
          </Link>
          , then try again.
        </p>
      </div>
    )
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent aria-busy={query.isPending}>
        <DialogTitle>{title}</DialogTitle>
        <DialogDescription>A profile can only be deleted while its Docker holds nothing.</DialogDescription>
        {content}
        <div className="flex justify-end">
          <Button type="button" size="sm" onClick={onClose}>
            Close
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}

/**
 * The Delete button and its dialog. The dialog asks the server what the VM
 * holds, then shows either the type-the-name confirmation (Cancel has the
 * focus, the Delete button waits for the exact name, the runtime-data box is
 * unchecked) or the reason the VM cannot be deleted yet.
 */
export function DeleteProfileDialog({ profile, busy, unavailable, onDelete }: DeleteProfileDialogProps) {
  const [open, setOpen] = useState(false)
  const reason = blockedReason(busy, unavailable)
  // A job started meanwhile: close, so the dialog does not come back when it ends.
  if (open && reason !== null) setOpen(false)
  return (
    <>
      <Button
        size="xs"
        variant="destructive"
        focusableWhenDisabled
        disabled={reason !== null}
        title={reason ?? undefined}
        aria-label={`Delete ${profile.name}`}
        onClick={() => setOpen(true)}
      >
        Delete
      </Button>
      {open && reason === null && (
        <DeleteDialogBody profile={profile} onClose={() => setOpen(false)} onDelete={onDelete} />
      )}
    </>
  )
}
