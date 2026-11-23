import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { api } from '@/lib/api/endpoints'
import type { DockerImage, Profile } from '@/lib/api/types'
import { expectNoA11yViolations } from '@/test/axe'
import { profileFixture } from '@/test/fixtures'
import { createTestClient } from '@/test/queryWrapper'
import { DockerView } from './DockerView'

afterEach(() => vi.restoreAllMocks())

const images: DockerImage[] = [
  { id: 'sha256:aaaaaaaaaaaabbbbbbbb', tags: ['synthetic/web:1', 'synthetic/web:latest'], sizeBytes: 5 * 1024 ** 2, createdAt: '2026-01-01T00:00:00Z', containers: 2 },
  { id: 'sha256:cccccccccccc1111', tags: [], sizeBytes: 1024 ** 3, createdAt: '2026-01-02T00:00:00Z', containers: 0 },
]

function setup(profile: Profile = profileFixture({ name: 'dev', status: 'running' })) {
  vi.spyOn(api, 'listProfiles').mockResolvedValue({ profiles: [profile] })
  const getImages = vi.spyOn(api, 'getImages')
  const view = render(
    <QueryClientProvider client={createTestClient()}>
      <MemoryRouter initialEntries={['/profiles/dev/docker']}>
        <Routes>
          <Route path="/profiles/:name/docker" element={<DockerView />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { getImages, user: userEvent.setup(), ...view }
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
