import { describe, expect, it } from 'vitest'
import { formatBytes, formatCount, formatPercent, formatRelativeTime } from './format'

const GiB = 1024 ** 3

describe('formatBytes', () => {
  it.each([
    [0, '0 B'],
    [-5, '0 B'],
    [NaN, '0 B'],
    [1, '1 B'],
    [1023, '1,023 B'],
    [1024, '1 KiB'],
    [1536, '1.5 KiB'],
    [10 * 1024, '10 KiB'],
    [1024 ** 2, '1 MiB'],
    [8 * GiB, '8 GiB'],
    [7.5 * GiB, '7.5 GiB'],
    [100 * GiB, '100 GiB'],
    [64424509440, '60 GiB'],
    [1024 ** 4, '1 TiB'],
    [2048 * 1024 ** 4, '2,048 TiB'],
    [1024 * 1024 - 1, '1 MiB'],
  ])('formats %s as %s', (input, expected) => {
    expect(formatBytes(input)).toBe(expected)
  })
})

describe('formatPercent', () => {
  it.each([
    [0, '0.0%'],
    [3, '3.0%'],
    [12.345, '12.3%'],
    [100, '100.0%'],
    [1234.56, '1,234.6%'],
    [Infinity, '0.0%'],
  ])('formats %s as %s', (input, expected) => {
    expect(formatPercent(input)).toBe(expected)
  })
})

describe('formatCount', () => {
  it.each([
    [0, '0'],
    [999, '999'],
    [1234567, '1,234,567'],
    [2.6, '3'],
    [NaN, '0'],
  ])('formats %s as %s', (input, expected) => {
    expect(formatCount(input)).toBe(expected)
  })
})

describe('formatRelativeTime', () => {
  const now = Date.parse('2026-06-10T12:00:00Z')
  const ago = (seconds: number) => new Date(now - seconds * 1000).toISOString()
  it.each([
    [10, 'just now'],
    [60, '1 minute ago'],
    [5 * 60, '5 minutes ago'],
    [3600, '1 hour ago'],
    [2 * 3600, '2 hours ago'],
    [86400, '1 day ago'],
    [3 * 86400, '3 days ago'],
    [45 * 86400, '1 month ago'],
    [90 * 86400, '3 months ago'],
    [400 * 86400, '1 year ago'],
    [800 * 86400, '2 years ago'],
  ])('%i seconds ago is %s', (seconds, want) => {
    expect(formatRelativeTime(ago(seconds), now)).toBe(want)
  })
  it('never renders a future timestamp as "in X"', () => {
    expect(formatRelativeTime(ago(-3 * 86400), now)).toBe('just now')
  })
  it('handles unparseable input', () => {
    expect(formatRelativeTime('nope', now)).toBe('unknown')
  })
})
