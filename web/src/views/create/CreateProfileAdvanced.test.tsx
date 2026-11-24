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

async function openDialog(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole('button', { name: 'New profile' }))
  return screen.findByRole('dialog', { name: 'New profile' })
}

async function openAdvanced(user: ReturnType<typeof userEvent.setup>) {
  const dialog = await openDialog(user)
  await user.type(within(dialog).getByLabelText('Name'), 'fresh')
  await user.click(within(dialog).getByRole('button', { name: 'Advanced' }))
  return dialog
}

const submit = () => screen.getByRole('button', { name: 'Create profile' })
const sent = (create: ReturnType<typeof setup>['create']) => JSON.stringify(create.mock.calls[0][0])

describe('create profile dialog, Advanced section', () => {
  it('is collapsed by default and opens and closes from the keyboard', async () => {
    const { user } = setup()
    const dialog = await openDialog(user)
    const toggle = within(dialog).getByRole('button', { name: 'Advanced' })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(within(dialog).queryByRole('textbox', { name: 'Hostname' })).toBeNull()
    toggle.focus()
    await user.keyboard('{Enter}')
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(within(dialog).getByRole('textbox', { name: 'Hostname' })).toBeVisible()
    await user.keyboard(' ')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(within(dialog).queryByRole('textbox', { name: 'Hostname' })).toBeNull()
  })

  it('shows every advanced field once opened and passes axe', async () => {
    const { user } = setup()
    const dialog = await openAdvanced(user)
    for (const name of ['Hostname', 'Add DNS server', 'Add DNS host']) {
      expect(within(dialog).getByRole(name.startsWith('Add') ? 'button' : 'textbox', { name })).toBeVisible()
    }
    for (const name of ['SSH agent', 'Mount inotify', 'Network address', 'Rosetta']) {
      expect(within(dialog).getByRole('checkbox', { name })).toBeVisible()
    }
    for (const name of ['Port forwarder', 'Network mode']) {
      expect(within(dialog).getByRole('combobox', { name })).toBeVisible()
    }
    expect(within(dialog).getByRole('textbox', { name: 'CPU type' })).toBeVisible()
    await expectNoA11yViolations(dialog)
  })

  it('sends nothing extra when the section was opened and left untouched', async () => {
    const { user, create } = setup()
    create.mockResolvedValue({ jobId: 'j_1', profile: 'fresh' })
    await openAdvanced(user)
    await user.click(submit())
    await waitFor(() => expect(create).toHaveBeenCalled())
    expect(sent(create)).toBe('{"name":"fresh"}')
  })

  it('sends each advanced field under its wire key and nothing else', async () => {
    const { user, create } = setup()
    create.mockResolvedValue({ jobId: 'j_1', profile: 'fresh' })
    const dialog = await openDialog(user)
    await user.type(within(dialog).getByLabelText('Name'), 'fresh')
    await user.selectOptions(within(dialog).getByLabelText('VM type'), 'qemu')
    await user.click(within(dialog).getByRole('button', { name: 'Advanced' }))
    await user.type(within(dialog).getByRole('textbox', { name: 'Hostname' }), 'dev-vm')
    await user.click(within(dialog).getByRole('button', { name: 'Add DNS server' }))
    await user.type(within(dialog).getByRole('textbox', { name: 'DNS server 1' }), '1.1.1.1')
    await user.click(within(dialog).getByRole('button', { name: 'Add DNS server' }))
    await user.type(within(dialog).getByRole('textbox', { name: 'DNS server 2' }), '2001:db8::1')
    await user.click(within(dialog).getByRole('button', { name: 'Add DNS host' }))
    await user.type(within(dialog).getByRole('textbox', { name: 'DNS host 1 name' }), 'example.com')
    await user.type(within(dialog).getByRole('textbox', { name: 'DNS host 1 address' }), '192.0.2.10')
    await user.click(within(dialog).getByRole('checkbox', { name: 'SSH agent' }))
    await user.click(within(dialog).getByRole('checkbox', { name: 'Mount inotify' }))
    await user.click(within(dialog).getByRole('checkbox', { name: 'Network address' }))
    await user.selectOptions(within(dialog).getByRole('combobox', { name: 'Port forwarder' }), 'grpc')
    await user.selectOptions(within(dialog).getByRole('combobox', { name: 'Network mode' }), 'bridged')
    await user.type(within(dialog).getByRole('textbox', { name: 'Network interface' }), 'en0')
    await user.type(within(dialog).getByRole('textbox', { name: 'CPU type' }), 'cortex-a72')
    await user.click(submit())
    await waitFor(() => expect(create).toHaveBeenCalled())
    expect(sent(create)).toBe(
      '{"name":"fresh","vmType":"qemu","hostname":"dev-vm","dns":["1.1.1.1","2001:db8::1"],' +
        '"dnsHosts":[{"name":"example.com","address":"192.0.2.10"}],"sshAgent":true,"portForwarder":"grpc",' +
        '"mountInotify":true,"networkAddress":true,"networkMode":"bridged","networkInterface":"en0","cpuType":"cortex-a72"}',
    )
  })

  it('sends Rosetta when VZ and aarch64 allow it', async () => {
    const { user, create } = setup()
    create.mockResolvedValue({ jobId: 'j_1', profile: 'fresh' })
    const dialog = await openAdvanced(user)
    await user.selectOptions(within(dialog).getByLabelText('VM type'), 'vz')
    await user.selectOptions(within(dialog).getByLabelText('Architecture'), 'aarch64')
    await user.click(within(dialog).getByRole('checkbox', { name: 'Rosetta' }))
    await user.click(submit())
    await waitFor(() => expect(create).toHaveBeenCalled())
    expect(sent(create)).toBe('{"name":"fresh","vmType":"vz","arch":"aarch64","vzRosetta":true}')
  })

  it('disables Rosetta with a visible reason and does not send it', async () => {
    const { user, create } = setup()
    create.mockResolvedValue({ jobId: 'j_1', profile: 'fresh' })
    const dialog = await openAdvanced(user)
    const rosetta = within(dialog).getByRole('checkbox', { name: 'Rosetta' })
    expect(rosetta).toBeEnabled()
    expect(rosetta).toHaveAccessibleDescription(/Needs VZ and aarch64.*Intel Mac/)
    await user.click(rosetta)
    expect(rosetta).toBeChecked()

    await user.selectOptions(within(dialog).getByLabelText('VM type'), 'qemu')
    expect(rosetta).toBeDisabled()
    expect(rosetta).not.toBeChecked()
    expect(rosetta).toHaveAccessibleDescription(/needs the VZ VM type/)
    expect(within(dialog).getByText(/needs the VZ VM type/)).toBeVisible()

    await user.selectOptions(within(dialog).getByLabelText('VM type'), 'vz')
    await user.selectOptions(within(dialog).getByLabelText('Architecture'), 'x86_64')
    expect(rosetta).toBeDisabled()
    expect(rosetta).toHaveAccessibleDescription(/needs the aarch64 architecture/)

    await user.click(submit())
    await waitFor(() => expect(create).toHaveBeenCalled())
    expect(sent(create)).toBe('{"name":"fresh","vmType":"vz","arch":"x86_64"}')
  })

  it('shows the network interface only for bridged mode', async () => {
    const { user } = setup()
    const dialog = await openAdvanced(user)
    const mode = within(dialog).getByRole('combobox', { name: 'Network mode' })
    expect(within(dialog).queryByRole('textbox', { name: 'Network interface' })).toBeNull()
    expect(mode).toHaveAccessibleDescription(/Choose bridged/)
    await user.selectOptions(mode, 'bridged')
    expect(within(dialog).getByRole('textbox', { name: 'Network interface' })).toBeVisible()
    await user.selectOptions(mode, 'shared')
    expect(within(dialog).queryByRole('textbox', { name: 'Network interface' })).toBeNull()
  })

  it('enables CPU type only for QEMU and says why otherwise', async () => {
    const { user } = setup()
    const dialog = await openAdvanced(user)
    const cpu = within(dialog).getByRole('textbox', { name: 'CPU type' })
    expect(cpu).toBeDisabled()
    expect(cpu).toHaveAccessibleDescription(/QEMU VM type only/)
    await user.selectOptions(within(dialog).getByLabelText('VM type'), 'qemu')
    expect(cpu).toBeEnabled()
    await user.selectOptions(within(dialog).getByLabelText('VM type'), 'krunkit')
    expect(cpu).toBeDisabled()
  })

  it('adds and removes DNS rows from the keyboard, names each row and announces changes', async () => {
    const { user } = setup()
    const dialog = await openAdvanced(user)
    const add = within(dialog).getByRole('button', { name: 'Add DNS server' })
    add.focus()
    await user.keyboard('{Enter}')
    expect(within(dialog).getByRole('textbox', { name: 'DNS server 1' })).toHaveFocus()
    expect(within(dialog).getByRole('status')).toHaveTextContent('DNS server 1 added. 1 DNS server in the list.')
    await user.keyboard('1.1.1.1')
    await user.click(add)
    await user.keyboard('8.8.8.8')
    await user.click(add)
    await user.keyboard('9.9.9.9')
    expect(within(dialog).getByRole('status')).toHaveTextContent('DNS server 3 added. 3 DNS servers in the list.')

    const remove = within(dialog).getByRole('button', { name: 'Remove DNS server 2' })
    remove.focus()
    await user.keyboard('{Enter}')
    expect(within(dialog).getByRole('status')).toHaveTextContent('DNS server 2 removed. 2 DNS servers left.')
    expect(within(dialog).getAllByRole('textbox', { name: /^DNS server \d$/ }).map((e) => (e as HTMLInputElement).value)).toEqual([
      '1.1.1.1',
      '9.9.9.9',
    ])
    expect(within(dialog).getByRole('textbox', { name: 'DNS server 2' })).toHaveFocus()

    await user.click(within(dialog).getByRole('button', { name: 'Remove DNS server 2' }))
    expect(within(dialog).getByRole('textbox', { name: 'DNS server 1' })).toHaveFocus()
    await user.click(within(dialog).getByRole('button', { name: 'Remove DNS server 1' }))
    expect(within(dialog).getByRole('status')).toHaveTextContent('DNS server 1 removed. No DNS servers left.')
    expect(within(dialog).queryByRole('textbox', { name: /^DNS server/ })).toBeNull()
    expect(within(dialog).getByRole('button', { name: 'Add DNS server' })).toHaveFocus()
  })

  it('adds and removes DNS host rows with named fields', async () => {
    const { user } = setup()
    const dialog = await openAdvanced(user)
    await user.click(within(dialog).getByRole('button', { name: 'Add DNS host' }))
    expect(within(dialog).getByRole('textbox', { name: 'DNS host 1 name' })).toHaveFocus()
    expect(within(dialog).getByRole('status')).toHaveTextContent('DNS host 1 added. 1 DNS host in the list.')
    await user.click(within(dialog).getByRole('button', { name: 'Add DNS host' }))
    expect(within(dialog).getByRole('textbox', { name: 'DNS host 2 address' })).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Remove DNS host 1' }))
    expect(within(dialog).getByRole('status')).toHaveTextContent('DNS host 1 removed. 1 DNS host left.')
    expect(within(dialog).queryByRole('textbox', { name: 'DNS host 2 name' })).toBeNull()
  })

  it('stops adding DNS servers at 8 and DNS hosts at 16', async () => {
    const { user } = setup()
    const dialog = await openAdvanced(user)
    const addDns = within(dialog).getByRole('button', { name: 'Add DNS server' })
    for (let i = 0; i < 8; i++) await user.click(addDns)
    expect(addDns).toBeDisabled()
    const addHost = within(dialog).getByRole('button', { name: 'Add DNS host' })
    for (let i = 0; i < 16; i++) await user.click(addHost)
    expect(addHost).toBeDisabled()
  })

  it('does not send blank rows', async () => {
    const { user, create } = setup()
    create.mockResolvedValue({ jobId: 'j_1', profile: 'fresh' })
    const dialog = await openAdvanced(user)
    await user.click(within(dialog).getByRole('button', { name: 'Add DNS server' }))
    await user.click(within(dialog).getByRole('button', { name: 'Add DNS host' }))
    await user.click(submit())
    await waitFor(() => expect(create).toHaveBeenCalled())
    expect(sent(create)).toBe('{"name":"fresh"}')
  })

  it('blocks a bad advanced value in the browser and focuses it', async () => {
    const { user, create } = setup()
    const dialog = await openAdvanced(user)
    await user.type(within(dialog).getByRole('textbox', { name: 'Hostname' }), 'bad_host')
    await user.click(within(dialog).getByRole('button', { name: 'Add DNS server' }))
    await user.type(within(dialog).getByRole('textbox', { name: 'DNS server 1' }), '1.1.1.1:53')
    await user.click(within(dialog).getByRole('button', { name: 'Add DNS host' }))
    await user.type(within(dialog).getByRole('textbox', { name: 'DNS host 1 name' }), 'example.com')
    await user.click(submit())
    expect(create).not.toHaveBeenCalled()
    const hostname = within(dialog).getByRole('textbox', { name: 'Hostname' })
    expect(hostname).toHaveAttribute('aria-invalid', 'true')
    expect(hostname).toHaveFocus()
    expect(hostname).toHaveAccessibleDescription(/DNS label/)
    expect(within(dialog).getByRole('textbox', { name: 'DNS server 1' })).toHaveAccessibleDescription(/IPv4 or IPv6/)
    expect(within(dialog).getByRole('textbox', { name: 'DNS host 1 address' })).toHaveAttribute('aria-invalid', 'true')
    await expectNoA11yViolations(dialog)
  })

  it('opens the collapsed section to show a client error inside it', async () => {
    const { user, create } = setup()
    const dialog = await openAdvanced(user)
    await user.type(within(dialog).getByRole('textbox', { name: 'Hostname' }), '-bad')
    await user.click(within(dialog).getByRole('button', { name: 'Advanced' }))
    expect(within(dialog).getByRole('button', { name: 'Advanced' })).toHaveAttribute('aria-expanded', 'false')
    await user.click(submit())
    expect(create).not.toHaveBeenCalled()
    expect(within(dialog).getByRole('button', { name: 'Advanced' })).toHaveAttribute('aria-expanded', 'true')
    expect(within(dialog).getByRole('textbox', { name: 'Hostname' })).toHaveFocus()
  })

  it('keeps entered values while the section is collapsed and sends them', async () => {
    const { user, create } = setup()
    create.mockResolvedValue({ jobId: 'j_1', profile: 'fresh' })
    const dialog = await openAdvanced(user)
    await user.type(within(dialog).getByRole('textbox', { name: 'Hostname' }), 'dev-vm')
    await user.click(within(dialog).getByRole('button', { name: 'Advanced' }))
    await user.click(submit())
    await waitFor(() => expect(create).toHaveBeenCalled())
    expect(sent(create)).toBe('{"name":"fresh","hostname":"dev-vm"}')
  })

  it('shows server 422 messages next to the right advanced field, mapped to the form rows', async () => {
    const { user, create } = setup()
    create.mockRejectedValue(
      new ApiError(422, 'validation_failed', 'Some values are not allowed.', {
        fields: {
          hostname: 'must be a DNS label',
          'dns[1]': 'must be an IPv4 or IPv6 address',
          'dnsHosts[0].name': 'must be a host name',
          'dnsHosts[0].address': 'must be an IPv4 or IPv6 address',
          vzRosetta: 'requires vmType vz and arch aarch64',
          networkInterface: 'requires networkMode bridged',
          cpuType: 'must be a CPU model name',
          portForwarder: 'must be one of ssh, grpc, none',
          networkMode: 'must be one of shared, bridged',
          'dns[9]': 'beyond the rows',
          '__proto__': 'ignored',
        },
      }),
    )
    const dialog = await openAdvanced(user)
    await user.type(within(dialog).getByRole('textbox', { name: 'Hostname' }), 'ok')
    // Row 1 stays blank, so the server's dns[1] is row 3 on screen.
    const add = within(dialog).getByRole('button', { name: 'Add DNS server' })
    await user.click(add)
    await user.type(within(dialog).getByRole('textbox', { name: 'DNS server 1' }), '1.1.1.1')
    await user.click(add)
    await user.click(add)
    await user.type(within(dialog).getByRole('textbox', { name: 'DNS server 3' }), '8.8.8.8')
    await user.click(within(dialog).getByRole('button', { name: 'Add DNS host' }))
    await user.type(within(dialog).getByRole('textbox', { name: 'DNS host 1 name' }), 'example.com')
    await user.type(within(dialog).getByRole('textbox', { name: 'DNS host 1 address' }), '192.0.2.10')
    await user.click(within(dialog).getByRole('checkbox', { name: 'Rosetta' }))
    await user.click(submit())

    const hostname = within(dialog).getByRole('textbox', { name: 'Hostname' })
    await waitFor(() => expect(hostname).toHaveAttribute('aria-invalid', 'true'))
    expect(hostname).toHaveFocus()
    expect(hostname).toHaveAccessibleDescription(/must be a DNS label/)
    expect(within(dialog).getByRole('textbox', { name: 'DNS server 1' })).not.toHaveAttribute('aria-invalid')
    expect(within(dialog).getByRole('textbox', { name: 'DNS server 2' })).not.toHaveAttribute('aria-invalid')
    expect(within(dialog).getByRole('textbox', { name: 'DNS server 3' })).toHaveAccessibleDescription('must be an IPv4 or IPv6 address')
    expect(within(dialog).getByRole('textbox', { name: 'DNS host 1 name' })).toHaveAccessibleDescription('must be a host name')
    expect(within(dialog).getByRole('textbox', { name: 'DNS host 1 address' })).toHaveAccessibleDescription(
      'must be an IPv4 or IPv6 address',
    )
    expect(within(dialog).getByRole('checkbox', { name: 'Rosetta' })).toHaveAccessibleDescription(/requires vmType vz and arch aarch64/)
    expect(within(dialog).getByRole('combobox', { name: 'Port forwarder' })).toHaveAccessibleDescription('must be one of ssh, grpc, none')
    expect(within(dialog).getByRole('combobox', { name: 'Network mode' })).toHaveAccessibleDescription(/must be one of shared, bridged/)
    expect(within(dialog).queryByText('beyond the rows')).toBeNull()
    expect(within(dialog).getByRole('alert')).toHaveTextContent(/Some values were rejected/)
    await expectNoA11yViolations(dialog)
  })

  it('opens the section for a server error on a field inside it and clears it on edit', async () => {
    const { user, create } = setup()
    create.mockRejectedValue(
      new ApiError(422, 'validation_failed', 'Some values are not allowed.', {
        fields: { hostname: 'must be a DNS label', 'dnsHosts[1].name': 'must be a host name' },
      }),
    )
    const dialog = await openAdvanced(user)
    const addHost = within(dialog).getByRole('button', { name: 'Add DNS host' })
    await user.click(addHost)
    await user.type(within(dialog).getByRole('textbox', { name: 'DNS host 1 name' }), 'a.test')
    await user.type(within(dialog).getByRole('textbox', { name: 'DNS host 1 address' }), '192.0.2.1')
    await user.click(addHost)
    await user.type(within(dialog).getByRole('textbox', { name: 'DNS host 2 name' }), 'b.test')
    await user.type(within(dialog).getByRole('textbox', { name: 'DNS host 2 address' }), '192.0.2.2')
    await user.type(within(dialog).getByRole('textbox', { name: 'Hostname' }), 'x')
    await user.click(within(dialog).getByRole('button', { name: 'Advanced' }))
    await user.click(submit())
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Advanced' })).toHaveAttribute('aria-expanded', 'true'))
    const second = within(dialog).getByRole('textbox', { name: 'DNS host 2 name' })
    expect(second).toHaveAccessibleDescription('must be a host name')
    await user.type(second, 'c')
    expect(second).not.toHaveAttribute('aria-invalid')
    expect(within(dialog).getByRole('textbox', { name: 'DNS host 1 name' })).not.toHaveAttribute('aria-invalid')
  })
})
