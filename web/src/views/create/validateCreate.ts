import type { CreateProfileRequest } from '@/lib/api/types'

export type NumberField = 'cpus' | 'memoryGiB' | 'diskGiB' | 'rootDiskGiB'
export type SelectField = 'runtime' | 'vmType' | 'arch' | 'mountType'
export type FieldName = 'name' | NumberField | SelectField | 'kubernetesVersion'

/** Field order on screen, which is also the order focus goes to the first invalid one. */
export const FIELD_NAMES: readonly FieldName[] = [
  'name',
  'cpus',
  'memoryGiB',
  'diskGiB',
  'rootDiskGiB',
  'runtime',
  'vmType',
  'arch',
  'mountType',
  'kubernetesVersion',
]

export interface CreateValues {
  name: string
  cpus: string
  memoryGiB: string
  diskGiB: string
  rootDiskGiB: string
  runtime: string
  vmType: string
  arch: string
  mountType: string
  kubernetes: boolean
  kubernetesVersion: string
}

export type FieldErrors = Partial<Record<FieldName, string>>

export const EMPTY_VALUES: CreateValues = {
  name: '',
  cpus: '',
  memoryGiB: '',
  diskGiB: '',
  rootDiskGiB: '',
  runtime: '',
  vmType: '',
  arch: '',
  mountType: '',
  kubernetes: false,
  kubernetesVersion: '',
}

export const NUMBER_LIMITS: Record<NumberField, { label: string; integer: boolean; max: number }> = {
  cpus: { label: 'CPUs', integer: true, max: Infinity },
  memoryGiB: { label: 'Memory', integer: false, max: 256 },
  diskGiB: { label: 'Disk', integer: true, max: 8192 },
  rootDiskGiB: { label: 'Root disk', integer: true, max: 8192 },
}

export const SELECT_OPTIONS: Record<SelectField, { label: string; options: { value: string; label: string }[] }> = {
  runtime: {
    label: 'Runtime',
    options: [
      { value: 'docker', label: 'Docker' },
      { value: 'containerd', label: 'containerd' },
      { value: 'incus', label: 'Incus' },
    ],
  },
  vmType: {
    label: 'VM type',
    options: [
      { value: 'vz', label: 'VZ' },
      { value: 'qemu', label: 'QEMU' },
      { value: 'krunkit', label: 'Krunkit' },
    ],
  },
  arch: {
    label: 'Architecture',
    options: [
      { value: 'aarch64', label: 'aarch64' },
      { value: 'x86_64', label: 'x86_64' },
    ],
  },
  mountType: {
    label: 'Mount type',
    options: [
      { value: 'virtiofs', label: 'virtiofs' },
      { value: 'sshfs', label: 'sshfs' },
      { value: '9p', label: '9p' },
    ],
  },
}

const NAME_RE = /^[A-Za-z0-9][A-Za-z0-9._-]*$/
const VERSION_RE = /^v?[0-9]+\.[0-9]+(\.[0-9]+)?(-[0-9A-Za-z.]+)?(\+k3s[0-9]+)?$/

function parse(raw: string): number {
  const s = raw.trim()
  return s === '' ? NaN : Number(s)
}

/** Colima maps `colima` and `colima-*` onto other profiles, so the server refuses them. */
export function isReservedName(name: string): boolean {
  const n = name.toLowerCase()
  return n === 'colima' || n.startsWith('colima-')
}

/**
 * Client-side checks for the obvious rules. The server stays the validator
 * (it also knows the host core count); its messages override these.
 */
export function validateCreate(values: CreateValues): FieldErrors {
  const errors: FieldErrors = {}

  const name = values.name.trim()
  if (name === '') errors.name = 'Name is required.'
  else if (name.length > 64 || !NAME_RE.test(name) || name.includes('..')) {
    errors.name =
      'Name must start with a letter or digit, then use letters, digits, dots, underscores or hyphens (at most 64 characters, no "..").'
  } else if (isReservedName(name)) {
    errors.name = 'Names "colima" and "colima-..." are reserved by Colima. Pick another name.'
  }

  for (const f of Object.keys(NUMBER_LIMITS) as NumberField[]) {
    if (values[f].trim() === '') continue
    const { label, integer, max } = NUMBER_LIMITS[f]
    const n = parse(values[f])
    if (!Number.isFinite(n) || n < 1 || (integer && !Number.isInteger(n))) {
      errors[f] = integer
        ? `${label} must be a whole number, at least 1.`
        : `${label} must be a number, at least 1.`
    } else if (n > max) {
      errors[f] = `${label} must be at most ${max}.`
    }
  }

  if (values.kubernetes && values.kubernetesVersion.trim() !== '') {
    const v = values.kubernetesVersion.trim()
    if (v.length > 64 || !VERSION_RE.test(v)) errors.kubernetesVersion = 'Version must look like v1.31.2+k3s1.'
  }
  return errors
}

/** The POST body: only what the user filled in. */
export function buildRequest(values: CreateValues): CreateProfileRequest {
  const body: CreateProfileRequest = { name: values.name.trim() }
  for (const f of Object.keys(NUMBER_LIMITS) as NumberField[]) {
    if (values[f].trim() !== '') body[f] = parse(values[f])
  }
  for (const f of Object.keys(SELECT_OPTIONS) as SelectField[]) {
    if (values[f] !== '') body[f] = values[f]
  }
  if (values.kubernetes) {
    body.kubernetes = true
    if (values.kubernetesVersion.trim() !== '') body.kubernetesVersion = values.kubernetesVersion.trim()
  }
  return body
}
