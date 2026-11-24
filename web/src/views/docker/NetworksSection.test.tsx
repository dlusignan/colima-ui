import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi, type MockInstance } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { api, PRUNE_NETWORKS_CONFIRMATION } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import type { DockerNetwork, NetworkDetail, NetworkPruneResult } from '@/lib/api/types'
import { Toaster } from '@/lib/toast/Toaster'
import { ToastProvider } from '@/lib/toast/ToastProvider'
import { expectNoA11yViolations } from '@/test/axe'
import { profileFixture } from '@/test/fixtures'
import { createTestClient } from '@/test/queryWrapper'
import { NetworksSection } from './NetworksSection'
import { networkPruneTitle } from './networkText'

afterEach(() => vi.restoreAllMocks())

const net = (id: string, name: string, containers: number, over: Partial<DockerNetwork> = {}): DockerNetwork => ({
  id,
  name,
  driver: 'bridge',
  scope: 'local',
  internal: false,
  containers,
  ...over,
})

const networks: DockerNetwork[] = [
  net('aaaaaaaaaaaa', 'bridge', 0),
  net('bbbbbbbbbbbb', 'host', 0, { driver: 'host' }),
  net('cccccccccccc', 'none', 0, { driver: 'null' }),
  net('dddddddddddd', 'synthetic-app', 2),
  net('eeeeeeeeeeee', 'synthetic-one', 1),
  net('ffffffffffff', 'synthetic-old', 0),
  net('111111111111', 'synthetic-scratch', 0, { internal: true }),
  net('222222222222', 'BRIDGE', 0),
]

const detail: NetworkDetail = {
  id: 'dddddddddddd',
  name: 'synthetic-app',
  driver: 'bridge',
  scope: 'local',
  internal: true,
  subnets: [
    { subnet: '172.28.0.0/16', gateway: '172.28.0.1' },
    { subnet: 'fd00:1::/64', gateway: '' },
  ],
  containers: [
    { name: 'synthetic-db', ipv4Address: '172.28.0.3', ipv6Address: '' },
    { name: '<b>stopped</b>', ipv4Address: '', ipv6Address: '' },
  ],
}

function setup(list: DockerNetwork[] = networks, status: 'running' | 'stopped' = 'running') {
  const getNetworks = vi.spyOn(api, 'getNetworks').mockResolvedValue({ networks: list })
  const client = createTestClient()
  const invalidate = vi.spyOn(client, 'invalidateQueries')
  const view = render(
    <QueryClientProvider client={client}>
      <ToastProvider>
        <NetworksSection profile={profileFixture({ name: 'dev', status })} />
        <Toaster />
      </ToastProvider>
    </QueryClientProvider>,
  )
  return { getNetworks, invalidate, user: userEvent.setup(), ...view }
}

const dialog = () => screen.getByRole('dialog')
const refreshed = (invalidate: MockInstance) => {
  const keys = invalidate.mock.calls.map((c: unknown[]) => JSON.stringify((c[0] as { queryKey: unknown }).queryKey))
  expect(keys).toContain(JSON.stringify(queryKeys.networks('dev')))
  expect(keys).toContain(JSON.stringify(queryKeys.engine('dev')))
}

describe('create network', () => {
  async function open(s: ReturnType<typeof setup>) {
    await screen.findByRole('table', { name: 'Networks in dev' })
    await s.user.click(screen.getByRole('button', { name: 'Create network' }))
    return within(await screen.findByRole('dialog'))
  }
  const created = net('999999999999', 'new-net', 0)

  it('sends exactly { name, internal } with no subnet, and refreshes the list and the engine', async () => {
    const s = setup()
    const create = vi.spyOn(api, 'createNetwork').mockResolvedValue(created)
    const d = await open(s)
    expect(d.getByLabelText('Driver')).toHaveValue('bridge')
    expect(d.getByLabelText('Driver')).toHaveAttribute('readonly')
    await s.user.type(d.getByLabelText('Name'), 'new-net')
    await s.user.click(d.getByRole('button', { name: 'Create network' }))
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1))
    expect(JSON.stringify(create.mock.calls[0])).toBe('["dev",{"name":"new-net","internal":false}]')
    expect(await screen.findByText('Created network new-net')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    refreshed(s.invalidate)
  })

  it('sends the internal flag and the subnet as typed (the server clears host bits)', async () => {
    const s = setup()
    const create = vi.spyOn(api, 'createNetwork').mockResolvedValue(created)
    const d = await open(s)
    expect(d.getByText(/clears host bits, so 172\.28\.0\.5\/16 becomes 172\.28\.0\.0\/16/)).toBeInTheDocument()
    await s.user.type(d.getByLabelText('Name'), 'new-net')
    await s.user.click(d.getByRole('checkbox', { name: 'Internal (no outside access)' }))
    await s.user.type(d.getByLabelText('Subnet (optional)'), '172.28.0.5/16')
    await s.user.click(d.getByRole('button', { name: 'Create network' }))
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1))
    expect(JSON.stringify(create.mock.calls[0][1])).toBe('{"name":"new-net","internal":true,"subnet":"172.28.0.5/16"}')
  })

  it.each([
    ['an empty name', '', '', 'Enter a name.'],
    ['a name with a space', 'bad name', '', /Use letters, digits/],
    ['a name over 128 characters', 'a'.repeat(129), '', 'Use at most 128 characters.'],
    ['the reserved name bridge', 'bridge', '', /built into Docker/],
    ['the reserved name HOST', 'HOST', '', /built into Docker/],
    ['a subnet without a prefix', 'ok', '172.28.0.0', /^Enter one range in CIDR form/],
    ['a subnet that is not an address', 'ok', 'nope/16', /^Enter one range in CIDR form/],
    ['an IPv4 prefix that is too wide', 'ok', '10.0.0.0/7', 'An IPv4 prefix must be between /8 and /28.'],
    ['an IPv4 prefix that is too narrow', 'ok', '10.0.0.0/29', 'An IPv4 prefix must be between /8 and /28.'],
    ['an IPv6 prefix that is too wide', 'ok', 'fd00::/32', 'An IPv6 prefix must be between /48 and /120.'],
    ['a loopback range', 'ok', '127.0.0.0/8', /overlaps loopback/],
    ['the Colima VM range', 'ok', '192.168.5.0/24', /^This range overlaps .*Colima VM/],
    ['a range that contains a refused one', 'ok', '192.168.0.0/16', /^This range overlaps .*Colima VM/],
    ['a link-local IPv6 range', 'ok', 'fe80::/64', /overlaps link-local/],
    ['an IPv4-mapped IPv6 range', 'ok', '::ffff:10.0.0.0/120', /^IPv4-mapped/],
  ])('blocks %s and sends nothing', async (_n, name, subnet, message) => {
    const s = setup()
    const create = vi.spyOn(api, 'createNetwork')
    const d = await open(s)
    if (name) await s.user.type(d.getByLabelText('Name'), name)
    if (subnet) await s.user.type(d.getByLabelText('Subnet (optional)'), subnet)
    await s.user.click(d.getByRole('button', { name: 'Create network' }))
    expect(await d.findByText(message)).toBeInTheDocument()
    expect(create).not.toHaveBeenCalled()
  })

  it('links the subnet error and the hint to the field', async () => {
    const s = setup()
    const d = await open(s)
    await s.user.type(d.getByLabelText('Name'), 'ok')
    await s.user.type(d.getByLabelText('Subnet (optional)'), '127.0.0.0/8')
    await s.user.click(d.getByRole('button', { name: 'Create network' }))
    const field = d.getByLabelText('Subnet (optional)')
    expect(field).toHaveAttribute('aria-invalid', 'true')
    expect(field).toHaveAccessibleDescription(/Optional, one range in CIDR form.*overlaps loopback/)
  })

  it('shows a 409 on the name field and keeps the dialog open without a toast', async () => {
    const s = setup()
    vi.spyOn(api, 'createNetwork').mockRejectedValue(new ApiError(409, 'conflict', 'x', { name: 'dup' }))
    const d = await open(s)
    await s.user.type(d.getByLabelText('Name'), 'dup')
    await s.user.click(d.getByRole('button', { name: 'Create network' }))
    expect(await d.findByText('A network with this name already exists.')).toBeInTheDocument()
    expect(d.getByLabelText('Name')).toHaveAttribute('aria-invalid', 'true')
    expect(d.getByLabelText('Name')).toHaveAccessibleDescription('A network with this name already exists.')
    expect(screen.queryByText('Could not create network dup')).not.toBeInTheDocument()
    await waitFor(() => refreshed(s.invalidate))
  })

  it('mentions an overlap in the 409 text when a subnet was sent', async () => {
    const s = setup()
    vi.spyOn(api, 'createNetwork').mockRejectedValue(new ApiError(409, 'conflict', 'x', { name: 'n' }))
    const d = await open(s)
    await s.user.type(d.getByLabelText('Name'), 'n')
    await s.user.type(d.getByLabelText('Subnet (optional)'), '172.28.0.0/16')
    await s.user.click(d.getByRole('button', { name: 'Create network' }))
    expect(await d.findByText(/or the subnet overlaps another Docker network/)).toBeInTheDocument()
  })

  it('shows 422 messages on the name and on the subnet', async () => {
    const s = setup()
    const create = vi
      .spyOn(api, 'createNetwork')
      .mockRejectedValueOnce(new ApiError(422, 'invalid_request', 'x', { fields: { subnet: 'Not an allowed subnet.' } }))
      .mockRejectedValueOnce(new ApiError(422, 'invalid_request', 'x', { fields: { name: 'Not a valid Docker name.' } }))
    const d = await open(s)
    await s.user.type(d.getByLabelText('Name'), 'ok')
    await s.user.type(d.getByLabelText('Subnet (optional)'), '172.28.0.0/16')
    await s.user.click(d.getByRole('button', { name: 'Create network' }))
    expect(await d.findByText('Not an allowed subnet.')).toBeInTheDocument()
    await s.user.click(d.getByRole('button', { name: 'Create network' }))
    expect(await d.findByText('Not a valid Docker name.')).toBeInTheDocument()
    expect(d.queryByText('Not an allowed subnet.')).not.toBeInTheDocument()
    expect(create).toHaveBeenCalledTimes(2)
  })

  it('toasts any other failure and passes axe with the dialog open', async () => {
    const s = setup()
    vi.spyOn(api, 'createNetwork').mockRejectedValue(new ApiError(502, 'docker_unavailable', 'x'))
    const d = await open(s)
    await expectNoA11yViolations(document.body)
    await s.user.type(d.getByLabelText('Name'), 'ok')
    await s.user.click(d.getByRole('button', { name: 'Create network' }))
    expect(await screen.findByText('Could not create network ok')).toBeInTheDocument()
  })

  it('passes axe with errors showing, has focus in the name field and Cancel closes it', async () => {
    const s = setup()
    const d = await open(s)
    await waitFor(() => expect(d.getByLabelText('Name')).toHaveFocus())
    await s.user.type(d.getByLabelText('Subnet (optional)'), 'bad')
    await s.user.click(d.getByRole('button', { name: 'Create network' }))
    await d.findByText('Enter a name.')
    await expectNoA11yViolations(document.body)
    await s.user.click(d.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })
})

describe('inspect network', () => {
  it('loads once by NAME, shows details as text, and never polls', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const get = vi.spyOn(api, 'getNetwork').mockResolvedValue(detail)
      const s = setup()
      await screen.findByRole('table', { name: 'Networks in dev' })
      expect(get).not.toHaveBeenCalled()
      await s.user.click(screen.getByRole('button', { name: 'Inspect synthetic-app' }))
      const d = within(await screen.findByRole('dialog'))
      expect(await d.findByText('172.28.0.0/16')).toBeInTheDocument()
      expect(get).toHaveBeenCalledWith('dev', 'synthetic-app', expect.anything())
      expect(get.mock.calls[0][1]).not.toBe('dddddddddddd')
      expect(d.getByText('gateway 172.28.0.1')).toBeInTheDocument()
      expect(d.getByText('no gateway')).toBeInTheDocument()
      expect(d.getByText('Yes')).toBeInTheDocument()
      expect(d.getByRole('cell', { name: '172.28.0.3' })).toBeInTheDocument()
      expect(d.getByText('<b>stopped</b>')).toBeInTheDocument()
      expect(dialog().querySelector('b')).toBeNull()
      expect(d.getByText(/2 containers, running or stopped/)).toBeInTheDocument()
      await expectNoA11yViolations(document.body)
      await vi.advanceTimersByTimeAsync(120_000)
      expect(get).toHaveBeenCalledTimes(1)
    } finally {
      vi.useRealTimers()
    }
  })

  it('sends the name of a network that is named like an id', async () => {
    const get = vi.spyOn(api, 'getNetwork').mockResolvedValue({ ...detail, name: 'abcdef012345' })
    const s = setup([net('999999999999', 'abcdef012345', 0)])
    await screen.findByRole('table', { name: 'Networks in dev' })
    await s.user.click(screen.getByRole('button', { name: 'Inspect abcdef012345' }))
    await waitFor(() => expect(get).toHaveBeenCalledWith('dev', 'abcdef012345', expect.anything()))
  })

  it('shows no subnets and no containers, after an error with Retry', async () => {
    const get = vi
      .spyOn(api, 'getNetwork')
      .mockRejectedValueOnce(new ApiError(500, 'internal_error', 'x'))
      .mockResolvedValue({ ...detail, subnets: [], containers: [] })
    const s = setup()
    await screen.findByRole('table', { name: 'Networks in dev' })
    await s.user.click(screen.getByRole('button', { name: 'Inspect synthetic-app' }))
    const d = within(await screen.findByRole('dialog'))
    expect(await d.findByRole('alert')).toHaveTextContent('The network could not be loaded.')
    await s.user.click(d.getByRole('button', { name: 'Retry' }))
    expect(await d.findByText('No subnets.')).toBeInTheDocument()
    expect(d.getByText('No container is attached to this network.')).toBeInTheDocument()
    expect(get).toHaveBeenCalledTimes(2)
    await s.user.click(d.getByRole('button', { name: 'Close' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('explains a network that no longer exists', async () => {
    vi.spyOn(api, 'getNetwork').mockRejectedValue(new ApiError(404, 'not_found', 'x'))
    const s = setup()
    await screen.findByRole('table', { name: 'Networks in dev' })
    await s.user.click(screen.getByRole('button', { name: 'Inspect synthetic-old' }))
    expect(await screen.findByText(/no longer exists/)).toBeInTheDocument()
  })
})

describe('remove network', () => {
  it('is disabled with the visible reason for built-in networks, in any letter case', async () => {
    const s = setup()
    const remove = vi.spyOn(api, 'removeNetwork')
    await screen.findByRole('table', { name: 'Networks in dev' })
    for (const name of ['bridge', 'host', 'none', 'BRIDGE']) {
      const button = screen.getByRole('button', { name: `Remove ${name}` })
      expect(button).toHaveAttribute('aria-disabled', 'true')
      expect(button).toHaveAccessibleDescription('Built into Docker')
      await s.user.click(button)
    }
    expect(screen.getAllByText('Built into Docker')).toHaveLength(4)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(remove).not.toHaveBeenCalled()
  })

  it('is disabled with the container count for networks in use', async () => {
    const s = setup()
    const remove = vi.spyOn(api, 'removeNetwork')
    await screen.findByRole('table', { name: 'Networks in dev' })
    const many = screen.getByRole('button', { name: 'Remove synthetic-app' })
    expect(many).toHaveAccessibleDescription('2 containers attached')
    expect(screen.getByText('2 containers attached')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Remove synthetic-one' })).toHaveAccessibleDescription('1 container attached')
    await s.user.click(many)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(remove).not.toHaveBeenCalled()
  })

  it('asks a plain confirmation that names the network, then removes', async () => {
    const s = setup()
    const remove = vi.spyOn(api, 'removeNetwork').mockResolvedValue(undefined)
    await screen.findByRole('table', { name: 'Networks in dev' })
    await s.user.click(screen.getByRole('button', { name: 'Remove synthetic-old' }))
    const d = within(await screen.findByRole('alertdialog'))
    expect(d.getByRole('heading', { name: 'Remove network synthetic-old?' })).toBeInTheDocument()
    expect(d.queryByRole('textbox')).not.toBeInTheDocument()
    await waitFor(() => expect(d.getByRole('button', { name: 'Cancel' })).toHaveFocus())
    await expectNoA11yViolations(document.body)
    expect(remove).not.toHaveBeenCalled()
    await s.user.click(d.getByRole('button', { name: 'Remove network' }))
    await waitFor(() => expect(remove).toHaveBeenCalledTimes(1))
    expect(remove).toHaveBeenCalledWith('dev', 'synthetic-old')
    expect(await screen.findByText('Removed network synthetic-old')).toBeInTheDocument()
    refreshed(s.invalidate)
  })

  it('sends nothing when cancelled', async () => {
    const s = setup()
    const remove = vi.spyOn(api, 'removeNetwork')
    await screen.findByRole('table', { name: 'Networks in dev' })
    await s.user.click(screen.getByRole('button', { name: 'Remove synthetic-old' }))
    await s.user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Cancel' }))
    expect(remove).not.toHaveBeenCalled()
  })

  it('shows a readable error and refreshes when the server answers 409', async () => {
    const s = setup()
    vi.spyOn(api, 'removeNetwork').mockRejectedValue(new ApiError(409, 'conflict', 'raw', { name: 'synthetic-old', containers: 1 }))
    await screen.findByRole('table', { name: 'Networks in dev' })
    await s.user.click(screen.getByRole('button', { name: 'Remove synthetic-old' }))
    await s.user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Remove network' }))
    expect(await screen.findByText('Could not remove network synthetic-old')).toBeInTheDocument()
    expect(screen.getByText(/in use or changed since the list loaded/)).toBeInTheDocument()
    expect(screen.queryByText('raw')).not.toBeInTheDocument()
    await waitFor(() => refreshed(s.invalidate))
  })
})

describe('prune networks', () => {
  const result: NetworkPruneResult = {
    removed: ['synthetic-old', 'synthetic-scratch'],
    removedCount: 2,
    truncated: false,
    resultComplete: true,
  }

  it('lists the unused, non-built-in networks, notes the lower bound, and sends nothing until confirmed', async () => {
    const s = setup()
    const prune = vi.spyOn(api, 'pruneNetworks').mockResolvedValue(result)
    await screen.findByRole('table', { name: 'Networks in dev' })
    await s.user.click(screen.getByRole('button', { name: 'Prune unused' }))
    const d = within(await screen.findByRole('alertdialog'))
    expect(d.getByRole('heading', { name: 'Prune 2 unused networks?' })).toBeInTheDocument()
    const items = within(d.getByRole('list', { name: 'Affected items' })).getAllByRole('listitem')
    expect(items.map((i) => i.textContent)).toEqual(['synthetic-old', 'synthetic-scratch'])
    expect(d.getByText(/can also remove networks that only stopped containers use/)).toBeInTheDocument()
    expect(d.getByText(/the list is a minimum/)).toBeInTheDocument()
    expect(d.getByText(/cannot be undone/)).toBeInTheDocument()
    expect(d.queryByRole('textbox')).not.toBeInTheDocument()
    await waitFor(() => expect(d.getByRole('button', { name: 'Cancel' })).toHaveFocus())
    await expectNoA11yViolations(document.body)
    expect(prune).not.toHaveBeenCalled()
    await s.user.click(d.getByRole('button', { name: 'Prune networks' }))
    await waitFor(() => expect(prune).toHaveBeenCalledTimes(1))
    // The function itself sends the fixed confirmation text; the UI passes only the profile.
    expect(prune).toHaveBeenCalledWith('dev')
    expect(PRUNE_NETWORKS_CONFIRMATION).toBe('delete-unused-networks')
    expect(await screen.findByText('Removed 2 networks')).toBeInTheDocument()
    refreshed(s.invalidate)
  })

  it('sends nothing when cancelled', async () => {
    const s = setup()
    const prune = vi.spyOn(api, 'pruneNetworks')
    await screen.findByRole('table', { name: 'Networks in dev' })
    await s.user.click(screen.getByRole('button', { name: 'Prune unused' }))
    await s.user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Cancel' }))
    expect(prune).not.toHaveBeenCalled()
  })

  it('is disabled with the reason when no network qualifies', async () => {
    const s = setup([networks[0], networks[1], networks[3]])
    await screen.findByRole('table', { name: 'Networks in dev' })
    const button = screen.getByRole('button', { name: 'Prune unused' })
    expect(button).toHaveAttribute('aria-disabled', 'true')
    expect(button).toHaveAccessibleDescription('No unused networks')
    await s.user.click(button)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  it('shows an error toast when the prune fails', async () => {
    const s = setup()
    vi.spyOn(api, 'pruneNetworks').mockRejectedValue(new ApiError(502, 'docker_unavailable', 'x'))
    await screen.findByRole('table', { name: 'Networks in dev' })
    await s.user.click(screen.getByRole('button', { name: 'Prune unused' }))
    await s.user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Prune networks' }))
    expect(await screen.findByText('Could not prune networks')).toBeInTheDocument()
  })

  it('shows the honest toast when the result is unreadable', async () => {
    const s = setup()
    vi.spyOn(api, 'pruneNetworks').mockResolvedValue({ removed: [], removedCount: null, truncated: false, resultComplete: false })
    await screen.findByRole('table', { name: 'Networks in dev' })
    await s.user.click(screen.getByRole('button', { name: 'Prune unused' }))
    await s.user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Prune networks' }))
    expect(await screen.findByText(/Prune ran and some networks were removed\. The exact result is unknown/)).toBeInTheDocument()
  })

  it('describes every kind of result honestly', () => {
    expect(networkPruneTitle(result)).toBe('Removed 2 networks')
    expect(networkPruneTitle({ ...result, removedCount: 1, removed: ['a'] })).toBe('Removed 1 network')
    expect(networkPruneTitle({ ...result, removedCount: 0, removed: [] })).toBe('No unused networks were removed.')
    expect(networkPruneTitle({ ...result, removedCount: 1200, truncated: true })).toBe(
      'Removed 1200 networks (not every name is listed)',
    )
    expect(networkPruneTitle({ removed: [], removedCount: null, truncated: false, resultComplete: false })).toBe(
      'Prune ran and some networks were removed. The exact result is unknown, refresh the list to see what is left.',
    )
    expect(networkPruneTitle({ removed: [], removedCount: 3, truncated: false, resultComplete: false })).toMatch(/exact result is unknown/)
  })
})

describe('stopped profile', () => {
  it('disables the toolbar with one visible reason and makes no request', async () => {
    const s = setup(networks, 'stopped')
    expect(await screen.findByText('Start the profile to see its networks.')).toBeInTheDocument()
    expect(screen.getByText('Start the profile to use these actions.')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Create network' })).toHaveAttribute('aria-disabled', 'true')
    expect(s.getNetworks).not.toHaveBeenCalled()
  })
})
