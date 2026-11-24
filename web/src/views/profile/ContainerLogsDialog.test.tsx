import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import { expectNoA11yViolations } from '@/test/axe'
import { createTestClient } from '@/test/queryWrapper'
import { ContainerLogsDialog } from './ContainerLogsDialog'

afterEach(() => vi.restoreAllMocks())

function setup() {
  const get = vi.spyOn(api, 'getContainerLogs')
  const user = userEvent.setup()
  const view = render(
    <QueryClientProvider client={createTestClient()}>
      <ContainerLogsDialog profile="dev" container={{ id: 'abc123def456', name: 'synthetic-web' }} />
    </QueryClientProvider>,
  )
  return { get, user, ...view }
}

const open = (user: ReturnType<typeof userEvent.setup>) =>
  user.click(screen.getByRole('button', { name: 'Logs for synthetic-web' }))

describe('ContainerLogsDialog', () => {
  it('scrolls inside the window when the content is taller than it', async () => {
    const { get, user } = setup()
    get.mockResolvedValue({ lines: ['a'], truncated: false })
    await open(user)
    const dialog = await screen.findByRole('dialog')
    expect(dialog.className).toContain('max-h-[85vh]')
    expect(dialog.className).toContain('overflow-y-auto')
  })

  it('requests nothing until opened, then renders lines as text and passes axe', async () => {
    const { get, user } = setup()
    get.mockResolvedValue({
      lines: ['2026-01-01T00:00:00.000000000Z <script>alert(1)</script>', '2026-01-01T00:00:01.000000000Z ok'],
      truncated: false,
    })
    expect(get).not.toHaveBeenCalled()
    await open(user)
    const log = await screen.findByRole('log')
    expect(log.textContent).toContain('<script>alert(1)</script>')
    expect(log.querySelector('script')).toBeNull()
    expect(get).toHaveBeenCalledWith('dev', 'abc123def456', 200, expect.anything())
    await expectNoA11yViolations(document.body)
  })

  it('requests the new tail and refetches on Refresh', async () => {
    const { get, user } = setup()
    get.mockResolvedValue({ lines: ['a'], truncated: false })
    await open(user)
    await screen.findByRole('log')
    await user.selectOptions(screen.getByRole('combobox', { name: 'Lines' }), '500')
    await waitFor(() => expect(get).toHaveBeenLastCalledWith('dev', 'abc123def456', 500, expect.anything()))
    await screen.findByRole('log')
    const calls = get.mock.calls.length
    await user.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(get.mock.calls.length).toBe(calls + 1))
  })

  it('shows the truncated note and the empty message', async () => {
    const { get, user } = setup()
    get.mockResolvedValue({ lines: ['a'], truncated: true })
    await open(user)
    expect(await screen.findByText('Some output is not shown.')).toBeInTheDocument()
    get.mockResolvedValue({ lines: [], truncated: false })
    await user.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(await screen.findByText('This container has produced no output.')).toBeInTheDocument()
  })

  it.each([
    [new ApiError(502, 'docker_unavailable', 'x'), 'Docker is not reachable in this VM yet.', true],
    [new ApiError(500, 'internal', 'x'), 'The logs could not be loaded.', true],
    [new ApiError(409, 'invalid_state', 'x', { reason: 'logs_unavailable' }), "This container's log driver does not keep logs readable here.", false],
    [new ApiError(404, 'container_not_found', 'x'), 'This container no longer exists. Close this dialog and refresh the container list.', false],
  ])('renders the error state %#', async (error, message, retry) => {
    const { get, user } = setup()
    get.mockRejectedValue(error)
    await open(user)
    expect(await screen.findByText(message)).toBeInTheDocument()
    expect(get).toHaveBeenCalledTimes(1)
    const button = screen.queryByRole('button', { name: 'Retry' })
    if (!retry) return expect(button).toBeNull()
    get.mockResolvedValue({ lines: ['ok'], truncated: false })
    await user.click(button!)
    expect(await screen.findByRole('log')).toBeInTheDocument()
  })

  it('closes on Escape and returns focus to the button', async () => {
    const { get, user } = setup()
    get.mockResolvedValue({ lines: ['a'], truncated: false })
    await open(user)
    await screen.findByRole('log')
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(screen.getByRole('button', { name: 'Logs for synthetic-web' })).toHaveFocus()
  })

  it('copies the visible text and handles a missing clipboard', async () => {
    const { get, user } = setup()
    get.mockResolvedValue({ lines: ['one', 'two'], truncated: false })
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    await open(user)
    await screen.findByRole('log')
    await user.click(screen.getByRole('button', { name: 'Copy' }))
    expect(writeText).toHaveBeenCalledWith('one\ntwo')
    expect(await screen.findByText('Copied.')).toBeInTheDocument()
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    await user.click(screen.getByRole('button', { name: 'Copy' }))
    expect(await screen.findByText('Could not copy to the clipboard.')).toBeInTheDocument()
  })
})
