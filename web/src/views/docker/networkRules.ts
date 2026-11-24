import type { CreateNetworkRequest } from '@/lib/api/types'

/** Limits of docs/api.md 1.3 and 3.22, checked in the browser before anything is sent. */
export const MAX_NETWORK_NAME = 128
export const MAX_SUBNET_LENGTH = 64

/** Networks Docker owns. The server refuses to create or remove them, in any letter case. */
const BUILT_IN = ['bridge', 'host', 'none', 'ingress']
const NAME_PATTERN = /^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/

export const isBuiltInNetwork = (name: string) => BUILT_IN.includes(name.toLowerCase())

/** The hint under the subnet field: the server policy of docs/api.md 3.22 in plain words. */
export const SUBNET_HINT =
  'Optional, one range in CIDR form. IPv4 prefix /8 to /28, IPv6 prefix /48 to /120. Not allowed: loopback, ' +
  '0.0.0.0/8 and ::/8, link-local, multicast, 240.0.0.0/4, IPv4-mapped IPv6 and 192.168.5.0/24 (used by the Colima VM). ' +
  'The server clears host bits, so 172.28.0.5/16 becomes 172.28.0.0/16. Leave it empty to let Docker choose.'

export function validateNetworkName(name: string): string | undefined {
  if (name === '') return 'Enter a name.'
  if (name.length > MAX_NETWORK_NAME) return `Use at most ${MAX_NETWORK_NAME} characters.`
  if (!NAME_PATTERN.test(name)) {
    return 'Use letters, digits, underscore, dot and hyphen, starting with a letter or digit.'
  }
  if (isBuiltInNetwork(name)) return 'This name belongs to a network built into Docker. Choose another name.'
  return undefined
}

interface Range {
  /** Inclusive bounds. */
  start: bigint
  end: bigint
}

interface Refused extends Range {
  label: string
}

function prefixRange(address: bigint, bits: number, width: number): Range {
  const host = BigInt(width - bits)
  const mask = (1n << host) - 1n
  const start = address & ~mask
  return { start, end: start | mask }
}

function parseV4(text: string): bigint | null {
  const parts = text.split('.')
  if (parts.length !== 4) return null
  let value = 0n
  for (const p of parts) {
    // Decimal only, no leading zeros, like Go's netip.
    if (!/^(0|[1-9][0-9]{0,2})$/.test(p)) return null
    const n = Number(p)
    if (n > 255) return null
    value = (value << 8n) | BigInt(n)
  }
  return value
}

function parseV6(text: string): bigint | null {
  if (text.includes('%') || !text.includes(':')) return null
  let tail: bigint[] = []
  let body = text
  // An embedded IPv4 address in the last 32 bits.
  const lastColon = text.lastIndexOf(':')
  if (text.slice(lastColon + 1).includes('.')) {
    const v4 = parseV4(text.slice(lastColon + 1))
    if (v4 === null) return null
    tail = [v4 >> 16n, v4 & 0xffffn]
    body = text.slice(0, lastColon + 1) + '0:0'
  }
  const halves = body.split('::')
  if (halves.length > 2) return null
  const groups = (s: string): string[] | null => (s === '' ? [] : s.split(':'))
  const head = groups(halves[0])
  const rest = halves.length === 2 ? groups(halves[1]) : []
  if (!head || !rest) return null
  const all = [...head, ...rest]
  if (!all.every((g) => /^[0-9a-fA-F]{1,4}$/.test(g))) return null
  const want = 8
  let words: string[]
  if (halves.length === 2) {
    if (all.length > want - 1) return null
    words = [...head, ...Array(want - all.length).fill('0'), ...rest]
  } else {
    if (all.length !== want) return null
    words = all
  }
  const values = words.map((w) => BigInt(parseInt(w, 16)))
  if (tail.length === 2) {
    values[6] = tail[0]
    values[7] = tail[1]
  }
  return values.reduce((acc, v) => (acc << 16n) | v, 0n)
}

const v4 = (text: string, bits: number, label: string): Refused => ({
  ...prefixRange(parseV4(text) as bigint, bits, 32),
  label,
})
const v6 = (text: string, bits: number, label: string): Refused => ({
  ...prefixRange(parseV6(text) as bigint, bits, 128),
  label,
})

// docs/api.md 3.22, rule 3. Overlap counts, so a short prefix cannot contain a refused range.
const REFUSED_V4: Refused[] = [
  v4('127.0.0.0', 8, 'loopback'),
  v4('0.0.0.0', 8, 'the unspecified range'),
  v4('169.254.0.0', 16, 'link-local'),
  v4('224.0.0.0', 4, 'multicast'),
  v4('240.0.0.0', 4, 'reserved'),
  v4('192.168.5.0', 24, 'the network the Colima VM uses for its own gateway and DNS'),
]
const REFUSED_V6: Refused[] = [
  v6('::1', 128, 'loopback'),
  v6('::', 8, 'the unspecified range'),
  v6('fe80::', 10, 'link-local'),
  v6('ff00::', 8, 'multicast'),
  v6('::ffff:0:0', 96, 'IPv4-mapped'),
]

const BAD_FORM = 'Enter one range in CIDR form, for example 172.28.0.0/16.'

/** Validates the optional subnet text. An empty text is valid (Docker chooses). Mirrors docs/api.md 3.22. */
export function validateSubnet(raw: string): string | undefined {
  const text = raw.trim()
  if (text === '') return undefined
  if (text.length > MAX_SUBNET_LENGTH) return `Use at most ${MAX_SUBNET_LENGTH} characters.`
  if (!/^[\x21-\x7e]+$/.test(text)) return 'Use plain characters only, with no spaces.'
  const slash = text.indexOf('/')
  if (slash < 0 || text.indexOf('/', slash + 1) >= 0) return BAD_FORM
  const addr = text.slice(0, slash)
  const bitsText = text.slice(slash + 1)
  if (!/^(0|[1-9][0-9]{0,3})$/.test(bitsText)) return BAD_FORM
  const bits = Number(bitsText)
  const isV6 = addr.includes(':')
  const value = isV6 ? parseV6(addr) : parseV4(addr)
  if (value === null) return BAD_FORM
  if (isV6) {
    if (value >> 32n === 0xffffn) return 'IPv4-mapped IPv6 addresses are not allowed.'
    if (bits < 48 || bits > 120) return 'An IPv6 prefix must be between /48 and /120.'
  } else if (bits < 8 || bits > 28) {
    return 'An IPv4 prefix must be between /8 and /28.'
  }
  const range = prefixRange(value, bits, isV6 ? 128 : 32)
  const hit = (isV6 ? REFUSED_V6 : REFUSED_V4).find((r) => range.start <= r.end && r.start <= range.end)
  if (hit) return `This range overlaps ${hit.label}, which is not allowed.`
  return undefined
}

export interface NetworkFormErrors {
  name?: string
  subnet?: string
}

export function validateNetworkForm(name: string, subnet: string): NetworkFormErrors {
  const errors: NetworkFormErrors = {}
  const nameError = validateNetworkName(name)
  if (nameError) errors.name = nameError
  const subnetError = validateSubnet(subnet)
  if (subnetError) errors.subnet = subnetError
  return errors
}

export const hasErrors = (e: NetworkFormErrors) => Boolean(e.name) || Boolean(e.subnet)

/** `{ name, internal }`, plus `subnet` (as typed, trimmed) when one is given. The server masks host bits. */
export function buildCreateRequest(name: string, internal: boolean, subnet: string): CreateNetworkRequest {
  const text = subnet.trim()
  return text === '' ? { name, internal } : { name, internal, subnet: text }
}
