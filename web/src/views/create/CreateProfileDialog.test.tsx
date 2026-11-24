import { QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import { expectNoA11yViolations } from '@/test/axe'
import { profileFixture } from '@/test/fixtures'
import { createTestClient } from '@/test/queryWrapper'
import { DashboardView } from '@/views/dashboard/DashboardView'
import { ToastProvider } from '@/lib/toast/ToastProvider'
import { CreateProfileProvider } from './CreateProfileProvider'

afterEach(() => vi.restoreAllMocks())

function Where() {
  const l = useLocation()
  return <p data-testid="where">{l.pathname + l.search}</p>
}

function setup() {
  vi.spyOn(api, 'listProfiles').mockResolvedValue({ profiles: [profileFixture({ name: 'dev' })] })
  vi.spyOn(api, 'getJob').mockRejectedValue(new Error('no job'))
  const create = vi.spyOn(api, 'createProfile')
  const view = render(
    <QueryClientProvider client={createTestClient()}>
      <MemoryRouter>
        <ToastProvider>
          <CreateProfileProvider>
            <Routes>
              <Route path="/" element={<DashboardView />} />
              <Route path="*" element={<p>other page</p>} />
            </Routes>
            <Where />
          </CreateProfileProvider>
        </ToastProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { create, user: userEvent.setup(), ...view }
}

async function openDialog(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole('button', { name: 'New profile' }))
  return screen.findByRole('dialog', { name: 'New profile' })
}

const submit = () => screen.getByRole('button', { name: 'Create profile' })

describe('create profile dialog', () => {
  it('opens from the dashboard button with every labelled field and passes axe', async () => {
    const { user } = setup()
    const dialog = await openDialog(user)
    for (const label of ['Name', /^CPUs/, /^Memory/, /^Disk/, /^Root disk/, 'Runtime', 'VM type', 'Architecture', 'Mount type', 'Kubernetes']) {
      expect(within(dialog).getByLabelText(label)).toBeInTheDocument()
    }
    expect(within(dialog).getByLabelText(/^CPUs/)).toHaveAttribute('placeholder', '2')
    expect(within(dialog).getByText(/downloads a VM image and can take several minutes/)).toBeInTheDocument()
    expect(within(dialog).getByRole('option', { name: 'Incus' })).toBeInTheDocument()
    await expectNoA11yViolations(dialog)
  })

  it('shows the Kubernetes version only when Kubernetes is checked', async () => {
    const { user } = setup()
    const dialog = await openDialog(user)
    expect(within(dialog).queryByLabelText('Kubernetes version')).toBeNull()
    await user.click(within(dialog).getByLabelText('Kubernetes'))
    expect(within(dialog).getByLabelText('Kubernetes version')).toBeInTheDocument()
    await user.click(within(dialog).getByLabelText('Kubernetes'))
    expect(within(dialog).queryByLabelText('Kubernetes version')).toBeNull()
  })

  it('sends only the name for an untouched form, then closes and opens the job log on 202', async () => {
    const { user, create } = setup()
    create.mockResolvedValue({ jobId: 'j_9', profile: 'fresh' })
    const dialog = await openDialog(user)
    await user.type(within(dialog).getByLabelText('Name'), 'fresh')
    await user.click(submit())
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('/profiles/fresh/log?job=j_9'))
    expect(JSON.stringify(create.mock.calls[0][0])).toBe('{"name":"fresh"}')
    expect(screen.queryByRole('dialog', { name: 'New profile' })).toBeNull()
  })

  it('sends the filled fields with their wire names', async () => {
    const { user, create } = setup()
    create.mockResolvedValue({ jobId: 'j_9', profile: 'fresh' })
    const dialog = await openDialog(user)
    await user.type(within(dialog).getByLabelText('Name'), 'fresh')
    await user.type(within(dialog).getByLabelText(/^CPUs/), '4')
    await user.type(within(dialog).getByLabelText(/^Memory/), '8')
    await user.type(within(dialog).getByLabelText(/^Disk \(/), '120')
    await user.type(within(dialog).getByLabelText(/^Root disk/), '30')
    await user.selectOptions(within(dialog).getByLabelText('Runtime'), 'containerd')
    await user.selectOptions(within(dialog).getByLabelText('VM type'), 'qemu')
    await user.selectOptions(within(dialog).getByLabelText('Architecture'), 'x86_64')
    await user.selectOptions(within(dialog).getByLabelText('Mount type'), 'sshfs')
    await user.click(within(dialog).getByLabelText('Kubernetes'))
    await user.type(within(dialog).getByLabelText('Kubernetes version'), 'v1.31.2+k3s1')
    await user.click(submit())
    await waitFor(() => expect(create).toHaveBeenCalled())
    expect(JSON.parse(JSON.stringify(create.mock.calls[0][0]))).toEqual({
      name: 'fresh',
      cpus: 4,
      memoryGiB: 8,
      diskGiB: 120,
      rootDiskGiB: 30,
      runtime: 'containerd',
      vmType: 'qemu',
      arch: 'x86_64',
      mountType: 'sshfs',
      kubernetes: true,
      kubernetesVersion: 'v1.31.2+k3s1',
    })
  })

  it('blocks invalid input in the browser and focuses the first invalid field', async () => {
    const { user, create } = setup()
    const dialog = await openDialog(user)
    const name = within(dialog).getByLabelText('Name')
    await user.type(name, 'colima-dev')
    await user.type(within(dialog).getByLabelText(/^CPUs/), '0')
    await user.click(submit())
    expect(create).not.toHaveBeenCalled()
    expect(name).toHaveAttribute('aria-invalid', 'true')
    expect(name).toHaveFocus()
    expect(name.getAttribute('aria-describedby')).toContain('error')
    expect(within(dialog).getByLabelText(/^CPUs/)).toHaveAttribute('aria-invalid', 'true')
    expect(within(dialog).getByRole('alert')).toHaveTextContent(/Fix the marked/)
    await expectNoA11yViolations(dialog)
  })

  it('shows the server field messages verbatim and keeps the dialog open', async () => {
    const { user, create } = setup()
    create.mockRejectedValue(
      new ApiError(422, 'validation_failed', 'Some values are not allowed.', {
        fields: { cpus: 'must be between 1 and 16', kubernetesVersion: 'requires kubernetes to be enabled' },
      }),
    )
    const dialog = await openDialog(user)
    await user.type(within(dialog).getByLabelText('Name'), 'fresh')
    await user.type(within(dialog).getByLabelText(/^CPUs/), '99')
    await user.click(submit())
    const cpus = within(dialog).getByLabelText(/^CPUs/)
    await waitFor(() => expect(cpus).toHaveAttribute('aria-invalid', 'true'))
    expect(within(dialog).getByText('must be between 1 and 16')).toBeInTheDocument()
    expect(cpus).toHaveFocus()
    expect(screen.getByRole('dialog', { name: 'New profile' })).toBeInTheDocument()
  })

  it('marks the Name field on profile_exists', async () => {
    const { user, create } = setup()
    create.mockRejectedValue(new ApiError(409, 'profile_exists', 'exists'))
    const dialog = await openDialog(user)
    const name = within(dialog).getByLabelText('Name')
    await user.type(name, 'dev')
    await user.click(submit())
    await waitFor(() => expect(name).toHaveAttribute('aria-invalid', 'true'))
    expect(name).toHaveFocus()
    expect(within(dialog).getAllByText(/already exists/).length).toBeGreaterThan(0)
  })

  it('explains profile_busy and a network error, keeps the dialog open and allows a retry', async () => {
    const { user, create } = setup()
    create.mockRejectedValueOnce(new ApiError(409, 'profile_busy', 'busy', { jobId: 'j_1' }))
    create.mockRejectedValueOnce(new ApiError(0, 'network_error', 'Could not reach the server.'))
    create.mockResolvedValueOnce({ jobId: 'j_2', profile: 'fresh' })
    const dialog = await openDialog(user)
    await user.type(within(dialog).getByLabelText('Name'), 'fresh')
    await user.click(submit())
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(/job is already running/)
    await user.click(submit())
    await waitFor(() => expect(within(dialog).getByRole('alert')).toHaveTextContent(/Could not reach the server/))
    expect(within(dialog).getByLabelText('Name')).not.toHaveAttribute('aria-invalid')
    await user.click(submit())
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('/profiles/fresh/log?job=j_2'))
  })

  it('Escape closes the dialog, returns focus to the button, and reopening starts clean', async () => {
    const { user } = setup()
    const button = await screen.findByRole('button', { name: 'New profile' })
    const dialog = await openDialog(user)
    await user.type(within(dialog).getByLabelText('Name'), 'half')
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'New profile' })).toBeNull())
    expect(button).toHaveFocus()
    const again = await openDialog(user)
    expect(within(again).getByLabelText('Name')).toHaveValue('')
  })
})
