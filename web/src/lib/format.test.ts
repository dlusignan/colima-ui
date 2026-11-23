import { describe, expect, it } from 'vitest'
import { formatBytes, formatCount, formatPercent } from './format'

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
