import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi, type MockInstance } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { api, PRUNE_VOLUMES_CONFIRMATION } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import type { DockerVolume, VolumeDetail, VolumePruneResult } from '@/lib/api/types'
import { Toaster } from '@/lib/toast/Toaster'
import { ToastProvider } from '@/lib/toast/ToastProvider'
import { expectNoA11yViolations } from '@/test/axe'
import { profileFixture } from '@/test/fixtures'
import { createTestClient } from '@/test/queryWrapper'
import { VolumesSection } from './VolumesSection'
import { pruneTitle } from './volumeText'

afterEach(() => vi.restoreAllMocks())

const volumes: DockerVolume[] = [
  { name: 'synthetic-data', driver: 'local', createdAt: '2026-01-01T00:00:00Z', sizeBytes: 2 * 1024 ** 2, containers: 2 },
  { name: 'synthetic-cache', driver: 'local', createdAt: '2026-01-02T00:00:00Z', sizeBytes: null, containers: null },
  { name: 'synthetic-old', driver: 'local', createdAt: '2026-01-03T00:00:00Z', sizeBytes: 1024 ** 2, containers: 0 },
  { name: 'synthetic-scratch', driver: 'local', createdAt: '2026-01-04T00:00:00Z', sizeBytes: 3 * 1024 ** 2, containers: 0 },
]

const detail: VolumeDetail = {
  name: 'synthetic-data',
  driver: 'local',
  createdAt: '2026-01-01T00:00:00Z',
  sizeBytes: 2 * 1024 ** 2,
  labels: { team: 'backend', note: '<b>not bold</b>' },
  containers: 2,
  containerNames: ['synthetic-db', 'synthetic-web'],
}

function setup(list: DockerVolume[] = volumes, status: 'running' | 'stopped' = 'running') {
  const getVolumes = vi.spyOn(api, 'getVolumes').mockResolvedValue({ volumes: list })
  const client = createTestClient()
  const invalidate = vi.spyOn(client, 'invalidateQueries')
  const view = render(
    <QueryClientProvider client={client}>
      <ToastProvider>
        <VolumesSection profile={profileFixture({ name: 'dev', status })} />
        <Toaster />
      </ToastProvider>
    </QueryClientProvider>,
  )
  return { getVolumes, invalidate, user: userEvent.setup(), ...view }
}

const dialog = () => screen.getByRole('dialog')
const refreshed = (invalidate: MockInstance) => {
  const keys = invalidate.mock.calls.map((c: unknown[]) => JSON.stringify((c[0] as { queryKey: unknown }).queryKey))
  expect(keys).toContain(JSON.stringify(queryKeys.volumes('dev')))
  expect(keys).toContain(JSON.stringify(queryKeys.engine('dev')))
}

describe('create volume', () => {
  async function open(s: ReturnType<typeof setup>) {
    await screen.findByRole('table', { name: 'Volumes in dev' })
    await s.user.click(screen.getByRole('button', { name: 'Create volume' }))
    return within(await screen.findByRole('dialog'))
  }

  it('sends exactly { name } and refreshes the list and the engine', async () => {
    const s = setup()
    const create = vi.spyOn(api, 'createVolume').mockResolvedValue({ ...volumes[0], name: 'new-vol' })
    const d = await open(s)
    await s.user.type(d.getByLabelText('Name'), 'new-vol')
    await s.user.click(d.getByRole('button', { name: 'Create volume' }))
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1))
    expect(create.mock.calls[0]).toEqual(['dev', { name: 'new-vol' }])
    expect(JSON.stringify(create.mock.calls[0][1])).toBe('{"name":"new-vol"}')
    expect(await screen.findByText('Created volume new-vol')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    refreshed(s.invalidate)
  })

  it('sends labels, dropping blank rows', async () => {
    const s = setup()
    const create = vi.spyOn(api, 'createVolume').mockResolvedValue(volumes[0])
    const d = await open(s)
    await s.user.type(d.getByLabelText('Name'), 'with-labels')
    await s.user.click(d.getByRole('button', { name: 'Add label' }))
    await s.user.type(d.getByLabelText('Label 1 key'), 'team')
    await s.user.type(d.getByLabelText('Label 1 value'), 'backend')
    await s.user.click(d.getByRole('button', { name: 'Add label' }))
    await s.user.click(d.getByRole('button', { name: 'Add label' }))
    await s.user.type(d.getByLabelText('Label 3 key'), 'empty')
    await s.user.click(d.getByRole('button', { name: 'Remove label 2' }))
    await s.user.click(d.getByRole('button', { name: 'Create volume' }))
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1))
    expect(JSON.stringify(create.mock.calls[0][1])).toBe('{"name":"with-labels","labels":{"team":"backend","empty":""}}')
  })

  it.each([
    ['a name with a space', 'bad name', undefined, undefined, /Use letters, digits/],
    ['an empty name', '', undefined, undefined, 'Enter a name.'],
    ['a name that starts with a dot', '.hidden', undefined, undefined, /Use letters, digits/],
    ['a name over 128 characters', 'a'.repeat(129), undefined, undefined, 'Use at most 128 characters.'],
    ['a reserved key prefix', 'ok', 'COM.Docker.x', 'v', /prefix is reserved/],
    ['a key with a bad character', 'ok', 'a b', 'v', /Use letters, digits/],
    ['a key over 63 characters', 'ok', 'k'.repeat(64), 'v', 'Key 1: Use at most 63 characters.'],
    ['a value over 256 bytes', 'ok', 'k', 'é'.repeat(129), 'Value 1: Use at most 256 bytes.'],
    ['a value with a control character', 'ok', 'k', 'a\u0007b', /control and invisible/],
    ['a value with a format character', 'ok', 'k', 'a​b', /control and invisible/],
  ])('blocks %s and sends nothing', async (_n, name, key, value, message) => {
    const s = setup()
    const create = vi.spyOn(api, 'createVolume')
    const d = await open(s)
    if (name) await s.user.type(d.getByLabelText('Name'), name)
    if (key !== undefined) {
      await s.user.click(d.getByRole('button', { name: 'Add label' }))
      await s.user.click(d.getByLabelText('Label 1 key'))
      await s.user.paste(key)
      await s.user.click(d.getByLabelText('Label 1 value'))
      await s.user.paste(value ?? '')
    }
    await s.user.click(d.getByRole('button', { name: 'Create volume' }))
    expect(await d.findByText(message)).toBeInTheDocument()
    expect(create).not.toHaveBeenCalled()
  })

  it('refuses a case-insensitive duplicate key and stops adding at 16 rows', async () => {
    const s = setup()
    const create = vi.spyOn(api, 'createVolume')
    const d = await open(s)
    await s.user.type(d.getByLabelText('Name'), 'ok')
    await s.user.click(d.getByRole('button', { name: 'Add label' }))
    await s.user.click(d.getByRole('button', { name: 'Add label' }))
    await s.user.type(d.getByLabelText('Label 1 key'), 'Team')
    await s.user.type(d.getByLabelText('Label 2 key'), 'team')
    await s.user.click(d.getByRole('button', { name: 'Create volume' }))
    expect(await d.findByText(/Key 2: This key is already used/)).toBeInTheDocument()
    expect(create).not.toHaveBeenCalled()
    for (let i = 0; i < 14; i++) await s.user.click(d.getByRole('button', { name: 'Add label' }))
    expect(d.getByRole('button', { name: 'Add label' })).toBeDisabled()
    expect(d.getByText('Limit of 16 labels reached.')).toBeInTheDocument()
  })

  it('shows a 409 next to the name and keeps the dialog open without a toast', async () => {
    const s = setup()
    vi.spyOn(api, 'createVolume').mockRejectedValue(new ApiError(409, 'conflict', 'x', { name: 'dup' }))
    const d = await open(s)
    await s.user.type(d.getByLabelText('Name'), 'dup')
    await s.user.click(d.getByRole('button', { name: 'Create volume' }))
    expect(await d.findByText('A volume with this name already exists.')).toBeInTheDocument()
    expect(d.getByLabelText('Name')).toHaveAttribute('aria-invalid', 'true')
    expect(d.getByLabelText('Name')).toHaveAccessibleDescription('A volume with this name already exists.')
    expect(screen.queryByText('Could not create volume dup')).not.toBeInTheDocument()
    await waitFor(() => refreshed(s.invalidate))
  })

  it('shows a 422 labels message in the labels group and a 422 name message on the name', async () => {
    const s = setup()
    const create = vi
      .spyOn(api, 'createVolume')
      .mockRejectedValueOnce(new ApiError(422, 'invalid_request', 'x', { fields: { labels: 'Labels are not valid.' } }))
      .mockRejectedValueOnce(new ApiError(422, 'invalid_request', 'x', { fields: { name: 'Not a valid Docker name.' } }))
    const d = await open(s)
    await s.user.type(d.getByLabelText('Name'), 'ok')
    await s.user.click(d.getByRole('button', { name: 'Add label' }))
    await s.user.type(d.getByLabelText('Label 1 key'), 'k')
    await s.user.click(d.getByRole('button', { name: 'Create volume' }))
    expect(await d.findByText('Labels are not valid.')).toBeInTheDocument()
    await s.user.click(d.getByRole('button', { name: 'Create volume' }))
    expect(await d.findByText('Not a valid Docker name.')).toBeInTheDocument()
    expect(d.queryByText('Labels are not valid.')).not.toBeInTheDocument()
    expect(create).toHaveBeenCalledTimes(2)
  })

  it('toasts any other failure and passes axe with the dialog open', async () => {
    const s = setup()
    vi.spyOn(api, 'createVolume').mockRejectedValue(new ApiError(502, 'docker_unavailable', 'x'))
    const d = await open(s)
    await s.user.click(d.getByRole('button', { name: 'Add label' }))
    await expectNoA11yViolations(document.body)
    await s.user.type(d.getByLabelText('Name'), 'ok')
    await s.user.click(d.getByRole('button', { name: 'Create volume' }))
    expect(await screen.findByText('Could not create volume ok')).toBeInTheDocument()
  })

  it('has focus in the name field and Cancel closes it', async () => {
    const s = setup()
    const d = await open(s)
    await waitFor(() => expect(d.getByLabelText('Name')).toHaveFocus())
    await s.user.click(d.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })
})

describe('inspect volume', () => {
  it('loads once when opened, shows details with labels as text and no host path, and never polls', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const get = vi.spyOn(api, 'getVolume').mockResolvedValue(detail)
      const s = setup()
      await screen.findByRole('table', { name: 'Volumes in dev' })
      expect(get).not.toHaveBeenCalled()
      await s.user.click(screen.getByRole('button', { name: 'Inspect synthetic-data' }))
      const d = within(await screen.findByRole('dialog'))
      expect(await d.findByText('backend')).toBeInTheDocument()
      expect(get).toHaveBeenCalledWith('dev', 'synthetic-data', expect.anything())
      expect(d.getByText('<b>not bold</b>')).toBeInTheDocument()
      expect(dialog().querySelector('b')).toBeNull()
      expect(d.getByText('synthetic-db')).toBeInTheDocument()
      expect(d.getByText('2 containers, running or stopped.')).toBeInTheDocument()
      expect(d.getByText('2 MiB')).toBeInTheDocument()
      expect(dialog().textContent).not.toMatch(/mountpoint|\/var\/lib/i)
      expect(dialog().querySelector('[aria-live]')).toBeNull()
      await expectNoA11yViolations(document.body)
      await vi.advanceTimersByTimeAsync(120_000)
      expect(get).toHaveBeenCalledTimes(1)
    } finally {
      vi.useRealTimers()
    }
  })

  it('shows empty labels and no containers, then an error with Retry', async () => {
    const get = vi
      .spyOn(api, 'getVolume')
      .mockRejectedValueOnce(new ApiError(500, 'internal_error', 'x'))
      .mockResolvedValue({ ...detail, labels: {}, containers: 0, containerNames: [], sizeBytes: null })
    const s = setup()
    await screen.findByRole('table', { name: 'Volumes in dev' })
    await s.user.click(screen.getByRole('button', { name: 'Inspect synthetic-data' }))
    const d = within(await screen.findByRole('dialog'))
    expect(await d.findByRole('alert')).toHaveTextContent('The volume could not be loaded.')
    await s.user.click(d.getByRole('button', { name: 'Retry' }))
    expect(await d.findByText('No labels.')).toBeInTheDocument()
    expect(d.getByText('No container uses this volume.')).toBeInTheDocument()
    expect(d.getByText('Unknown')).toBeInTheDocument()
    expect(get).toHaveBeenCalledTimes(2)
    await s.user.click(d.getByRole('button', { name: 'Close' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('explains a volume that no longer exists', async () => {
    vi.spyOn(api, 'getVolume').mockRejectedValue(new ApiError(404, 'not_found', 'x'))
    const s = setup()
    await screen.findByRole('table', { name: 'Volumes in dev' })
    await s.user.click(screen.getByRole('button', { name: 'Inspect synthetic-old' }))
    expect(await screen.findByText(/no longer exists/)).toBeInTheDocument()
  })
})

describe('remove volume', () => {
  it('is disabled with the visible reason for in-use and unknown volumes', async () => {
    const s = setup()
    const remove = vi.spyOn(api, 'removeVolume')
    await screen.findByRole('table', { name: 'Volumes in dev' })
    const inUse = screen.getByRole('button', { name: 'Remove synthetic-data' })
    expect(inUse).toHaveAttribute('aria-disabled', 'true')
    expect(inUse).toHaveAccessibleDescription('In use by 2 containers')
    expect(screen.getByText('In use by 2 containers')).toBeVisible()
    const unknown = screen.getByRole('button', { name: 'Remove synthetic-cache' })
    expect(unknown).toHaveAccessibleDescription('Usage unknown')
    await s.user.click(inUse)
    await s.user.click(unknown)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(remove).not.toHaveBeenCalled()
  })

  it('uses the singular for one container', async () => {
    setup([{ ...volumes[0], containers: 1 }])
    await screen.findByRole('table', { name: 'Volumes in dev' })
    expect(screen.getByText('In use by 1 container')).toBeVisible()
  })

  it('needs the exact name, warns that data is lost, and then removes', async () => {
    const s = setup()
    const remove = vi.spyOn(api, 'removeVolume').mockResolvedValue(undefined)
    await screen.findByRole('table', { name: 'Volumes in dev' })
    await s.user.click(screen.getByRole('button', { name: 'Remove synthetic-old' }))
    const d = within(await screen.findByRole('alertdialog'))
    expect(d.getByText(/deleted\. This cannot be recovered/)).toBeInTheDocument()
    await waitFor(() => expect(d.getByRole('button', { name: 'Cancel' })).toHaveFocus())
    const confirm = d.getByRole('button', { name: 'Remove volume' })
    expect(confirm).toBeDisabled()
    await expectNoA11yViolations(document.body)
    await s.user.type(d.getByLabelText(/Type synthetic-old to confirm/), 'synthetic-ol')
    expect(confirm).toBeDisabled()
    await s.user.type(d.getByLabelText(/Type synthetic-old to confirm/), 'd')
    await s.user.click(confirm)
    await waitFor(() => expect(remove).toHaveBeenCalledTimes(1))
    expect(remove).toHaveBeenCalledWith('dev', 'synthetic-old')
    expect(await screen.findByText('Removed volume synthetic-old')).toBeInTheDocument()
    refreshed(s.invalidate)
  })

  it('shows a readable error and refreshes when the server answers 409', async () => {
    const s = setup()
    vi.spyOn(api, 'removeVolume').mockRejectedValue(new ApiError(409, 'conflict', 'raw', { name: 'synthetic-old' }))
    await screen.findByRole('table', { name: 'Volumes in dev' })
    await s.user.click(screen.getByRole('button', { name: 'Remove synthetic-old' }))
    const d = within(await screen.findByRole('alertdialog'))
    await s.user.type(d.getByLabelText(/Type synthetic-old to confirm/), 'synthetic-old')
    await s.user.click(d.getByRole('button', { name: 'Remove volume' }))
    expect(await screen.findByText('Could not remove volume synthetic-old')).toBeInTheDocument()
    expect(screen.getByText(/in use or changed since the list loaded/)).toBeInTheDocument()
    expect(screen.queryByText('raw')).not.toBeInTheDocument()
    await waitFor(() => refreshed(s.invalidate))
  })
})

describe('prune volumes', () => {
  const result: VolumePruneResult = {
    removed: ['synthetic-old', 'synthetic-scratch'],
    removedCount: 2,
    truncated: false,
    reclaimedBytes: 4 * 1024 ** 2,
    includesNamedVolumes: true,
    resultComplete: true,
  }

  it('lists the unused volumes with sizes and the total, sends nothing until confirmed, and requires "prune"', async () => {
    const s = setup()
    const prune = vi.spyOn(api, 'pruneVolumes').mockResolvedValue(result)
    await screen.findByRole('table', { name: 'Volumes in dev' })
    await s.user.click(screen.getByRole('button', { name: 'Prune unused' }))
    const d = within(await screen.findByRole('alertdialog'))
    expect(d.getByRole('heading', { name: 'Prune 2 unused volumes?' })).toBeInTheDocument()
    const items = within(d.getByRole('list', { name: 'Affected items' })).getAllByRole('listitem')
    expect(items.map((i) => i.textContent)).toEqual(['synthetic-old (1 MiB)', 'synthetic-scratch (3 MiB)'])
    expect(d.getByText(/ALL volumes that no container uses, including volumes you created and named yourself/)).toBeInTheDocument()
    expect(d.getByText(/4 MiB in total/)).toBeInTheDocument()
    expect(d.getByText(/cannot be recovered/)).toBeInTheDocument()
    await waitFor(() => expect(d.getByRole('button', { name: 'Cancel' })).toHaveFocus())
    await expectNoA11yViolations(document.body)
    const confirm = d.getByRole('button', { name: 'Prune volumes' })
    expect(confirm).toBeDisabled()
    await s.user.type(d.getByLabelText(/Type prune to confirm/), 'prun')
    expect(confirm).toBeDisabled()
    expect(prune).not.toHaveBeenCalled()
    await s.user.type(d.getByLabelText(/Type prune to confirm/), 'e')
    await s.user.click(confirm)
    await waitFor(() => expect(prune).toHaveBeenCalledTimes(1))
    // The function itself sends the fixed confirmation text; the UI passes only the profile.
    expect(prune).toHaveBeenCalledWith('dev')
    expect(PRUNE_VOLUMES_CONFIRMATION).toBe('delete-unused-volumes')
    expect(await screen.findByText('Removed 2 volumes, freed 4 MiB')).toBeInTheDocument()
    refreshed(s.invalidate)
  })

  it('sends nothing when cancelled', async () => {
    const s = setup()
    const prune = vi.spyOn(api, 'pruneVolumes')
    await screen.findByRole('table', { name: 'Volumes in dev' })
    await s.user.click(screen.getByRole('button', { name: 'Prune unused' }))
    await s.user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Cancel' }))
    expect(prune).not.toHaveBeenCalled()
  })

  it('is disabled with the reason when no volume is unused', async () => {
    const s = setup([volumes[0], volumes[1]])
    await screen.findByRole('table', { name: 'Volumes in dev' })
    const button = screen.getByRole('button', { name: 'Prune unused' })
    expect(button).toHaveAttribute('aria-disabled', 'true')
    expect(button).toHaveAccessibleDescription('No unused volumes')
    await s.user.click(button)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  it('shows an error toast when the prune fails', async () => {
    const s = setup()
    vi.spyOn(api, 'pruneVolumes').mockRejectedValue(new ApiError(502, 'docker_unavailable', 'x'))
    await screen.findByRole('table', { name: 'Volumes in dev' })
    await s.user.click(screen.getByRole('button', { name: 'Prune unused' }))
    const d = within(await screen.findByRole('alertdialog'))
    await s.user.type(d.getByLabelText(/Type prune to confirm/), 'prune')
    await s.user.click(d.getByRole('button', { name: 'Prune volumes' }))
    expect(await screen.findByText('Could not prune volumes')).toBeInTheDocument()
  })

  it('describes every kind of result honestly', () => {
    expect(pruneTitle(result)).toBe('Removed 2 volumes, freed 4 MiB')
    expect(pruneTitle({ ...result, removedCount: 1, removed: ['a'] })).toBe('Removed 1 volume, freed 4 MiB')
    expect(pruneTitle({ ...result, reclaimedBytes: null })).toBe('Removed 2 volumes')
    expect(pruneTitle({ ...result, removedCount: 0, removed: [], reclaimedBytes: 0 })).toBe('No unused volumes were removed.')
    expect(pruneTitle({ ...result, removedCount: 1200, truncated: true })).toBe(
      'Removed 1200 volumes, freed 4 MiB (not every name is listed)',
    )
    expect(
      pruneTitle({ removed: [], removedCount: null, truncated: false, reclaimedBytes: null, includesNamedVolumes: true, resultComplete: false }),
    ).toBe('Prune ran and some volumes were removed. The exact result is unknown, refresh the list to see what is left.')
  })
})

describe('stopped profile', () => {
  it('disables the toolbar with one visible reason and makes no request', async () => {
    const s = setup(volumes, 'stopped')
    expect(await screen.findByText('Start the profile to see its volumes.')).toBeInTheDocument()
    expect(screen.getByText('Start the profile to use these actions.')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Create volume' })).toHaveAttribute('aria-disabled', 'true')
    expect(s.getVolumes).not.toHaveBeenCalled()
  })
})
