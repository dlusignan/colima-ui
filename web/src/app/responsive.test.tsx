import { render, screen } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { api } from '@/lib/api/endpoints'
import { profileFixture } from '@/test/fixtures'
import { createTestClient } from '@/test/queryWrapper'
import { ContainersPanel } from '@/views/profile/ContainersPanel'
import { ToastProvider } from '@/lib/toast/ToastProvider'
import { Layout } from './Layout'

afterEach(() => vi.restoreAllMocks())

describe('responsive layout', () => {
  it('lets the page span the window: no width cap, fluid padding', () => {
    const { container } = render(
      <QueryClientProvider client={createTestClient()}>
        <MemoryRouter>
          <Layout />
        </MemoryRouter>
      </QueryClientProvider>,
    )
    const main = container.querySelector('main')
    expect(main?.className).not.toMatch(/max-w-/)
    expect(main?.className).not.toContain('mx-auto')
    expect(main?.className).toContain('p-4')
    expect(main?.className).toContain('sm:p-6')
    expect(container.querySelector('header')?.className).toContain('flex-wrap')
  })

  it('hides lower-priority container columns progressively and keeps Actions visible', async () => {
    vi.spyOn(api, 'getContainers').mockResolvedValue({
      containers: [
        {
          id: 'aaaaaaaaaaaa',
          fullId: 'a'.repeat(64),
          name: 'synthetic-application-main-api-1',
          image: 'synthetic/long:4.0',
          state: 'running',
          status: 'Up 2 hours',
          cpuPercent: 1,
          memoryBytes: 1024,
          createdAt: '2026-01-01T00:00:00Z',
          ports: [],
        },
      ],
    })
    render(
      <QueryClientProvider client={createTestClient()}>
        <MemoryRouter>
          <ToastProvider>
            <ContainersPanel profile={profileFixture({ name: 'dev', status: 'running', runtime: 'docker' })} />
          </ToastProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    )
    const header = (name: string) => screen.findByRole('columnheader', { name, hidden: true })
    expect((await header('Created')).className).toContain('hidden lg:table-cell')
    expect((await header('Ports')).className).toContain('hidden md:table-cell')
    expect((await header('CPU')).className).toContain('hidden sm:table-cell')
    expect((await header('Memory')).className).toContain('hidden sm:table-cell')
    for (const name of ['Name', 'Image', 'Status', 'Logs', 'Actions']) {
      expect((await header(name)).className).not.toContain('hidden')
    }
    const row = await screen.findByRole('rowheader', { name: 'synthetic-application-main-api-1' })
    expect(row.className).toContain('break-words')
    expect(row.getAttribute('title')).toBe('synthetic-application-main-api-1')
    expect(screen.getByRole('region', { name: 'Containers table' }).className).toContain('overflow-x-auto')
  })

  it('keeps a side gutter on phone-width dialogs', () => {
    render(
      <Dialog open>
        <DialogContent>
          <DialogTitle>Synthetic</DialogTitle>
        </DialogContent>
      </Dialog>,
    )
    const popup = screen.getByRole('dialog')
    expect(popup.className).toContain('w-[calc(100%-2rem)]')
    expect(popup.className).toContain('max-w-lg')
  })
})
