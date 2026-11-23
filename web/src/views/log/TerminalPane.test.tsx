import { render } from '@testing-library/react'
import { StrictMode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const h = vi.hoisted(() => {
  const terms: Array<{
    open: ReturnType<typeof vi.fn>
    write: ReturnType<typeof vi.fn>
    reset: ReturnType<typeof vi.fn>
    dispose: ReturnType<typeof vi.fn>
    loadAddon: ReturnType<typeof vi.fn>
    options: { theme?: unknown }
  }> = []
  const fits: Array<{ dispose: ReturnType<typeof vi.fn> }> = []
  return { terms, fits }
})

vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    open = vi.fn()
    write = vi.fn()
    reset = vi.fn()
    dispose = vi.fn()
    loadAddon = vi.fn()
    scrollToBottom = vi.fn()
    onScroll = vi.fn(() => ({ dispose: vi.fn() }))
    buffer = { active: { viewportY: 0, baseY: 0 } }
    options: { theme?: unknown }
    constructor(o: { theme?: unknown }) {
      this.options = o
      h.terms.push(this as never)
    }
  },
}))
vi.mock('@xterm/addon-fit', () => ({
  FitAddon: class {
    fit = vi.fn()
    dispose = vi.fn()
    constructor() {
      h.fits.push(this as never)
    }
  },
}))

import { TerminalPane } from './TerminalPane'

const disconnects = { resize: 0 }
let frames: FrameRequestCallback[]
const runFrames = () => {
  const cbs = frames
  frames = []
  cbs.forEach((cb) => cb(0))
}
const l = (seq: number, line = `line ${seq}`) => ({ seq, line })

beforeEach(() => {
  h.terms.length = 0
  h.fits.length = 0
  frames = []
  disconnects.resize = 0
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {
        disconnects.resize++
      }
    },
  )
  vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => frames.push(cb))
  vi.stubGlobal('cancelAnimationFrame', () => {})
})
afterEach(() => vi.unstubAllGlobals())

describe('TerminalPane', () => {
  it('writes ANSI lines with CRLF, only the new ones on update', () => {
    const red = '\x1b[31mred\x1b[0m'
    const { rerender } = render(<TerminalPane lines={[l(1, red), l(2)]} />)
    const term = h.terms[0]
    expect(term.open).toHaveBeenCalledTimes(1)
    runFrames()
    expect(term.write).toHaveBeenLastCalledWith(`${red}\r\nline 2\r\n`)
    rerender(<TerminalPane lines={[l(1, red), l(2), l(3)]} />)
    runFrames()
    expect(term.write).toHaveBeenCalledTimes(2)
    expect(term.write).toHaveBeenLastCalledWith('line 3\r\n')
    expect(term.reset).not.toHaveBeenCalled()
  })

  it('batches thousands of lines into one write per frame', () => {
    const many = Array.from({ length: 5000 }, (_, i) => l(i + 1))
    render(<TerminalPane lines={many} />)
    const term = h.terms[0]
    let rounds = 0
    while (frames.length) {
      runFrames()
      rounds++
    }
    expect(term.write).toHaveBeenCalledTimes(rounds)
    expect(rounds).toBe(10)
  })

  it('redraws when an older line shows up and writes the notice first', () => {
    const { rerender } = render(<TerminalPane lines={[l(1), l(3)]} notice="2 earlier lines were dropped" />)
    runFrames()
    const term = h.terms[0]
    expect(term.write.mock.calls[0][0]).toContain('2 earlier lines were dropped')
    rerender(<TerminalPane lines={[l(1), l(2), l(3)]} notice="2 earlier lines were dropped" />)
    runFrames()
    expect(term.reset).toHaveBeenCalledTimes(1)
    expect(term.write).toHaveBeenLastCalledWith(
      expect.stringMatching(/line 1\r\nline 2\r\nline 3\r\n$/),
    )
  })

  it('disposes the terminal, the addon and the observers on unmount', () => {
    const { unmount } = render(
      <StrictMode>
        <TerminalPane lines={[]} />
      </StrictMode>,
    )
    unmount()
    expect(h.terms.length).toBeGreaterThan(0)
    for (const t of h.terms) expect(t.dispose).toHaveBeenCalledTimes(1)
    for (const f of h.fits) expect(f.dispose).toHaveBeenCalledTimes(1)
    expect(disconnects.resize).toBe(h.terms.length)
  })

  it('applies the active theme again when data-theme changes', async () => {
    render(<TerminalPane lines={[]} />)
    const term = h.terms[0]
    const before = term.options.theme
    document.documentElement.style.setProperty('--background', 'rgb(1, 2, 3)')
    document.documentElement.setAttribute('data-theme', 'light')
    await new Promise((r) => setTimeout(r, 0))
    expect(term.options.theme).not.toBe(before)
    expect((term.options.theme as { background?: string }).background).toBe('rgb(1, 2, 3)')
    document.documentElement.style.removeProperty('--background')
  })
})
