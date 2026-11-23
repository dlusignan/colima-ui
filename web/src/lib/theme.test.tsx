import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ThemeSwitcher } from '@/components/ThemeSwitcher'
import { STORAGE_KEY } from '@/lib/theme'
import { fireMediaChange, setPrefersDark } from '@/test/setup'

const html = document.documentElement

describe('theme switcher', () => {
  it('follows prefers-color-scheme when nothing is stored', () => {
    setPrefersDark(false)
    render(<ThemeSwitcher />)
    expect(html.dataset.theme).toBe('light')
    expect(html.classList.contains('dark')).toBe(false)
    expect(html.style.colorScheme).toBe('light')
  })

  it('defaults to mocha when the OS prefers dark', () => {
    render(<ThemeSwitcher />)
    expect(html.dataset.theme).toBe('mocha')
    expect(html.classList.contains('dark')).toBe(true)
    expect(html.style.colorScheme).toBe('dark')
  })

  it('a stored choice wins over the OS', () => {
    localStorage.setItem(STORAGE_KEY, 'light')
    render(<ThemeSwitcher />)
    expect(html.dataset.theme).toBe('light')
  })

  it('applies OS changes live only while nothing is stored', () => {
    render(<ThemeSwitcher />)
    fireMediaChange(false)
    expect(html.dataset.theme).toBe('light')
  })

  it('ignores OS changes once a choice is stored', () => {
    localStorage.setItem(STORAGE_KEY, 'mocha')
    render(<ThemeSwitcher />)
    fireMediaChange(false)
    expect(html.dataset.theme).toBe('mocha')
  })

  it('writes the key on choice and clears it for System', async () => {
    const user = userEvent.setup()
    render(<ThemeSwitcher />)
    const select = screen.getByLabelText(/theme/i)
    await user.selectOptions(select, 'light')
    expect(localStorage.getItem(STORAGE_KEY)).toBe('light')
    expect(html.dataset.theme).toBe('light')
    expect(html.classList.contains('dark')).toBe(false)
    await user.selectOptions(select, 'system')
    expect(localStorage.getItem(STORAGE_KEY)).toBeNull()
    expect(html.dataset.theme).toBe('mocha')
  })

  it('falls back to system when storage throws', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('denied')
    })
    setPrefersDark(false)
    render(<ThemeSwitcher />)
    expect(html.dataset.theme).toBe('light')
    await userEvent.setup().selectOptions(screen.getByLabelText(/theme/i), 'mocha')
    expect(html.dataset.theme).toBe('mocha')
  })
})
