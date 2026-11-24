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
import { AdvancedFields, type RowHandlers } from './AdvancedFields'
import { describe, SELECT_CLASS } from './formStyle'
import { Message } from './formParts'
import {
  buildRequest,
  EMPTY_VALUES,
  FIELD_NAMES,
  fieldOrder,
  isFieldKey,
  MAX_DNS,
  MAX_DNS_HOSTS,
  NUMBER_LIMITS,
  requestRows,
  SELECT_OPTIONS,
  validateCreate,
  type CreateValues,
  type FieldErrors,
  type NumberField,
  type SelectField,
} from './validateCreate'

const NUMBER_META: Record<NumberField, { unit: string; step: string; placeholder: string }> = {
  cpus: { unit: 'cores', step: '1', placeholder: '2' },
  memoryGiB: { unit: 'GiB', step: '0.5', placeholder: '2' },
  diskGiB: { unit: 'GiB', step: '1', placeholder: '100' },
  rootDiskGiB: { unit: 'GiB', step: '1', placeholder: '20' },
}

/**
 * Field messages from a 422, verbatim from the server. Array elements are
 * named by their place in the request, so they are mapped back to form rows
 * (blank rows are not sent).
 */
function fieldErrorsFrom(error: unknown, values: CreateValues): FieldErrors {
  if (!(error instanceof ApiError) || error.status !== 422) return {}
  const fields = error.details.fields
  if (typeof fields !== 'object' || fields === null) return {}
  const rows = requestRows(values)
  const out: FieldErrors = {}
  for (const [key, message] of Object.entries(fields as Record<string, unknown>)) {
    if (typeof message !== 'string' || !message || !isFieldKey(key)) continue
    const dns = /^dns\[(\d+)\]$/.exec(key)
    const host = /^dnsHosts\[(\d+)\]\.(name|address)$/.exec(key)
    if (dns) {
      const row = rows.dns[Number(dns[1])]
      if (row !== undefined) out[`dns[${row}]`] = message
    } else if (host) {
      const row = rows.dnsHosts[Number(host[1])]
      if (row !== undefined) out[`dnsHosts[${row}].${host[2]}`] = message
    } else {
      out[key] = message
    }
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
  const [focusRequest, setFocusRequest] = useState<{ key: string; n: number } | null>(null)
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const [announcement, setAnnouncement] = useState('')

  const create = useMutation({ mutationFn: api.createProfile })

  useEffect(() => {
    if (!focusRequest) return
    const { key } = focusRequest
    const id = key === 'dns' || key === 'dnsHosts' ? `${formId}-${key}-add` : `${formId}-${key}`
    document.getElementById(id)?.focus()
  }, [focusRequest, formId])

  const isAdvancedKey = (key: string) => !(FIELD_NAMES as readonly string[]).includes(key)
  const firstInvalid = (errors: FieldErrors, from: CreateValues) => fieldOrder(from).find((k) => errors[k])
  /** Focuses a field, opening the Advanced section first when the field is inside it. */
  const requestFocus = (key: string) => {
    if (isAdvancedKey(key)) setAdvancedOpen(true)
    setFocusRequest((r) => ({ key, n: (r?.n ?? 0) + 1 }))
  }

  const dropErrors = (matches: (key: string) => boolean) => {
    const keep = (e: FieldErrors) => Object.fromEntries(Object.entries(e).filter(([k]) => !matches(k)))
    setClientErrors(keep)
    setServerErrors(keep)
  }

  function edit<K extends keyof CreateValues>(field: K, value: CreateValues[K]) {
    setValues((v) => ({ ...v, [field]: value }))
    // Rules that depend on this field no longer apply to the old answer.
    dropErrors(
      (k) =>
        k === field ||
        (field === 'dns' && k.startsWith('dns')) ||
        ((field === 'vmType' || field === 'arch') && k === 'vzRosetta') ||
        (field === 'networkMode' && k === 'networkInterface'),
    )
  }

  const count = (n: number, noun: string) => `${n === 0 ? 'No' : n} ${noun}${n === 1 ? '' : 's'}`

  const rows: RowHandlers = {
    onDns: (i, value) => {
      setValues((v) => ({ ...v, dns: v.dns.map((d, j) => (j === i ? value : d)) }))
      dropErrors((k) => k === `dns[${i}]` || k === 'dns')
    },
    addDns: () => {
      if (values.dns.length >= MAX_DNS) return
      const n = values.dns.length + 1
      setValues((v) => ({ ...v, dns: [...v.dns, ''] }))
      setAnnouncement(`DNS server ${n} added. ${count(n, 'DNS server')} in the list.`)
      requestFocus(`dns[${n - 1}]`)
    },
    removeDns: (i) => {
      const left = values.dns.length - 1
      setValues((v) => ({ ...v, dns: v.dns.filter((_, j) => j !== i) }))
      dropErrors((k) => k.startsWith('dns'))
      setAnnouncement(`DNS server ${i + 1} removed. ${count(left, 'DNS server')} left.`)
      requestFocus(left === 0 ? 'dns' : `dns[${Math.min(i, left - 1)}]`)
    },
    onHost: (i, field, value) => {
      setValues((v) => ({
        ...v,
        dnsHosts: v.dnsHosts.map((r, j) => (j === i ? { ...r, [field]: value } : r)),
      }))
      dropErrors((k) => k === `dnsHosts[${i}].${field}` || k === 'dnsHosts')
    },
    addHost: () => {
      if (values.dnsHosts.length >= MAX_DNS_HOSTS) return
      const n = values.dnsHosts.length + 1
      setValues((v) => ({ ...v, dnsHosts: [...v.dnsHosts, { name: '', address: '' }] }))
      setAnnouncement(`DNS host ${n} added. ${count(n, 'DNS host')} in the list.`)
      requestFocus(`dnsHosts[${n - 1}].name`)
    },
    removeHost: (i) => {
      const left = values.dnsHosts.length - 1
      setValues((v) => ({ ...v, dnsHosts: v.dnsHosts.filter((_, j) => j !== i) }))
      dropErrors((k) => k.startsWith('dnsHosts'))
      setAnnouncement(`DNS host ${i + 1} removed. ${count(left, 'DNS host')} left.`)
      requestFocus(left === 0 ? 'dnsHosts' : `dnsHosts[${Math.min(i, left - 1)}].name`)
    },
  }

  function onSubmit(e: FormEvent) {
    e.preventDefault()
    if (create.isPending) return
    setSummary('')
    const errors = validateCreate(values)
    setClientErrors(errors)
    const bad = firstInvalid(errors, values)
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
        let fields = fieldErrorsFrom(error, values)
        if (error instanceof ApiError && error.code === 'profile_exists') {
          fields = { name: describeError(error, body.name) }
        }
        setServerErrors(fields)
        setSummary(describeError(error, body.name))
        const bad = firstInvalid(fields, values)
        if (bad) requestFocus(bad)
      },
    })
  }

  const errorFor = (f: string) => serverErrors[f] ?? clientErrors[f]
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

        <AdvancedFields
          formId={formId}
          values={values}
          pending={pending}
          open={advancedOpen}
          onToggle={() => setAdvancedOpen((o) => !o)}
          errorFor={errorFor}
          edit={edit}
          rows={rows}
        />

        <div role="status" className="sr-only">
          {announcement}
        </div>

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
