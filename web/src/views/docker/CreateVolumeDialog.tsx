import { useId, useRef, useState, type FormEvent } from 'react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import { useDockerMutation } from './useDockerMutation'
import {
  buildCreateRequest,
  hasErrors,
  isBlankRow,
  MAX_LABELS,
  validateVolumeForm,
  type LabelRow,
} from './volumeRules'

interface ServerErrors {
  name?: string
  labels?: string
}

/** Maps a 409 or 422 of the create route to the field it belongs to. Other errors are not handled here. */
function serverErrorsFrom(error: unknown): ServerErrors | null {
  if (!(error instanceof ApiError)) return null
  if (error.status === 409 && error.code === 'conflict') return { name: 'A volume with this name already exists.' }
  if (error.status === 422) {
    const fields = error.details.fields
    const out: ServerErrors = {}
    if (typeof fields === 'object' && fields !== null) {
      const f = fields as Record<string, unknown>
      if (typeof f.name === 'string' && f.name) out.name = f.name
      if (typeof f.labels === 'string' && f.labels) out.labels = f.labels
    }
    if (!out.name && !out.labels) out.name = 'Docker rejected this request. Check the name and the labels.'
    return out
  }
  return null
}

const errorText = 'text-xs text-destructive'

/** Dialog to create a local volume: a name and up to 16 optional labels. Nothing is sent until the form is valid. */
export function CreateVolumeDialog({
  profile,
  open,
  onOpenChange,
}: {
  profile: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [server, setServer] = useState<ServerErrors>({})
  const mutation = useDockerMutation({
    profile,
    section: 'volumes',
    action: 'volume-create',
    mutationFn: (body: ReturnType<typeof buildCreateRequest>) => api.createVolume(profile, body),
    successTitle: (_d, v) => `Created volume ${v.name}`,
    errorTitle: (v) => `Could not create volume ${v.name}`,
    onSuccess: () => onOpenChange(false),
    handleError: (error) => {
      const mapped = serverErrorsFrom(error)
      if (!mapped) return false
      setServer(mapped)
      return true
    },
  })
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="top-[8%] max-h-[85vh] max-w-lg overflow-y-auto">
        <DialogTitle>Create volume</DialogTitle>
        <DialogDescription>
          Creates an empty local volume in {profile}. The driver is always local. Labels are optional.
        </DialogDescription>
        <CreateVolumeForm
          server={server}
          clearServer={(field) => setServer((s) => ({ ...s, [field]: undefined }))}
          pending={mutation.isPending}
          onCancel={() => onOpenChange(false)}
          onSubmit={(body) => {
            setServer({})
            mutation.run(body)
          }}
        />
      </DialogContent>
    </Dialog>
  )
}

function CreateVolumeForm({
  server,
  clearServer,
  pending,
  onCancel,
  onSubmit,
}: {
  server: ServerErrors
  clearServer: (field: keyof ServerErrors) => void
  pending: boolean
  onCancel: () => void
  onSubmit: (body: ReturnType<typeof buildCreateRequest>) => void
}) {
  const [name, setName] = useState('')
  const [rows, setRows] = useState<LabelRow[]>([])
  const [submitted, setSubmitted] = useState(false)
  const nextId = useRef(1)
  const nameId = useId()
  const nameErrorId = useId()
  const labelsErrorId = useId()
  const errors = validateVolumeForm(name, rows)
  const shown = submitted ? errors : { rows: {} as typeof errors.rows }
  const nameError = shown.name ?? server.name
  const labelsError = shown.labels ?? server.labels
  const filled = rows.filter((r) => !isBlankRow(r)).length

  function submit(e: FormEvent) {
    e.preventDefault()
    setSubmitted(true)
    if (pending || hasErrors(errors)) return
    onSubmit(buildCreateRequest(name, rows))
  }
  const update = (id: number, patch: Partial<LabelRow>) => {
    clearServer('labels')
    setRows((rs) => rs.map((r) => (r.id === id ? { ...r, ...patch } : r)))
  }

  return (
    <form onSubmit={submit} noValidate className="flex flex-col gap-4">
      <div className="flex flex-col gap-1.5 text-sm">
        <label htmlFor={nameId}>Name</label>
        <Input
          id={nameId}
          value={name}
          onChange={(e) => {
            clearServer('name')
            setName(e.target.value)
          }}
          autoComplete="off"
          spellCheck={false}
          autoFocus
          aria-invalid={nameError ? true : undefined}
          aria-describedby={nameError ? nameErrorId : undefined}
        />
        {nameError && (
          <p id={nameErrorId} className={errorText}>
            {nameError}
          </p>
        )}
      </div>

      <fieldset className="flex flex-col gap-2" aria-describedby={labelsError ? labelsErrorId : undefined}>
        <legend className="text-sm">Labels (optional, at most {MAX_LABELS})</legend>
        {rows.map((row, i) => {
          const n = i + 1
          const e = shown.rows[row.id] ?? {}
          return (
            <div key={row.id} role="group" aria-label={`Label ${n}`} className="flex flex-col gap-1">
              <div className="flex items-start gap-2">
                <Input
                  aria-label={`Label ${n} key`}
                  placeholder="key"
                  value={row.key}
                  onChange={(ev) => update(row.id, { key: ev.target.value })}
                  autoComplete="off"
                  spellCheck={false}
                  aria-invalid={e.key ? true : undefined}
                  aria-describedby={e.key ? `${nameErrorId}-k${row.id}` : undefined}
                />
                <Input
                  aria-label={`Label ${n} value`}
                  placeholder="value"
                  value={row.value}
                  onChange={(ev) => update(row.id, { value: ev.target.value })}
                  autoComplete="off"
                  spellCheck={false}
                  aria-invalid={e.value ? true : undefined}
                  aria-describedby={e.value ? `${nameErrorId}-v${row.id}` : undefined}
                />
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  aria-label={`Remove label ${n}`}
                  onClick={() => {
                    clearServer('labels')
                    setRows((rs) => rs.filter((r) => r.id !== row.id))
                  }}
                >
                  Remove
                </Button>
              </div>
              {e.key && (
                <p id={`${nameErrorId}-k${row.id}`} className={errorText}>
                  Key {n}: {e.key}
                </p>
              )}
              {e.value && (
                <p id={`${nameErrorId}-v${row.id}`} className={errorText}>
                  Value {n}: {e.value}
                </p>
              )}
            </div>
          )
        })}
        {labelsError && (
          <p id={labelsErrorId} className={errorText}>
            {labelsError}
          </p>
        )}
        <div className="flex items-center gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={rows.length >= MAX_LABELS}
            onClick={() => setRows((rs) => [...rs, { id: nextId.current++, key: '', value: '' }])}
          >
            Add label
          </Button>
          {rows.length >= MAX_LABELS && <span className="text-xs text-muted-foreground">Limit of {MAX_LABELS} labels reached.</span>}
          {filled > 0 && rows.length < MAX_LABELS && (
            <span className="text-xs text-muted-foreground">
              {filled} of {MAX_LABELS}
            </span>
          )}
        </div>
      </fieldset>

      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" disabled={pending}>
          {pending ? 'Creating…' : 'Create volume'}
        </Button>
      </div>
    </form>
  )
}
