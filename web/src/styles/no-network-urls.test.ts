import { readdirSync, readFileSync, statSync } from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'

const root = path.resolve(import.meta.dirname, '../..')

function css(dir: string): string[] {
  return readdirSync(dir).flatMap((n) => {
    const p = path.join(dir, n)
    return statSync(p).isDirectory() ? css(p) : n.endsWith('.css') ? [p] : []
  })
}

describe('fonts and assets stay local', () => {
  it('has no http(s):// in source CSS or index.html', () => {
    const files = [...css(path.join(root, 'src')), path.join(root, 'index.html')]
    const hits = files.filter((f) => /https?:\/\//i.test(readFileSync(f, 'utf8')))
    expect(hits).toEqual([])
  })

  it('imports fonts from local packages', () => {
    const index = readFileSync(path.join(root, 'src/index.css'), 'utf8')
    expect(index).toContain('@fontsource-variable/geist"')
    expect(index).toContain('@fontsource-variable/geist-mono')
  })

  it('loads theme-init.js as a classic script in head', () => {
    const html = readFileSync(path.join(root, 'index.html'), 'utf8')
    const head = html.slice(html.indexOf('<head>'), html.indexOf('</head>'))
    expect(head).toMatch(/<script src="\/theme-init\.js"><\/script>/)
  })
})
