import { QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
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

function setup() {
  vi.spyOn(api, 'listProfiles').mockResolvedValue({ profiles: [profileFixture({ name: 'dev' })] })
  vi.spyOn(api, 'getJob').mockRejectedValue(new Error('no job'))
  const create = vi.spyOn(api, 'createProfile')
  render(
    <QueryClientProvider client={createTestClient()}>
      <MemoryRouter>
        <ToastProvider>
          <CreateProfileProvider>
            <Routes>
              <Route path="/" element={<DashboardView />} />
              <Route path="*" element={<p>other page</p>} />
            </Routes>
          </CreateProfileProvider>
        </ToastProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { create, user: userEvent.setup() }
}

async function openAdvanced(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole('button', { name: 'New profile' }))
  const dialog = await screen.findByRole('dialog', { name: 'New profile' })
  await user.type(within(dialog).getByLabelText('Name'), 'fresh')
  await user.click(within(dialog).getByRole('button', { name: 'Advanced' }))
  return dialog
}

const submit = () => screen.getByRole('button', { name: 'Create profile' })
const sent = (create: ReturnType<typeof setup>['create']) => JSON.stringify(create.mock.calls[0][0])
const NOTE = 'Mounted folders are visible to every container in this VM.'

describe('create profile dialog, mounts and environment variables', () => {
  it('shows the exposure note and references it from the Mounts group', async () => {
    const { user } = setup()
    const dialog = await openAdvanced(user)
    const group = within(dialog).getByRole('group', { name: 'Mounts' })
    expect(within(group).getByText(NOTE)).toBeVisible()
    expect(group).toHaveAccessibleDescription(new RegExp(NOTE))
    expect(group).toHaveAccessibleDescription(/replace Colima's default mount/)
    expect(within(dialog).getByRole('group', { name: 'Environment variables' })).toBeVisible()
    await expectNoA11yViolations(dialog)
  })

  it('sends mounts as {path, writable} and env as an object, nothing else', async () => {
    const { user, create } = setup()
    create.mockResolvedValue({ jobId: 'j_1', profile: 'fresh' })
    const dialog = await openAdvanced(user)
    await user.click(within(dialog).getByRole('button', { name: 'Add mount' }))
    await user.type(within(dialog).getByRole('textbox', { name: 'Mount 1 path' }), '/srv/data')
    await user.click(within(dialog).getByRole('button', { name: 'Add mount' }))
    await user.type(within(dialog).getByRole('textbox', { name: 'Mount 2 path' }), '/srv/work')
    await user.click(within(dialog).getByRole('checkbox', { name: 'Mount 2 writable' }))
    await user.click(within(dialog).getByRole('button', { name: 'Add variable' }))
    await user.type(within(dialog).getByRole('textbox', { name: 'Environment variable 1 name' }), 'HTTP_PROXY')
    await user.type(within(dialog).getByLabelText('Environment variable 1 value'), 'http://192.0.2.1:3128')
    await user.click(within(dialog).getByRole('button', { name: 'Add variable' }))
    await user.type(within(dialog).getByRole('textbox', { name: 'Environment variable 2 name' }), 'a')
    await user.click(submit())
    await waitFor(() => expect(create).toHaveBeenCalled())
    expect(sent(create)).toBe(
      '{"name":"fresh","mounts":[{"path":"/srv/data","writable":false},{"path":"/srv/work","writable":true}],' +
        '"env":{"HTTP_PROXY":"http://192.0.2.1:3128","a":""}}',
    )
  })

  it('omits empty groups even when blank rows were added', async () => {
    const { user, create } = setup()
    create.mockResolvedValue({ jobId: 'j_1', profile: 'fresh' })
    const dialog = await openAdvanced(user)
    await user.click(within(dialog).getByRole('button', { name: 'Add mount' }))
    await user.click(within(dialog).getByRole('button', { name: 'Add variable' }))
    await user.click(submit())
    await waitFor(() => expect(create).toHaveBeenCalled())
    expect(sent(create)).toBe('{"name":"fresh"}')
  })

  it('blocks relative paths, duplicates, bad keys and bad values without sending', async () => {
    const { user, create } = setup()
    const dialog = await openAdvanced(user)
    const add = within(dialog).getByRole('button', { name: 'Add mount' })
    await user.click(add)
    await user.type(within(dialog).getByRole('textbox', { name: 'Mount 1 path' }), 'relative')
    await user.click(add)
    await user.type(within(dialog).getByRole('textbox', { name: 'Mount 2 path' }), '/srv/a')
    await user.click(add)
    await user.type(within(dialog).getByRole('textbox', { name: 'Mount 3 path' }), '/SRV/a')
    await user.click(within(dialog).getByRole('button', { name: 'Add variable' }))
    await user.type(within(dialog).getByRole('textbox', { name: 'Environment variable 1 name' }), '1bad')
    await user.click(within(dialog).getByRole('button', { name: 'Add variable' }))
    await user.type(within(dialog).getByRole('textbox', { name: 'Environment variable 2 name' }), 'OK')
    await user.type(within(dialog).getByLabelText('Environment variable 2 value'), 'x"')
    await user.click(submit())
    expect(create).not.toHaveBeenCalled()
    const first = within(dialog).getByRole('textbox', { name: 'Mount 1 path' })
    expect(first).toHaveFocus()
    expect(first).toHaveAccessibleDescription(/absolute path/)
    expect(within(dialog).getByRole('textbox', { name: 'Mount 2 path' })).not.toHaveAttribute('aria-invalid')
    expect(within(dialog).getByRole('textbox', { name: 'Mount 3 path' })).toHaveAccessibleDescription(/repeats an earlier mount/)
    expect(within(dialog).getByRole('textbox', { name: 'Environment variable 1 name' })).toHaveAttribute('aria-invalid', 'true')
    expect(within(dialog).getByLabelText('Environment variable 2 value')).toHaveAccessibleDescription(/must not end with a double quote/)
    await expectNoA11yViolations(dialog)
  })

  it('shows an advisory hint, not an error, for a broad writable mount', async () => {
    const { user, create } = setup()
    create.mockResolvedValue({ jobId: 'j_1', profile: 'fresh' })
    const dialog = await openAdvanced(user)
    await user.click(within(dialog).getByRole('button', { name: 'Add mount' }))
    const path = within(dialog).getByRole('textbox', { name: 'Mount 1 path' })
    await user.type(path, '/Users')
    await user.click(within(dialog).getByRole('checkbox', { name: 'Mount 1 writable' }))
    expect(path).toHaveAccessibleDescription(/broad folder/)
    expect(path).not.toHaveAttribute('aria-invalid')
    await user.click(submit())
    await waitFor(() => expect(create).toHaveBeenCalled())
  })

  it('disables the add buttons at the caps', async () => {
    const { user } = setup()
    const dialog = await openAdvanced(user)
    const addMount = within(dialog).getByRole('button', { name: 'Add mount' })
    for (let i = 0; i < 16; i++) await user.click(addMount)
    expect(addMount).toBeDisabled()
    const addVar = within(dialog).getByRole('button', { name: 'Add variable' })
    for (let i = 0; i < 32; i++) await user.click(addVar)
    expect(addVar).toBeDisabled()
  }, 30000)

  it('masks values by default and the show toggle works from the keyboard', async () => {
    const { user } = setup()
    const dialog = await openAdvanced(user)
    await user.click(within(dialog).getByRole('button', { name: 'Add variable' }))
    await user.type(within(dialog).getByRole('textbox', { name: 'Environment variable 1 name' }), 'API_TOKEN')
    const value = within(dialog).getByLabelText('Environment variable 1 value')
    await user.type(value, 'synthetic-secret')
    expect(value).toHaveAttribute('type', 'password')
    const toggle = within(dialog).getByRole('button', { name: 'Show value of API_TOKEN' })
    expect(toggle).toHaveAttribute('aria-pressed', 'false')
    toggle.focus()
    await user.keyboard('{Enter}')
    expect(toggle).toHaveAttribute('aria-pressed', 'true')
    expect(value).toHaveAttribute('type', 'text')
    await user.keyboard(' ')
    expect(toggle).toHaveAttribute('aria-pressed', 'false')
    expect(value).toHaveAttribute('type', 'password')
    expect(value).toHaveValue('synthetic-secret')
    await expectNoA11yViolations(dialog)
  })

  it('announces counts and positions only, never values, and moves focus', async () => {
    const { user } = setup()
    const dialog = await openAdvanced(user)
    await user.click(within(dialog).getByRole('button', { name: 'Add mount' }))
    const status = within(dialog).getByRole('status')
    expect(status).toHaveTextContent('Mount 1 added. 1 mount in the list.')
    expect(within(dialog).getByRole('textbox', { name: 'Mount 1 path' })).toHaveFocus()
    await user.click(within(dialog).getByRole('button', { name: 'Add variable' }))
    expect(within(dialog).getByRole('textbox', { name: 'Environment variable 1 name' })).toHaveFocus()
    await user.type(within(dialog).getByLabelText('Environment variable 1 value'), 'synthetic-secret')
    await user.click(within(dialog).getByRole('button', { name: 'Show value of variable 1' }))
    expect(status).not.toHaveTextContent('synthetic-secret')
    await user.click(within(dialog).getByRole('button', { name: 'Remove environment variable 1' }))
    expect(status).toHaveTextContent('Variable 1 removed. No variables left.')
    expect(status).not.toHaveTextContent('synthetic-secret')
    expect(within(dialog).getByRole('button', { name: 'Add variable' })).toHaveFocus()
    await user.click(within(dialog).getByRole('button', { name: 'Remove mount 1' }))
    expect(status).toHaveTextContent('Mount 1 removed. No mounts left.')
    expect(within(dialog).getByRole('button', { name: 'Add mount' })).toHaveFocus()
  })

  it('maps server 422 field names onto the right rows and passes axe', async () => {
    const { user, create } = setup()
    create.mockRejectedValue(
      new ApiError(422, 'validation_failed', 'Some values are not allowed.', {
        fields: {
          'mounts[1].path': 'must be an existing directory on this machine',
          'env[0]': 'name must be letters',
          'env.ZED': 'must not contain a NUL byte',
          'mounts[7].path': 'beyond the rows',
          'env.NOPE': 'unknown key',
        },
      }),
    )
    const dialog = await openAdvanced(user)
    const addMount = within(dialog).getByRole('button', { name: 'Add mount' })
    await user.click(addMount) // row 1 stays blank, so the server's mounts[1] is row 3
    await user.click(addMount)
    await user.type(within(dialog).getByRole('textbox', { name: 'Mount 2 path' }), '/srv/a')
    await user.click(addMount)
    await user.type(within(dialog).getByRole('textbox', { name: 'Mount 3 path' }), '/srv/b')
    const addVar = within(dialog).getByRole('button', { name: 'Add variable' })
    await user.click(addVar)
    await user.type(within(dialog).getByRole('textbox', { name: 'Environment variable 1 name' }), 'ZED')
    await user.type(within(dialog).getByLabelText('Environment variable 1 value'), 'v')
    await user.click(addVar)
    await user.type(within(dialog).getByRole('textbox', { name: 'Environment variable 2 name' }), 'ALPHA')
    await user.click(submit())

    const bad = await within(dialog).findByRole('textbox', { name: 'Mount 3 path' })
    await waitFor(() => expect(bad).toHaveAttribute('aria-invalid', 'true'))
    expect(bad).toHaveAccessibleDescription('must be an existing directory on this machine')
    expect(within(dialog).getByRole('textbox', { name: 'Mount 2 path' })).not.toHaveAttribute('aria-invalid')
    // Sorted order is ALPHA, ZED, so env[0] is the second row.
    expect(within(dialog).getByRole('textbox', { name: 'Environment variable 2 name' })).toHaveAccessibleDescription('name must be letters')
    expect(within(dialog).getByRole('textbox', { name: 'Environment variable 1 name' })).not.toHaveAttribute('aria-invalid')
    expect(within(dialog).getByLabelText('Environment variable 1 value')).toHaveAccessibleDescription('must not contain a NUL byte')
    expect(within(dialog).queryByText('beyond the rows')).toBeNull()
    expect(within(dialog).queryByText('unknown key')).toBeNull()
    expect(within(dialog).getByRole('alert')).toHaveTextContent(/Some values were rejected/)
    await expectNoA11yViolations(dialog)

    await user.type(bad, 'x')
    expect(bad).not.toHaveAttribute('aria-invalid')
  })

  it('shows group errors for mounts and env', async () => {
    const { user, create } = setup()
    create.mockRejectedValue(
      new ApiError(422, 'validation_failed', 'x', {
        fields: { mounts: 'must list at most 16 mounts', env: 'must list at most 32 variables' },
      }),
    )
    const dialog = await openAdvanced(user)
    await user.click(within(dialog).getByRole('button', { name: 'Add mount' }))
    await user.type(within(dialog).getByRole('textbox', { name: 'Mount 1 path' }), '/srv/a')
    await user.click(submit())
    await waitFor(() =>
      expect(within(dialog).getByRole('group', { name: 'Mounts' })).toHaveAccessibleDescription(/at most 16 mounts/),
    )
    expect(within(dialog).getByRole('group', { name: 'Environment variables' })).toHaveAccessibleDescription(/at most 32 variables/)
  })
})
