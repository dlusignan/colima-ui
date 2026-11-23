import { useCallback, useEffect, useState } from 'react'

export type ThemeDef = { id: string; label: string; colorScheme: 'dark' | 'light' }

// Add a palette: one CSS file in styles/themes, one @import in its index.css,
// one line here, and the id in public/theme-init.js.
export const themes: ThemeDef[] = [
  { id: 'mocha', label: 'Mocha (dark)', colorScheme: 'dark' },
  { id: 'light', label: 'Light', colorScheme: 'light' },
]

export const STORAGE_KEY = 'colima-ui-theme'
export const DARK_QUERY = '(prefers-color-scheme: dark)'
const DARK_DEFAULT = 'mocha'
const LIGHT_DEFAULT = 'light'

export function readStoredTheme(): string | null {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    return v && themes.some((t) => t.id === v) ? v : null
  } catch {
    return null
  }
}

function writeStoredTheme(id: string | null) {
  try {
    if (id === null) localStorage.removeItem(STORAGE_KEY)
    else localStorage.setItem(STORAGE_KEY, id)
  } catch {
    // storage blocked: choice applies for this page only
  }
}

function systemTheme(): string {
  const mq = typeof window.matchMedia === 'function' ? window.matchMedia(DARK_QUERY) : null
  return mq && !mq.matches ? LIGHT_DEFAULT : DARK_DEFAULT
}

export function applyTheme(id: string) {
  const def = themes.find((t) => t.id === id) ?? themes[0]
  const root = document.documentElement
  root.setAttribute('data-theme', def.id)
  root.classList.toggle('dark', def.colorScheme === 'dark')
  root.style.colorScheme = def.colorScheme
}

/** choice is the stored id or 'system'. */
export function useTheme() {
  const [choice, setChoice] = useState<string>(() => readStoredTheme() ?? 'system')

  useEffect(() => {
    if (choice !== 'system') {
      applyTheme(choice)
      return
    }
    applyTheme(systemTheme())
    if (typeof window.matchMedia !== 'function') return
    const mq = window.matchMedia(DARK_QUERY)
    const onChange = () => applyTheme(systemTheme())
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [choice])

  const select = useCallback((next: string) => {
    writeStoredTheme(next === 'system' ? null : next)
    setChoice(next)
  }, [])

  return { choice, select, themes }
}
