import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import { expectNoA11yViolations } from '@/test/axe'
import { createTestClient } from '@/test/queryWrapper'
import { SshConfigDialog } from './SshConfigDialog'

afterEach(() => vi.restoreAllMocks())

const sample = {
  raw: 'Host colima-synthetic\n  HostName 127.0.0.1\n  Port 60022\n',
  entries: [
    { key: 'Host', value: 'colima-synthetic' },
    { key: 'HostName', value: '127.0.0.1' },
    { key: 'Port', value: '60022' },
  ],
}

function setup() {
  const get = vi.spyOn(api, 'getSshConfig')
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={createTestClient()}>
      <SshConfigDialog profile="dev" />
    </QueryClientProvider>,
  )
  return { get, user }
}

const open = (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByRole('button', { name: 'SSH config' }))

describe('SshConfigDialog', () => {
  it('fetches nothing until opened, then shows the table, the raw text and the hint', async () => {
    const { get, user } = setup()
    get.mockResolvedValue(sample)
    expect(get).not.toHaveBeenCalled()
    await open(user)
    expect(await screen.findByRole('cell', { name: '60022' })).toBeInTheDocument()
    expect(screen.getByRole('rowheader', { name: 'HostName' })).toBeInTheDocument()
    expect(screen.getByLabelText('Raw SSH config').textContent).toBe(sample.raw)
    expect(screen.getByText('ssh -F ./ssh_config colima-synthetic')).toBeInTheDocument()
    expect(get).toHaveBeenCalledTimes(1)
    await expectNoA11yViolations(document.body)
  })

  it('copies the raw text and handles a missing clipboard', async () => {
    const { get, user } = setup()
    get.mockResolvedValue(sample)
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    await open(user)
    await screen.findByLabelText('Raw SSH config')
    await user.click(screen.getByRole('button', { name: 'Copy' }))
    expect(writeText).toHaveBeenCalledWith(sample.raw)
    expect(await screen.findByText('Copied.')).toBeInTheDocument()
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    await user.click(screen.getByRole('button', { name: 'Copy' }))
    expect(await screen.findByText('Could not copy to the clipboard.')).toBeInTheDocument()
  })

  it('says to start a stopped profile and offers no retry', async () => {
    const { get, user } = setup()
    get.mockRejectedValue(new ApiError(409, 'profile_not_running', 'x', { status: 'stopped' }))
    await open(user)
    expect(await screen.findByText('Start the profile to see its SSH config.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Copy' })).toBeDisabled()
  })

  it('shows a generic error with Retry that refetches', async () => {
    const { get, user } = setup()
    get.mockRejectedValueOnce(new ApiError(502, 'colima_failed', 'x', {}))
    await open(user)
    expect(await screen.findByText('The SSH config could not be loaded.')).toBeInTheDocument()
    get.mockResolvedValue(sample)
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(screen.getByRole('cell', { name: '60022' })).toBeInTheDocument())
  })
})
