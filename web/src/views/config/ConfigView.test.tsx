import { QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import type { Profile, ProfileConfig } from '@/lib/api/types'
import { profileFixture } from '@/test/fixtures'
import { createTestClient } from '@/test/queryWrapper'
import { expectNoA11yViolations } from '@/test/axe'
import { ConfigView } from './ConfigView'

const config: ProfileConfig = { name: 'synthetic-a', cpu: 2, memoryGiB: 4, diskGiB: 60 }

afterEach(() => vi.restoreAllMocks())

function setup(opts: { profile?: Partial<Profile>; getConfig?: () => Promise<ProfileConfig> } = {}) {
  vi.spyOn(api, 'getConfig').mockImplementation(opts.getConfig ?? (() => Promise.resolve(config)))
  vi.spyOn(api, 'listProfiles').mockResolvedValue({ profiles: [profileFixture(opts.profile)] })
  const router = createMemoryRouter(
    [
      { path: '/profiles/:name/config', element: <ConfigView /> },
      { path: '/profiles/:name', element: <p>detail page</p> },
      { path: '/profiles/:name/log', element: <p>log page</p> },
    ],
    { initialEntries: ['/profiles/synthetic-a/config'] },
  )
  const view = render(
    <QueryClientProvider client={createTestClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { router, ...view }
}

const cpu = () => screen.findByLabelText(/^CPU/)

describe('ConfigView', () => {
  it('loads values, marks changed fields and passes axe when idle', async () => {
    const user = userEvent.setup()
    const { container } = setup()
    const input = await cpu()
    expect(input).toHaveValue(2)
    expect(screen.queryByText('changed')).toBeNull()
    await expectNoA11yViolations(container)
    await user.clear(input)
    await user.type(input, '4')
    expect(screen.getByText('changed')).toBeInTheDocument()
  })

  it('shows client errors, focuses the first invalid field and does not call the API', async () => {
    const user = userEvent.setup()
    const put = vi.spyOn(api, 'updateConfig')
    const { container } = setup()
    const input = await cpu()
    await user.clear(input)
    await user.type(input, '0')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input).toHaveFocus()
    expect(input.getAttribute('aria-describedby')).toContain('error')
    expect(screen.getByRole('alert')).toHaveTextContent(/Fix the marked/)
    expect(put).not.toHaveBeenCalled()
    await expectNoA11yViolations(container)
  })

  it('renders the server field message verbatim next to the field, even when the client passes', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'updateConfig').mockRejectedValue(
      new ApiError(422, 'validation_failed', 'invalid', {
        fields: { cpu: 'cpu must be at most 8 (host cores)' },
      }),
    )
    const { container } = setup()
    const input = await cpu()
    await user.clear(input)
    await user.type(input, '64')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('cpu must be at most 8 (host cores)')).toBeInTheDocument()
    expect(input).toHaveAttribute('aria-invalid', 'true')
    await waitFor(() => expect(input).toHaveFocus())
    await expectNoA11yViolations(container)
    await user.type(input, '1')
    expect(screen.queryByText('cpu must be at most 8 (host cores)')).toBeNull()
  })

  it('sends only changed fields, then offers a restart that starts the operation and links to the log', async () => {
    const user = userEvent.setup()
    const put = vi.spyOn(api, 'updateConfig').mockResolvedValue({
      config: { ...config, cpu: 4 },
      restartRequired: ['cpu'],
    })
    const restart = vi.spyOn(api, 'restartProfile').mockResolvedValue({ jobId: 'j_9' })
    const { container } = setup()
    const input = await cpu()
    await user.clear(input)
    await user.type(input, '4')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(put).toHaveBeenCalledWith('synthetic-a', { cpu: 4 })
    expect(await screen.findByText(/Restart required for: CPU/)).toBeInTheDocument()
    await expectNoA11yViolations(container)
    await user.click(screen.getByRole('button', { name: 'Restart now' }))
    await user.click(await screen.findByRole('button', { name: 'Restart' }))
    expect(restart).toHaveBeenCalledWith('synthetic-a')
    const link = await screen.findByRole('link', { name: /restart log/ })
    expect(link).toHaveAttribute('href', '/profiles/synthetic-a/log?job=j_9')
  })

  it('shows no restart prompt when nothing needs a restart', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'updateConfig').mockResolvedValue({
      config: { ...config, memoryGiB: 8 },
      restartRequired: [],
    })
    setup({ profile: { status: 'stopped' } })
    const mem = await screen.findByLabelText(/^Memory/)
    await user.clear(mem)
    await user.type(mem, '8')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Saved. Applies on next start.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Restart now' })).toBeNull()
  })

  it('disables the form while a job runs on the profile', async () => {
    const { container } = setup({ profile: { activeJobId: 'j_1' } })
    const input = await cpu()
    await waitFor(() => expect(input).toBeDisabled())
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    expect(screen.getByText(/job is running/)).toBeInTheDocument()
    await expectNoA11yViolations(container)
  })

  it('tells the user to try again on a 409 without a job id', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'updateConfig').mockRejectedValue(
      new ApiError(409, 'profile_busy', 'busy'),
    )
    setup()
    const input = await cpu()
    await user.clear(input)
    await user.type(input, '3')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('The profile was busy. Try again.')).toBeInTheDocument()
  })

  it('shows the first-run empty state on 404 for a known profile', async () => {
    setup({
      getConfig: () => Promise.reject(new ApiError(404, 'profile_not_found', 'no file')),
    })
    expect(await screen.findByText(/No configuration file yet/)).toBeInTheDocument()
    expect(screen.queryByRole('form')).toBeNull()
  })

  it('says the profile does not exist on 404 for an unknown profile', async () => {
    vi.spyOn(api, 'getConfig').mockRejectedValue(new ApiError(404, 'profile_not_found', 'x'))
    vi.spyOn(api, 'listProfiles').mockResolvedValue({ profiles: [] })
    const router = createMemoryRouter([{ path: '/profiles/:name/config', element: <ConfigView /> }], {
      initialEntries: ['/profiles/typo/config'],
    })
    render(
      <QueryClientProvider client={createTestClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )
    expect(await screen.findByText(/profile typo does not exist/)).toBeInTheDocument()
  })

  it('guards navigation while dirty and lets the user stay or leave', async () => {
    const user = userEvent.setup()
    const { router } = setup()
    const input = await cpu()
    await user.clear(input)
    await user.type(input, '3')
    await router.navigate('/profiles/synthetic-a')
    expect(await screen.findByText('Discard unsaved changes?')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Stay' }))
    expect(router.state.location.pathname).toBe('/profiles/synthetic-a/config')
    await router.navigate('/profiles/synthetic-a')
    await user.click(await screen.findByRole('button', { name: 'Leave' }))
    expect(await screen.findByText('detail page')).toBeInTheDocument()
  })

  it('does not guard a clean form', async () => {
    const { router } = setup()
    await cpu()
    await router.navigate('/profiles/synthetic-a')
    expect(await screen.findByText('detail page')).toBeInTheDocument()
  })
})
