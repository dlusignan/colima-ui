const KIB = 1024
const UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB'] as const

const oneDecimal = new Intl.NumberFormat('en-US', { maximumFractionDigits: 1 })
const wholeNumber = new Intl.NumberFormat('en-US', { maximumFractionDigits: 0 })

/** Binary units, one decimal below 10 and none above: `8 GiB`, `7.5 GiB`, `100 GiB`. */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  let value = bytes
  let unit = 0
  while (value >= KIB && unit < UNITS.length - 1) {
    value /= KIB
    unit++
  }
  // Rounding can carry into the next unit (1023.96 KiB), so round first.
  const rounded = Number(value.toFixed(value < 10 ? 1 : 0))
  if (rounded >= KIB && unit < UNITS.length - 1) {
    return `1 ${UNITS[unit + 1]}`
  }
  const fmt = rounded < 10 ? oneDecimal : wholeNumber
  return `${fmt.format(rounded)} ${UNITS[unit]}`
}

/** One decimal, trailing `.0` kept so columns line up: `12.5%`, `3.0%`. */
export function formatPercent(value: number): string {
  if (!Number.isFinite(value)) return '0.0%'
  return `${new Intl.NumberFormat('en-US', {
    minimumFractionDigits: 1,
    maximumFractionDigits: 1,
  }).format(value)}%`
}

/** A whole number with thousands separators. */
export function formatCount(value: number): string {
  if (!Number.isFinite(value)) return '0'
  return wholeNumber.format(Math.round(value))
}
