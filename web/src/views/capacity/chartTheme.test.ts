import { afterEach, describe, expect, it } from 'vitest'
import { buildChartTheme } from './chartTheme'

afterEach(() => document.documentElement.removeAttribute('style'))

describe('buildChartTheme', () => {
  it('reads the chart tokens from the root element', () => {
    const s = document.documentElement.style
    s.setProperty('--chart-1', 'c1')
    s.setProperty('--chart-2', 'c2')
    s.setProperty('--chart-3', 'c3')
    s.setProperty('--warning', 'warn')
    s.setProperty('--muted-foreground', 'muted')
    s.setProperty('--border', 'line')
    expect(buildChartTheme()).toEqual({
      series: ['c1', 'c2', 'c3'],
      allocated: 'warn',
      axis: 'muted',
      grid: 'line',
    })
  })

  it('follows a theme change on the next call', () => {
    document.documentElement.style.setProperty('--chart-1', 'before')
    expect(buildChartTheme().series[0]).toBe('before')
    document.documentElement.style.setProperty('--chart-1', 'after')
    expect(buildChartTheme().series[0]).toBe('after')
  })

  it('falls back to a named color when a token is missing', () => {
    expect(buildChartTheme().grid).toBe('gray')
  })
})
