import { readFileSync } from 'node:fs'
import path from 'node:path'
import { describe, expect, it, vi } from 'vitest'
import { STORAGE_KEY } from '@/lib/theme'
import { setPrefersDark } from '@/test/setup'

const script = readFileSync(
  path.resolve(import.meta.dirname, '../../public/theme-init.js'),
  'utf8',
)
const run = () => new Function(script)()
const html = document.documentElement

describe('theme-init.js', () => {
  it('uses the stored choice', () => {
    localStorage.setItem(STORAGE_KEY, 'light')
    run()
    expect(html.dataset.theme).toBe('light')
    expect(html.classList.contains('dark')).toBe(false)
    expect(html.style.colorScheme).toBe('light')
  })

  it('follows the OS when nothing is stored', () => {
    setPrefersDark(false)
    run()
    expect(html.dataset.theme).toBe('light')
  })

  it('falls back to mocha when storage throws and OS is dark', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied')
    })
    run()
    expect(html.dataset.theme).toBe('mocha')
    expect(html.classList.contains('dark')).toBe(true)
  })
})
