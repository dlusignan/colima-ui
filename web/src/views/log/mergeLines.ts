import type { JobLine } from '@/lib/events'

/** Merges two line lists into one ordered by seq, one entry per seq. */
export function mergeLines(
  a: readonly JobLine[],
  b: readonly JobLine[],
): readonly JobLine[] {
  const bySeq = new Map<number, JobLine>()
  for (const l of a) bySeq.set(l.seq, l)
  for (const l of b) if (!bySeq.has(l.seq)) bySeq.set(l.seq, l)
  return [...bySeq.values()].sort((x, y) => x.seq - y.seq)
}
