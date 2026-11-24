import type { CreateProfileRequest, DnsHost } from '@/lib/api/types'

export type NumberField = 'cpus' | 'memoryGiB' | 'diskGiB' | 'rootDiskGiB'
export type SelectField = 'runtime' | 'vmType' | 'arch' | 'mountType'
export type AdvancedToggle = 'sshAgent' | 'vzRosetta' | 'mountInotify' | 'networkAddress'
export type AdvancedSelect = 'portForwarder' | 'networkMode'
export type AdvancedText = 'hostname' | 'networkInterface' | 'cpuType'
/** Fields with a fixed id. DNS rows have their own keys: `dns[0]`, `dnsHosts[1].name`. */
export type FieldName =
  | 'name'
  | NumberField
  | SelectField
  | 'kubernetesVersion'
  | AdvancedText
  | AdvancedSelect
  | 'vzRosetta'
  | 'dns'
  | 'dnsHosts'

export const MAX_DNS = 8
export const MAX_DNS_HOSTS = 16

export interface DnsHostRow {
  name: string
  address: string
}

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

/** Advanced fields in screen order (the DNS rows go between `dns` and `dnsHosts`). */
const ADVANCED_ORDER = [
  'hostname',
  'portForwarder',
  'dns',
  'dnsHosts',
  'networkMode',
  'networkInterface',
  'vzRosetta',
  'cpuType',
] as const

/** Every error key in screen order, rows included, so focus goes to the first invalid field. */
export function fieldOrder(values: CreateValues): string[] {
  const out: string[] = [...FIELD_NAMES, 'hostname', 'portForwarder', 'dns']
  values.dns.forEach((_, i) => out.push(`dns[${i}]`))
  out.push('dnsHosts')
  values.dnsHosts.forEach((_, i) => out.push(`dnsHosts[${i}].name`, `dnsHosts[${i}].address`))
  out.push(...ADVANCED_ORDER.slice(4))
  return out
}

/** Whether an error key is one the form can show (a fixed field or a DNS row). */
export function isFieldKey(key: string): boolean {
  return (
    (FIELD_NAMES as readonly string[]).includes(key) ||
    (ADVANCED_ORDER as readonly string[]).includes(key) ||
    /^dns\[\d+\]$/.test(key) ||
    /^dnsHosts\[\d+\]\.(name|address)$/.test(key)
  )
}

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
  hostname: string
  dns: string[]
  dnsHosts: DnsHostRow[]
  sshAgent: boolean
  vzRosetta: boolean
  portForwarder: string
  mountInotify: boolean
  networkAddress: boolean
  networkMode: string
  networkInterface: string
  cpuType: string
}

export type FieldErrors = Partial<Record<string, string>>

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
  hostname: '',
  dns: [],
  dnsHosts: [],
  sshAgent: false,
  vzRosetta: false,
  portForwarder: '',
  mountInotify: false,
  networkAddress: false,
  networkMode: '',
  networkInterface: '',
  cpuType: '',
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

export const ADVANCED_SELECTS: Record<AdvancedSelect, { label: string; options: { value: string; label: string }[] }> = {
  portForwarder: {
    label: 'Port forwarder',
    options: [
      { value: 'ssh', label: 'ssh' },
      { value: 'grpc', label: 'grpc' },
      { value: 'none', label: 'none' },
    ],
  },
  networkMode: {
    label: 'Network mode',
    options: [
      { value: 'shared', label: 'shared' },
      { value: 'bridged', label: 'bridged' },
    ],
  },
}

const NAME_RE = /^[A-Za-z0-9][A-Za-z0-9._-]*$/
const VERSION_RE = /^v?[0-9]+\.[0-9]+(\.[0-9]+)?(-[0-9A-Za-z.]+)?(\+k3s[0-9]+)?$/

const LABEL_RE = /^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/
const INTERFACE_RE = /^[A-Za-z0-9][A-Za-z0-9._-]{0,14}$/
const CPU_TYPE_RE = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/
const OCTET = '(25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)'
const IPV4_RE = new RegExp(`^${OCTET}(\\.${OCTET}){3}$`)
const HEX_GROUP_RE = /^[0-9A-Fa-f]{1,4}$/

export function isIPv4(s: string): boolean {
  return IPV4_RE.test(s)
}

export function isIPv6(input: string): boolean {
  if (!/^[0-9A-Fa-f:.]+$/.test(input) || !input.includes(':')) return false
  let s = input
  const cut = s.lastIndexOf(':') + 1
  if (s.slice(cut).includes('.')) {
    if (!isIPv4(s.slice(cut))) return false
    s = `${s.slice(0, cut)}0:0` // an embedded IPv4 address is two groups
  }
  const groups = (p: string) => (p === '' ? [] : p.split(':'))
  const parts = s.split('::')
  if (parts.length > 2) return false
  if (parts.length === 1) {
    const g = groups(s)
    return g.length === 8 && g.every((x) => HEX_GROUP_RE.test(x))
  }
  const all = [...groups(parts[0]), ...groups(parts[1])]
  return all.length <= 7 && all.every((x) => HEX_GROUP_RE.test(x))
}

/** One IPv4 or IPv6 address: no zone, port or prefix, as the backend accepts. */
export function isAddress(s: string): boolean {
  return isIPv4(s) || isIPv6(s)
}

/** Dot-separated DNS labels, at most 253 characters, no trailing dot. */
export function isHostName(s: string): boolean {
  return s !== '' && s.length <= 253 && s.split('.').every((l) => LABEL_RE.test(l))
}

/** Why Rosetta cannot be used with the chosen VM type or architecture, or '' when nothing rules it out. */
export function rosettaBlocked(values: Pick<CreateValues, 'vmType' | 'arch'>): string {
  if (values.vmType !== '' && values.vmType !== 'vz') return 'Rosetta needs the VZ VM type. Set VM type to VZ (or Default) to use it.'
  if (values.arch === 'x86_64') return 'Rosetta needs the aarch64 architecture. Set Architecture to aarch64 to use it.'
  return ''
}

/** Why CPU type is unavailable, or ''. */
export function cpuTypeBlocked(values: Pick<CreateValues, 'vmType'>): string {
  return values.vmType === 'qemu' ? '' : 'CPU type applies to the QEMU VM type only. Set VM type to QEMU to use it.'
}

export function interfaceVisible(values: Pick<CreateValues, 'networkMode'>): boolean {
  return values.networkMode === 'bridged'
}

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

  if (values.hostname.trim() !== '' && !LABEL_RE.test(values.hostname.trim())) {
    errors.hostname =
      'Hostname must be one DNS label: letters, digits and hyphens, starting and ending with a letter or digit, at most 63 characters.'
  }
  values.dns.forEach((raw, i) => {
    const d = raw.trim()
    if (d !== '' && !isAddress(d)) errors[`dns[${i}]`] = 'Enter an IPv4 or IPv6 address, with no port, prefix or zone.'
  })
  values.dnsHosts.forEach((row, i) => {
    const n = row.name.trim()
    const a = row.address.trim()
    if (n === '' && a === '') return
    if (n === '') errors[`dnsHosts[${i}].name`] = 'Enter a host name.'
    else if (!isHostName(n)) {
      errors[`dnsHosts[${i}].name`] =
        'Host name must be dot-separated labels of letters, digits and hyphens, at most 253 characters, no trailing dot.'
    }
    if (a === '') errors[`dnsHosts[${i}].address`] = 'Enter an IPv4 or IPv6 address.'
    else if (!isAddress(a)) errors[`dnsHosts[${i}].address`] = 'Enter an IPv4 or IPv6 address, with no port, prefix or zone.'
  })
  if (interfaceVisible(values) && values.networkInterface.trim() !== '' && !INTERFACE_RE.test(values.networkInterface.trim())) {
    errors.networkInterface =
      'Interface must be letters, digits, dots, underscores or hyphens, starting with a letter or digit, at most 15 characters.'
  }
  if (!cpuTypeBlocked(values) && values.cpuType.trim() !== '' && !CPU_TYPE_RE.test(values.cpuType.trim())) {
    errors.cpuType =
      'CPU type must be one model name such as max or cortex-a72 (no feature list), at most 64 characters.'
  }
  return errors
}

/**
 * Which row of the form each element of the request arrays came from. Blank
 * rows are not sent, so a server error on `dns[1]` can belong to row 2 or 3.
 */
export function requestRows(values: CreateValues): { dns: number[]; dnsHosts: number[] } {
  const keep = <T,>(rows: T[], used: (r: T) => boolean) =>
    rows.flatMap((r, i) => (used(r) ? [i] : []))
  return {
    dns: keep(values.dns, (d) => d.trim() !== ''),
    dnsHosts: keep(values.dnsHosts, (r) => r.name.trim() !== '' || r.address.trim() !== ''),
  }
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

  if (values.hostname.trim() !== '') body.hostname = values.hostname.trim()
  const dns = values.dns.map((d) => d.trim()).filter((d) => d !== '')
  if (dns.length > 0) body.dns = dns
  const dnsHosts: DnsHost[] = values.dnsHosts
    .map((r) => ({ name: r.name.trim(), address: r.address.trim() }))
    .filter((r) => r.name !== '' || r.address !== '')
  if (dnsHosts.length > 0) body.dnsHosts = dnsHosts
  if (values.sshAgent) body.sshAgent = true
  if (values.vzRosetta && !rosettaBlocked(values)) body.vzRosetta = true
  if (values.portForwarder !== '') body.portForwarder = values.portForwarder as CreateProfileRequest['portForwarder']
  if (values.mountInotify) body.mountInotify = true
  if (values.networkAddress) body.networkAddress = true
  if (values.networkMode !== '') body.networkMode = values.networkMode as CreateProfileRequest['networkMode']
  if (interfaceVisible(values) && values.networkInterface.trim() !== '') {
    body.networkInterface = values.networkInterface.trim()
  }
  if (!cpuTypeBlocked(values) && values.cpuType.trim() !== '') body.cpuType = values.cpuType.trim()
  return body
}
