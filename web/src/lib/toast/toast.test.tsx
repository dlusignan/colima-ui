import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useEffect } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { Toaster } from './Toaster'
import { ToastProvider } from './ToastProvider'
import { useToast } from './useToast'
import type { ToastApi } from './context'
import { expectNoA11yViolations } from '@/test/axe'

const held: { current: ToastApi | null } = { current: null }
const api: ToastApi = {
  success: (o) => held.current!.success(o),
  error: (o) => held.current!.error(o),
  info: (o) => held.current!.info(o),
  close: (id) => held.current!.close(id),
}
function Capture() {
  const t = useToast()
  useEffect(() => {
    held.current = t
  })
  return null
}

function setup() {
  return render(
    <ToastProvider>
      <Capture />
      <button>page button</button>
      <Toaster />
    </ToastProvider>,
  )
}

const advance = (ms: number) => act(() => vi.advanceTimersByTimeAsync(ms))
const fire = (fn: () => void) => act(fn)

beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }))
afterEach(() => vi.useRealTimers())

describe('toasts', () => {
  it('throws a clear error outside the provider', () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
    expect(() => render(<Capture />)).toThrow('useToast must be used inside a ToastProvider')
    spy.mockRestore()
  })

  it('puts the viewport in the top left corner', () => {
    setup()
    const viewport = screen.getByRole('region', { name: 'Notifications' })
    for (const c of ['fixed', 'top-4', 'left-4', 'right-4', 'z-40']) expect(viewport).toHaveClass(c)
    expect(viewport).not.toHaveClass('right-0')
  })

  it('auto-dismisses success and info after 5 s but keeps errors', async () => {
    setup()
    fire(() => {
      api.success({ title: 'Saved' })
      api.info({ title: 'FYI' })
      api.error({ title: 'Broke' })
    })
    expect(screen.getAllByRole('status')).toHaveLength(2)
    expect(screen.getByRole('alert')).toHaveTextContent('Broke')
    await advance(4900)
    expect(screen.getAllByRole('status')).toHaveLength(2)
    await advance(200)
    expect(screen.queryAllByRole('status')).toHaveLength(0)
    await advance(60_000)
    expect(screen.getByRole('alert')).toHaveTextContent('Broke')
  })

  it('lets durationMs override the default', async () => {
    setup()
    fire(() => api.success({ title: 'Quick', durationMs: 1000 }))
    await advance(1100)
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })

  it('pauses on hover and resumes after', async () => {
    setup()
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    fire(() => api.success({ title: 'Hover me' }))
    await advance(3000)
    await user.hover(screen.getByRole('status'))
    await advance(10_000)
    expect(screen.getByRole('status')).toBeInTheDocument()
    await user.unhover(screen.getByRole('status'))
    await advance(1900)
    expect(screen.getByRole('status')).toBeInTheDocument()
    await advance(300)
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })

  it('pauses while focus is inside the viewport', async () => {
    setup()
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    fire(() => api.success({ title: 'Focus me' }))
    await user.tab()
    await user.tab()
    expect(screen.getByRole('status')).toHaveFocus()
    await advance(10_000)
    expect(screen.getByRole('status')).toBeInTheDocument()
  })

  it('pauses while the tab is hidden', async () => {
    setup()
    fire(() => api.success({ title: 'Hidden' }))
    await advance(2000)
    const hidden = vi.spyOn(document, 'hidden', 'get')
    hidden.mockReturnValue(true)
    fire(() => document.dispatchEvent(new Event('visibilitychange')))
    await advance(20_000)
    expect(screen.getByRole('status')).toBeInTheDocument()
    hidden.mockReturnValue(false)
    fire(() => document.dispatchEvent(new Event('visibilitychange')))
    await advance(4900)
    expect(screen.getByRole('status')).toBeInTheDocument()
    await advance(200)
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })

  it('shows 3 at once, newest on top, and queues the rest without dropping any', async () => {
    setup()
    fire(() => {
      for (const n of ['one', 'two', 'three', 'four', 'five']) api.error({ title: n })
    })
    const titles = () => screen.getAllByRole('alert').map((a) => a.textContent?.replace(/×$/, ''))
    expect(titles()).toEqual(['three', 'two', 'one'])
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    await user.click(within(screen.getByRole('alert', { name: 'two' })).getByRole('button', { name: 'Dismiss notification' }))
    await advance(500)
    expect(titles()).toEqual(['four', 'three', 'one'])
    await user.click(within(screen.getByRole('alert', { name: 'one' })).getByRole('button', { name: 'Dismiss notification' }))
    await advance(500)
    expect(titles()).toEqual(['five', 'four', 'three'])
  })

  it('removes a toast with the Dismiss notification button', async () => {
    setup()
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    fire(() => api.error({ title: 'Close me' }))
    await user.click(screen.getByRole('button', { name: 'Dismiss notification' }))
    await advance(500)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('closes a toast by id, including a queued one', async () => {
    setup()
    fire(() => {
      for (const n of ['a', 'b', 'c']) api.error({ title: n })
    })
    let queued = ''
    fire(() => {
      queued = api.error({ title: 'queued' })
    })
    fire(() => api.close(queued))
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    await user.click(within(screen.getByRole('alert', { name: 'a' })).getByRole('button', { name: 'Dismiss notification' }))
    await advance(500)
    expect(screen.getAllByRole('alert').map((a) => a.getAttribute('aria-labelledby') && a.textContent?.replace(/×$/, ''))).toEqual(['c', 'b'])
  })

  it('runs an action button with the keyboard and closes the toast', async () => {
    setup()
    const onClick = vi.fn()
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    fire(() => api.error({ title: 'Failed', action: { label: 'Retry', onClick } }))
    screen.getByRole('button', { name: 'Retry' }).focus()
    await user.keyboard('{Enter}')
    expect(onClick).toHaveBeenCalledOnce()
    await advance(500)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('renders an action link', () => {
    setup()
    fire(() => api.info({ title: 'Done', action: { label: 'Open log', href: '/jobs/1' } }))
    expect(screen.getByRole('link', { name: 'Open log' })).toHaveAttribute('href', '/jobs/1')
  })

  it('assigns status and alert roles with the text as description', () => {
    setup()
    fire(() => {
      api.success({ title: 'Ok', description: 'it worked' })
      api.error({ title: 'Bad', description: 'it broke' })
    })
    expect(screen.getByRole('status', { description: 'it worked' })).toBeInTheDocument()
    expect(screen.getByRole('alert', { description: 'it broke' })).toBeInTheDocument()
  })

  it('does not move focus when a toast appears', () => {
    setup()
    screen.getByRole('button', { name: 'page button' }).focus()
    fire(() => api.error({ title: 'Quiet' }))
    expect(screen.getByRole('button', { name: 'page button' })).toHaveFocus()
  })

  it('replaces a toast with the same key, visible or queued', () => {
    setup()
    fire(() => {
      api.info({ title: 'v1', key: 'same' })
      api.info({ title: 'v2', key: 'same' })
    })
    expect(screen.getAllByRole('status')).toHaveLength(1)
    expect(screen.getByRole('status')).toHaveTextContent('v2')
    fire(() => {
      for (const n of ['x', 'y']) api.error({ title: n })
      api.info({ title: 'q1', key: 'q' })
      api.info({ title: 'q2', key: 'q' })
    })
    expect(screen.getAllByRole('status').length + screen.getAllByRole('alert').length).toBe(3)
  })

  it('moves focus into the viewport on F6', async () => {
    setup()
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    fire(() => api.error({ title: 'Reach me' }))
    await user.keyboard('{F6}')
    expect(screen.getByRole('region', { name: 'Notifications' })).toHaveFocus()
  })

  it('passes axe with toasts on screen', async () => {
    vi.useRealTimers()
    const { container } = setup()
    fire(() => {
      api.success({ title: 'Ok', description: 'it worked', action: { label: 'Undo', onClick: () => {} } })
      api.error({ title: 'Bad', description: 'it broke' })
    })
    await expectNoA11yViolations(container)
  })
})
