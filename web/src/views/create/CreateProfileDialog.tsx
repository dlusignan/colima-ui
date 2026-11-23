import { useEffect, useId, useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import { logPath } from '@/views/log/logPath'
import {
  buildRequest,
  EMPTY_VALUES,
  FIELD_NAMES,
  NUMBER_LIMITS,
  SELECT_OPTIONS,
  validateCreate,
  type CreateValues,
  type FieldErrors,
  type FieldName,
  type NumberField,
  type SelectField,
} from './validateCreate'

const NUMBER_META: Record<NumberField, { unit: string; step: string; placeholder: string }> = {
  cpus: { unit: 'cores', step: '1', placeholder: '2' },
  memoryGiB: { unit: 'GiB', step: '0.5', placeholder: '2' },
  diskGiB: { unit: 'GiB', step: '1', placeholder: '100' },
  rootDiskGiB: { unit: 'GiB', step: '1', placeholder: '20' },
}

const SELECT_CLASS =
  'h-8 w-full min-w-0 rounded-lg border border-input bg-transparent px-2 py-1 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive md:text-sm dark:bg-input/30'

/** Field messages from a 422, verbatim from the server. */
function fieldErrorsFrom(error: unknown): FieldErrors {
  if (!(error instanceof ApiError) || error.status !== 422) return {}
  const fields = error.details.fields
  if (typeof fields !== 'object' || fields === null) return {}
  const out: FieldErrors = {}
  for (const f of FIELD_NAMES) {
    const v = (fields as Record<string, unknown>)[f]
    if (typeof v === 'string' && v) out[f] = v
  }
  return out
}

function describeError(error: unknown, name: string): string {
  if (!(error instanceof ApiError)) return 'Could not create the profile.'
  switch (error.status) {
    case 401:
      return ''
    case 422:
      return 'Some values were rejected. Fix the marked fields and try again.'
    case 409:
      return error.code === 'profile_exists'
        ? `A profile named ${name} already exists. Pick another name.`
        : 'A job is already running on a profile with this name. Wait for it to finish, then try again.'
    case 0:
      return 'Could not reach the server. Check that it is still running, then try again.'
    case 503:
      return error.code === 'shutting_down'
        ? 'The server is shutting down.'
        : 'The server is unavailable. Try again.'
    case 502:
      return 'Colima could not be started. Try again.'
    default:
      return error.message || 'Could not create the profile.'
  }
}

function describe(id: string, hint: boolean, error?: string) {
  return [hint ? `${id}-hint` : null, error ? `${id}-error` : null].filter(Boolean).join(' ') || undefined
}

function Message({ id, error, hint }: { id: string; error?: string; hint?: string }) {
  return (
    <>
      {hint && (
        <p id={`${id}-hint`} className="text-xs text-muted-foreground">
          {hint}
        </p>
      )}
      {error && (
        <p id={`${id}-error`} className="text-xs text-destructive">
          {error}
        </p>
      )}
    </>
  )
}

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function CreateProfileDialog({ open, onOpenChange }: Props) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="top-[6%] max-h-[88vh] max-w-xl overflow-y-auto">
        <CreateForm onClose={() => onOpenChange(false)} />
      </DialogContent>
    </Dialog>
  )
}

function CreateForm({ onClose }: { onClose: () => void }) {
  const formId = useId()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [values, setValues] = useState<CreateValues>(EMPTY_VALUES)
  const [clientErrors, setClientErrors] = useState<FieldErrors>({})
  const [serverErrors, setServerErrors] = useState<FieldErrors>({})
  const [summary, setSummary] = useState('')
  const [focusRequest, setFocusRequest] = useState<{ field: FieldName; n: number } | null>(null)

  const create = useMutation({ mutationFn: api.createProfile })

  useEffect(() => {
    if (focusRequest) document.getElementById(`${formId}-${focusRequest.field}`)?.focus()
  }, [focusRequest, formId])

  const firstInvalid = (errors: FieldErrors) => FIELD_NAMES.find((f) => errors[f])
  const requestFocus = (field: FieldName) => setFocusRequest((r) => ({ field, n: (r?.n ?? 0) + 1 }))

  function edit<K extends keyof CreateValues>(field: K, value: CreateValues[K]) {
    setValues((v) => ({ ...v, [field]: value }))
    setClientErrors((e) => ({ ...e, [field]: undefined }))
    setServerErrors((e) => ({ ...e, [field]: undefined }))
  }

  function onSubmit(e: FormEvent) {
    e.preventDefault()
    if (create.isPending) return
    setSummary('')
    const errors = validateCreate(values)
    setClientErrors(errors)
    const bad = firstInvalid(errors)
    if (bad) {
      setServerErrors({})
      setSummary('Fix the marked fields and try again.')
      requestFocus(bad)
      return
    }
    const body = buildRequest(values)
    create.mutate(body, {
      onSuccess: ({ jobId, profile }) => {
        setServerErrors({})
        void queryClient.invalidateQueries({ queryKey: queryKeys.profiles })
        onClose()
        void navigate(logPath(profile, jobId))
      },
      onError: (error) => {
        let fields = fieldErrorsFrom(error)
        if (error instanceof ApiError && error.code === 'profile_exists') {
          fields = { name: describeError(error, body.name) }
        }
        setServerErrors(fields)
        setSummary(describeError(error, body.name))
        const bad = firstInvalid(fields)
        if (bad) requestFocus(bad)
      },
    })
  }

  const errorFor = (f: FieldName) => serverErrors[f] ?? clientErrors[f]
  const pending = create.isPending

  const nameId = `${formId}-name`
  const versionId = `${formId}-kubernetesVersion`

  return (
    <>
      <DialogTitle>New profile</DialogTitle>
      <DialogDescription>
        Creates a new Colima VM with the settings below. Leave a field empty to use Colima&apos;s default. The
        first start downloads a VM image and can take several minutes. The output opens when you create it.
      </DialogDescription>
      <form onSubmit={onSubmit} noValidate aria-label="New profile" className="flex flex-col gap-3">
        <div className="flex flex-col gap-1">
          <label htmlFor={nameId} className="text-sm font-medium">
            Name
          </label>
          <Input
            id={nameId}
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            value={values.name}
            disabled={pending}
            aria-required="true"
            aria-invalid={errorFor('name') ? true : undefined}
            aria-describedby={describe(nameId, true, errorFor('name'))}
            onChange={(e) => edit('name', e.target.value)}
          />
          <Message
            id={nameId}
            error={errorFor('name')}
            hint="Letters, digits, dots, underscores and hyphens. Names starting with colima are reserved."
          />
        </div>

        <div className="grid grid-cols-2 gap-3">
          {(Object.keys(NUMBER_LIMITS) as NumberField[]).map((f) => {
            const id = `${formId}-${f}`
            const error = errorFor(f)
            return (
              <div key={f} className="flex flex-col gap-1">
                <label htmlFor={id} className="text-sm font-medium">
                  {NUMBER_LIMITS[f].label} ({NUMBER_META[f].unit})
                </label>
                <Input
                  id={id}
                  type="number"
                  step={NUMBER_META[f].step}
                  inputMode="decimal"
                  placeholder={NUMBER_META[f].placeholder}
                  value={values[f]}
                  disabled={pending}
                  aria-invalid={error ? true : undefined}
                  aria-describedby={describe(id, false, error)}
                  onChange={(e) => edit(f, e.target.value)}
                />
                <Message id={id} error={error} />
              </div>
            )
          })}
        </div>

        <div className="grid grid-cols-2 gap-3">
          {(Object.keys(SELECT_OPTIONS) as SelectField[]).map((f) => {
            const id = `${formId}-${f}`
            const error = errorFor(f)
            return (
              <div key={f} className="flex flex-col gap-1">
                <label htmlFor={id} className="text-sm font-medium">
                  {SELECT_OPTIONS[f].label}
                </label>
                <select
                  id={id}
                  className={SELECT_CLASS}
                  value={values[f]}
                  disabled={pending}
                  aria-invalid={error ? true : undefined}
                  aria-describedby={describe(id, false, error)}
                  onChange={(e) => edit(f, e.target.value)}
                >
                  <option value="">Default</option>
                  {SELECT_OPTIONS[f].options.map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </select>
                <Message id={id} error={error} />
              </div>
            )
          })}
        </div>

        <div className="flex flex-col gap-1">
          <label className="flex items-center gap-2 text-sm font-medium">
            <input
              type="checkbox"
              checked={values.kubernetes}
              disabled={pending}
              onChange={(e) => edit('kubernetes', e.target.checked)}
            />
            Kubernetes
          </label>
        </div>

        {values.kubernetes && (
          <div className="flex flex-col gap-1">
            <label htmlFor={versionId} className="text-sm font-medium">
              Kubernetes version
            </label>
            <Input
              id={versionId}
              autoComplete="off"
              spellCheck={false}
              placeholder="v1.31.2+k3s1"
              value={values.kubernetesVersion}
              disabled={pending}
              aria-invalid={errorFor('kubernetesVersion') ? true : undefined}
              aria-describedby={describe(versionId, false, errorFor('kubernetesVersion'))}
              onChange={(e) => edit('kubernetesVersion', e.target.value)}
            />
            <Message id={versionId} error={errorFor('kubernetesVersion')} />
          </div>
        )}

        <div role="alert" className="text-sm text-destructive">
          {summary}
        </div>

        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" disabled={pending} onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" disabled={pending}>
            {pending ? 'Creating...' : 'Create profile'}
          </Button>
        </div>
      </form>
    </>
  )
}
