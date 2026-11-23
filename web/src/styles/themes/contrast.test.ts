import { readdirSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { themes } from '@/lib/theme'
import { describe, expect, it } from 'vitest'

const dir = import.meta.dirname

function parseTokens(css: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const m of css.matchAll(/(--[\w-]+)\s*:\s*(#[0-9a-fA-F]{6})\s*;/g)) {
    out[m[1]] = m[2].toLowerCase()
  }
  return out
}

function channel(v: number): number {
  const c = v / 255
  return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
}

function luminance(hex: string): number {
  const n = parseInt(hex.slice(1), 16)
  return (
    0.2126 * channel((n >> 16) & 255) +
    0.7152 * channel((n >> 8) & 255) +
    0.0722 * channel(n & 255)
  )
}

export function ratio(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

// [foreground token, background token, minimum ratio]
const TEXT = 4.5
const UI = 3
const status = ['success', 'warning', 'info', 'destructive']
const pairs: [string, string, number][] = [
  ['--foreground', '--background', TEXT],
  ['--card-foreground', '--card', TEXT],
  ['--popover-foreground', '--popover', TEXT],
  ['--foreground', '--card', TEXT],
  ['--foreground', '--popover', TEXT],
  ['--muted-foreground', '--background', TEXT],
  ['--muted-foreground', '--card', TEXT],
  ['--muted-foreground', '--muted', TEXT],
  ['--primary-foreground', '--primary', TEXT],
  ['--secondary-foreground', '--secondary', TEXT],
  ['--accent-foreground', '--accent', TEXT],
  ['--primary', '--background', TEXT],
  ['--sidebar-foreground', '--sidebar', TEXT],
  ['--sidebar-primary-foreground', '--sidebar-primary', TEXT],
  ['--sidebar-accent-foreground', '--sidebar-accent', TEXT],
  ...status.flatMap((s): [string, string, number][] => [
    [`--${s}`, '--background', TEXT],
    [`--${s}`, '--card', TEXT],
    [`--${s}-foreground`, `--${s}`, TEXT],
  ]),
  // UI component boundaries and focus rings need 3:1. --border is a
  // decorative divider and is deliberately exempt (WCAG 1.4.11).
  ['--ring', '--background', UI],
  ['--input', '--background', UI],
  ...[1, 2, 3, 4, 5].map((i): [string, string, number] => [
    `--chart-${i}`,
    '--card',
    UI,
  ]),
]

const files = readdirSync(dir).filter((f) => f.endsWith('.css') && f !== 'index.css')

describe('theme contrast', () => {
  it('has one css file per registered theme', () => {
    expect([...files].sort()).toEqual(themes.map((t) => `${t.id}.css`).sort())
  })

  for (const file of files) {
    describe(file, () => {
      const tokens = parseTokens(readFileSync(path.join(dir, file), 'utf8'))
      for (const [fg, bg, min] of pairs) {
        it(`${fg} on ${bg} >= ${min}`, () => {
          expect(tokens[fg], `${fg} defined`).toBeDefined()
          expect(tokens[bg], `${bg} defined`).toBeDefined()
          expect(ratio(tokens[fg], tokens[bg])).toBeGreaterThanOrEqual(min)
        })
      }
    })
  }
})
