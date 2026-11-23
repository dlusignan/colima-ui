import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { act } from 'react'
import { afterEach, beforeEach, vi } from 'vitest'

type Listener = (e: { matches: boolean; media: string }) => void

let current = { matches: true, listeners: new Set<Listener>() }

/** Set the simulated OS preference (true = dark) without firing events. */
export function setPrefersDark(matches: boolean) {
  current.matches = matches
}

/** Change the simulated OS preference and fire `change` to subscribers. */
export function fireMediaChange(matches: boolean) {
  current.matches = matches
  act(() => {
    current.listeners.forEach((l) => l({ matches, media: '' }))
  })
}

beforeEach(() => {
  current = { matches: true, listeners: new Set() }
  const state = current
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    configurable: true,
    value: vi.fn((media: string) => ({
      get matches() {
        return media.includes('light') ? !state.matches : state.matches
      },
      media,
      onchange: null,
      addEventListener: (_: string, l: Listener) => state.listeners.add(l),
      removeEventListener: (_: string, l: Listener) => state.listeners.delete(l),
      addListener: (l: Listener) => state.listeners.add(l),
      removeListener: (l: Listener) => state.listeners.delete(l),
      dispatchEvent: () => false,
    })),
  })
})

afterEach(() => {
  cleanup()
  localStorage.clear()
  vi.restoreAllMocks()
  document.documentElement.removeAttribute('data-theme')
  document.documentElement.classList.remove('dark')
  document.documentElement.style.colorScheme = ''
})
