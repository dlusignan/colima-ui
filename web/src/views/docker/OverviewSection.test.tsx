import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import type { EngineInfo, Profile } from '@/lib/api/types'
import { expectNoA11yViolations } from '@/test/axe'
import { profileFixture } from '@/test/fixtures'
import { createTestClient } from '@/test/queryWrapper'
import { ToastProvider } from '@/lib/toast/ToastProvider'
import { DockerView } from './DockerView'

afterEach(() => vi.restoreAllMocks())

const engine: EngineInfo = {
  version: '27.0.0',
  apiVersion: '1.46',
  os: 'linux',
  arch: 'aarch64',
  cpus: 4,
  memoryBytes: 8 * 1024 ** 3,
  containers: { total: 5, running: 3, paused: 0, stopped: 2 },
  images: 7,
  diskUsage: {
    imagesBytes: 2 * 1024 ** 3,
    containersBytes: 1024 ** 2,
    volumesBytes: 512 * 1024 ** 2,
    buildCacheBytes: 0,
    layersBytes: 1024 ** 3,
  },
}

function setup(profile: Profile = profileFixture({ name: 'dev', status: 'running' })) {
  vi.spyOn(api, 'listProfiles').mockResolvedValue({ profiles: [profile] })
  vi.spyOn(api, 'getImages').mockResolvedValue({ images: [] })
  vi.spyOn(api, 'getVolumes').mockResolvedValue({ volumes: [] })
  vi.spyOn(api, 'getNetworks').mockResolvedValue({ networks: [] })
  const getEngine = vi.spyOn(api, 'getEngine').mockResolvedValue(engine)
  const view = render(
    <QueryClientProvider client={createTestClient()}>
      <MemoryRouter initialEntries={['/profiles/dev/docker']}>
        <ToastProvider>
          <Routes>
            <Route path="/profiles/:name/docker" element={<DockerView />} />
          </Routes>
        </ToastProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { getEngine, user: userEvent.setup(), ...view }
}

describe('overview section', () => {
  it('renders every engine fact, the bar and the legend, and passes axe', async () => {
    const { getEngine, container } = setup()
    await screen.findByText('27.0.0')
    const section = screen.getByRole('heading', { name: 'Overview' }).closest('section')!
    const facts = section.querySelector('dl')!
    const fact = (label: string) => within(facts).getByText(label).nextElementSibling
    expect(fact('Docker version')).toHaveTextContent('27.0.0')
    expect(fact('API version')).toHaveTextContent('1.46')
    expect(fact('OS / architecture')).toHaveTextContent('linux / aarch64')
    expect(fact('Containers')).toHaveTextContent('3 running, 2 stopped')
    expect(fact('Images')).toHaveTextContent('7')
    expect(fact('Engine CPUs')).toHaveTextContent('4')
    expect(fact('Engine memory')).toHaveTextContent('8 GiB')
    const legend = within(section).getByRole('list')
    expect(within(legend).getByText('Images').nextElementSibling).toHaveTextContent('2 GiB')
    expect(within(legend).getByText('Containers').nextElementSibling).toHaveTextContent('1 MiB')
    expect(within(legend).getByText('Volumes').nextElementSibling).toHaveTextContent('512 MiB')
    expect(within(legend).getByText('Build cache').nextElementSibling).toHaveTextContent('0 B')
    expect(within(section).getByText('Total 2.5 GiB')).toBeInTheDocument()
    expect(
      within(section).getByRole('img', {
        name: 'Docker disk usage: Images 2 GiB, Containers 1 MiB, Volumes 512 MiB, Build cache 0 B. Total 2.5 GiB.',
      }),
    ).toBeInTheDocument()
    expect(getEngine).toHaveBeenCalledWith('dev', expect.anything())
    await expectNoA11yViolations(container)
  })

  it('shows the paused count only when there are paused containers', async () => {
    const { getEngine } = setup()
    getEngine.mockResolvedValue({ ...engine, containers: { total: 4, running: 1, paused: 1, stopped: 2 } })
    expect(await screen.findByText('1 running, 1 paused, 2 stopped')).toBeInTheDocument()
  })

  it('asks to start a stopped profile and makes no request', async () => {
    const { getEngine } = setup(profileFixture({ name: 'dev', status: 'stopped' }))
    expect(await screen.findByText('Start the profile to see its Docker overview.')).toBeInTheDocument()
    expect(getEngine).not.toHaveBeenCalled()
  })

  it('shows a loading message', async () => {
    const { getEngine } = setup()
    getEngine.mockReturnValue(new Promise(() => {}))
    expect(await screen.findByText('Loading overview…')).toBeInTheDocument()
  })

  it('shows a note, not an error, for a profile that does not use Docker', async () => {
    const { getEngine } = setup()
    getEngine.mockRejectedValue(new ApiError(409, 'invalid_state', 'x', { runtime: 'not_docker' }))
    expect(await screen.findByText('This profile does not use Docker.')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('shows a retryable error', async () => {
    const { getEngine, user } = setup()
    getEngine.mockRejectedValueOnce(new ApiError(500, 'internal', 'x'))
    getEngine.mockResolvedValue(engine)
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('The overview could not be loaded.')
    await user.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('27.0.0')).toBeInTheDocument()
  })

  it('explains docker_unavailable without a Retry button', async () => {
    const { getEngine } = setup()
    getEngine.mockRejectedValue(new ApiError(503, 'docker_unavailable', 'x'))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Docker is not reachable in this VM yet.')
    expect(within(alert).queryByRole('button')).not.toBeInTheDocument()
  })

  it('renders an all-zero disk usage without error', async () => {
    const { getEngine } = setup()
    getEngine.mockResolvedValue({
      ...engine,
      containers: { total: 0, running: 0, paused: 0, stopped: 0 },
      diskUsage: { imagesBytes: 0, containersBytes: 0, volumesBytes: 0, buildCacheBytes: 0, layersBytes: 0 },
    })
    expect(await screen.findByText('Total 0 B')).toBeInTheDocument()
    expect(screen.getByText('None')).toBeInTheDocument()
  })
})
