export const MAX_TABLE_ROWS = 30

/** At most `max` evenly spaced items, always including the first and the last. */
export function thinRows<T>(items: readonly T[], max = MAX_TABLE_ROWS): T[] {
  if (items.length <= max) return [...items]
  const out: T[] = []
  const step = (items.length - 1) / (max - 1)
  for (let i = 0; i < max; i++) out.push(items[Math.round(i * step)])
  return out
}
