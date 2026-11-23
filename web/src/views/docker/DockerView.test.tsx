import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import type { DockerImage, DockerNetwork, DockerVolume, Profile } from '@/lib/api/types'
import { expectNoA11yViolations } from '@/test/axe'
import { profileFixture } from '@/test/fixtures'
import { createTestClient } from '@/test/queryWrapper'
import { DockerView } from './DockerView'

afterEach(() => vi.restoreAllMocks())

const images: DockerImage[] = [
  { id: 'sha256:aaaaaaaaaaaabbbbbbbb', tags: ['synthetic/web:1', 'synthetic/web:latest'], sizeBytes: 5 * 1024 ** 2, createdAt: '2026-01-01T00:00:00Z', containers: 2 },
  { id: 'sha256:cccccccccccc1111', tags: [], sizeBytes: 1024 ** 3, createdAt: '2026-01-02T00:00:00Z', containers: 0 },
]

const volumes: DockerVolume[] = [
  { name: 'synthetic-data', driver: 'local', createdAt: '2026-01-01T00:00:00Z', sizeBytes: 2 * 1024 ** 2, containers: 1 },
  { name: 'synthetic-cache', driver: 'local', createdAt: '2026-01-02T00:00:00Z', sizeBytes: null, containers: null },
  { name: 'synthetic-old', driver: 'local', createdAt: '2026-01-03T00:00:00Z', sizeBytes: 0, containers: 0 },
]

const networks: DockerNetwork[] = [
  { id: 'n1', name: 'bridge', driver: 'bridge', scope: 'local', internal: false, containers: 3 },
  { id: 'n2', name: 'synthetic-private', driver: 'bridge', scope: 'local', internal: true, containers: 0 },
]

function setup(profile: Profile = profileFixture({ name: 'dev', status: 'running' })) {
  vi.spyOn(api, 'listProfiles').mockResolvedValue({ profiles: [profile] })
  const getImages = vi.spyOn(api, 'getImages').mockResolvedValue({ images })
  const getVolumes = vi.spyOn(api, 'getVolumes').mockResolvedValue({ volumes })
  const getNetworks = vi.spyOn(api, 'getNetworks').mockResolvedValue({ networks })
  const view = render(
    <QueryClientProvider client={createTestClient()}>
      <MemoryRouter initialEntries={['/profiles/dev/docker']}>
        <Routes>
          <Route path="/profiles/:name/docker" element={<DockerView />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { getImages, getVolumes, getNetworks, user: userEvent.setup(), ...view }
}

describe('images section', () => {
  it('renders tagged, untagged and unused images, and passes axe', async () => {
    const { getImages, container } = setup()
    getImages.mockResolvedValue({ images })
    const table = await screen.findByRole('table', { name: 'Images in dev' })
    expect(within(table).getByText('synthetic/web:1')).toBeInTheDocument()
    expect(within(table).getByText('synthetic/web:latest')).toBeInTheDocument()
    expect(within(table).getByText('<none>')).toBeInTheDocument()
    expect(within(table).getByText('aaaaaaaaaaaa')).toBeInTheDocument()
    expect(within(table).getByText('5 MiB')).toBeInTheDocument()
    expect(within(table).getByText('Unused')).toBeInTheDocument()
    expect(within(table).getByText('2')).toBeInTheDocument()
    expect(within(table).getAllByText(/ago$/)[0]).toHaveAttribute('title')
    expect(getImages).toHaveBeenCalledWith('dev', expect.anything())
    await expectNoA11yViolations(container)
  })

  it('asks to start a stopped profile and makes no request', async () => {
    const { getImages } = setup(profileFixture({ name: 'dev', status: 'stopped' }))
    expect(await screen.findByText('Start the profile to see its images.')).toBeInTheDocument()
    expect(getImages).not.toHaveBeenCalled()
  })

  it('shows a loading message', async () => {
    const { getImages } = setup()
    getImages.mockReturnValue(new Promise(() => {}))
    expect(await screen.findByText('Loading images…')).toBeInTheDocument()
  })

  it('shows a retryable error', async () => {
    const { getImages, user } = setup()
    getImages.mockRejectedValueOnce(new ApiError(500, 'internal', 'x'))
    getImages.mockResolvedValue({ images })
    expect(await screen.findByRole('alert')).toHaveTextContent('Images could not be loaded.')
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('table', { name: 'Images in dev' })).toBeInTheDocument()
  })

  it('explains docker_unavailable', async () => {
    const { getImages } = setup()
    getImages.mockRejectedValue(new ApiError(503, 'docker_unavailable', 'x'))
    expect(await screen.findByRole('alert')).toHaveTextContent('Docker is not reachable in this VM yet.')
  })

  it('shows an empty message', async () => {
    const { getImages } = setup()
    getImages.mockResolvedValue({ images: [] })
    expect(await screen.findByText('No images.')).toBeInTheDocument()
  })
})

describe('volumes section', () => {
  it('renders sizes, unknowns and unused volumes with scoped headers, and passes axe', async () => {
    const { getVolumes, container } = setup()
    const table = await screen.findByRole('table', { name: 'Volumes in dev' })
    expect(within(table).getByRole('rowheader', { name: 'synthetic-data' })).toBeInTheDocument()
    expect(within(table).getByText('2 MiB')).toBeInTheDocument()
    const cache = within(table).getByRole('rowheader', { name: 'synthetic-cache' }).closest('tr')!
    expect(within(cache).getAllByText('–')).toHaveLength(2)
    expect(within(table).getByText('Unused')).toBeInTheDocument()
    expect(within(table).getAllByText(/ago$/)[0]).toHaveAttribute('title')
    for (const h of within(table).getAllByRole('columnheader')) expect(h).toHaveAttribute('scope', 'col')
    expect(getVolumes).toHaveBeenCalledWith('dev', expect.anything())
    await screen.findByRole('table', { name: 'Networks in dev' })
    await expectNoA11yViolations(container)
  })

  it('has a heading outline of Docker, Images, Volumes, Networks', async () => {
    setup()
    await screen.findByRole('table', { name: 'Networks in dev' })
    expect(screen.getAllByRole('heading').map((h) => [h.tagName, h.textContent])).toEqual([
      ['H2', 'Docker: dev'],
      ['H3', 'Images'],
      ['H3', 'Volumes'],
      ['H3', 'Networks'],
    ])
  })

  it('shows an error in the volumes section only, and retries', async () => {
    const { getVolumes, user } = setup()
    getVolumes.mockReset()
    getVolumes.mockRejectedValueOnce(new ApiError(500, 'internal', 'x'))
    getVolumes.mockResolvedValue({ volumes })
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Volumes could not be loaded.')
    expect(await screen.findByRole('table', { name: 'Images in dev' })).toBeInTheDocument()
    expect(await screen.findByRole('table', { name: 'Networks in dev' })).toBeInTheDocument()
    await user.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('table', { name: 'Volumes in dev' })).toBeInTheDocument()
  })

  it('shows loading and empty states', async () => {
    const first = setup()
    first.getVolumes.mockReturnValue(new Promise(() => {}))
    expect(await screen.findByText('Loading volumes…')).toBeInTheDocument()
    first.unmount()
    const second = setup()
    second.getVolumes.mockResolvedValue({ volumes: [] })
    expect(await screen.findByText('No volumes.')).toBeInTheDocument()
  })

  it('asks to start a stopped profile and makes no volumes request', async () => {
    const { getVolumes } = setup(profileFixture({ name: 'dev', status: 'stopped' }))
    expect(await screen.findByText('Start the profile to see its volumes.')).toBeInTheDocument()
    expect(getVolumes).not.toHaveBeenCalled()
  })
})

describe('networks section', () => {
  it('renders Internal as text and notes that stopped containers count', async () => {
    setup()
    const table = await screen.findByRole('table', { name: 'Networks in dev' })
    const priv = within(table).getByRole('rowheader', { name: 'synthetic-private' }).closest('tr')!
    expect(within(priv).getByText('Yes')).toBeInTheDocument()
    const bridge = within(table).getByRole('rowheader', { name: 'bridge' }).closest('tr')!
    expect(within(bridge).getByText('No')).toBeInTheDocument()
    expect(within(bridge).getByText('3')).toBeInTheDocument()
    expect(screen.getByText('The container count includes stopped containers.')).toBeInTheDocument()
    for (const h of within(table).getAllByRole('columnheader')) expect(h).toHaveAttribute('scope', 'col')
  })

  it('shows an error in the networks section only, and retries', async () => {
    const { getNetworks, user } = setup()
    getNetworks.mockReset()
    getNetworks.mockRejectedValueOnce(new ApiError(500, 'internal', 'x'))
    getNetworks.mockResolvedValue({ networks })
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Networks could not be loaded.')
    expect(await screen.findByRole('table', { name: 'Images in dev' })).toBeInTheDocument()
    expect(await screen.findByRole('table', { name: 'Volumes in dev' })).toBeInTheDocument()
    await user.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('table', { name: 'Networks in dev' })).toBeInTheDocument()
  })

  it('shows loading and empty states', async () => {
    const first = setup()
    first.getNetworks.mockReturnValue(new Promise(() => {}))
    expect(await screen.findByText('Loading networks…')).toBeInTheDocument()
    first.unmount()
    const second = setup()
    second.getNetworks.mockResolvedValue({ networks: [] })
    expect(await screen.findByText('No networks.')).toBeInTheDocument()
  })

  it('asks to start a stopped profile and makes no networks request', async () => {
    const { getNetworks } = setup(profileFixture({ name: 'dev', status: 'stopped' }))
    expect(await screen.findByText('Start the profile to see its networks.')).toBeInTheDocument()
    expect(getNetworks).not.toHaveBeenCalled()
  })
})
