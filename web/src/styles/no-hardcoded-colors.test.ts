import { readdirSync, readFileSync, statSync } from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'

const src = path.resolve(import.meta.dirname, '..')
const palette =
  'slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose'
const rules: [string, RegExp][] = [
  ['hex literal', /#[0-9a-fA-F]{3,8}\b/],
  ['color function literal', /\b(rgba?|hsla?|oklch|oklab|lab|lch|hwb)\(/],
  [
    'tailwind palette class',
    new RegExp(
      `\\b(bg|text|border|ring|fill|stroke|from|via|to|divide|outline|decoration|shadow|accent|caret)-(${palette})-\\d+`,
    ),
  ],
  ['white/black class', /\b(bg|text|border|ring|fill|stroke)-(white|black)\b/],
]

function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = path.join(dir, name)
    if (statSync(p).isDirectory()) {
      return p === path.join(src, 'styles', 'themes') ? [] : walk(p)
    }
    return /\.(ts|tsx|css)$/.test(name) && !/\.test\./.test(name) ? [p] : []
  })
}

describe('no hard-coded colors outside themes', () => {
  const files = walk(src)
  it('scans files', () => expect(files.length).toBeGreaterThan(3))
  for (const [label, re] of rules) {
    it(`has no ${label}`, () => {
      const hits = files.filter((f) => re.test(readFileSync(f, 'utf8')))
      expect(hits.map((f) => path.relative(src, f))).toEqual([])
    })
  }
})
