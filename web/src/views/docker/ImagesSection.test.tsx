import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi, type MockInstance } from 'vitest'
import { ApiError } from '@/lib/api/client'
import { api, PRUNE_IMAGES_CONFIRMATION } from '@/lib/api/endpoints'
import { queryKeys } from '@/lib/api/keys'
import type { DockerImage, ImageDetail, ImagePruneResult, ImageRemoveResult } from '@/lib/api/types'
import { Toaster } from '@/lib/toast/Toaster'
import { ToastProvider } from '@/lib/toast/ToastProvider'
import { expectNoA11yViolations } from '@/test/axe'
import { profileFixture } from '@/test/fixtures'
import { createTestClient } from '@/test/queryWrapper'
import { ImagesSection } from './ImagesSection'
import { imagePruneTitle, removeTitle } from './imageText'

afterEach(() => vi.restoreAllMocks())

const MiB = 1024 ** 2
const img = (hex: string, tags: string[], containers: number, sizeBytes: number): DockerImage => ({
  id: `sha256:${hex.repeat(12)}${'0'.repeat(52)}`,
  tags,
  sizeBytes,
  createdAt: '2026-01-01T00:00:00Z',
  containers,
})

const images: DockerImage[] = [
  img('a', ['synthetic/web:1', 'synthetic/web:latest'], 0, 5 * MiB),
  img('b', ['synthetic/api:1'], 2, 7 * MiB),
  img('c', ['registry.example.test:5000/team/app:v1'], 0, 3 * MiB),
  img('d', [], 0, 1 * MiB),
  img('e', [], 1, 2 * MiB),
]

const detail: ImageDetail = {
  id: 'aaaaaaaaaaaa',
  tags: ['synthetic/web:1', 'synthetic/web:latest'],
  digests: ['synthetic/web@sha256:' + '9'.repeat(64)],
  sizeBytes: 5 * MiB,
  createdAt: '2026-01-01T00:00:00Z',
  architecture: 'arm64',
  os: 'linux',
  exposedPorts: [80, 443],
  containers: 1,
  containerNames: ['<b>synthetic-web</b>'],
  layers: [
    { createdAt: '2026-01-01T00:00:00Z', sizeBytes: 4 * MiB },
    { createdAt: null, sizeBytes: 0 },
  ],
  layerCount: 2,
}

const removeResult: ImageRemoveResult = {
  untagged: ['synthetic/web:1'],
  deleted: [],
  removedCount: 1,
  truncated: false,
  resultComplete: true,
}

const pruneResult: ImagePruneResult = {
  untagged: [],
  deleted: ['dddddddddddd'],
  removedCount: 1,
  truncated: false,
  reclaimedBytes: MiB,
  all: false,
  resultComplete: true,
}

function setup(list: DockerImage[] = images, status: 'running' | 'stopped' = 'running') {
  const getImages = vi.spyOn(api, 'getImages').mockResolvedValue({ images: list })
  const client = createTestClient()
  const invalidate = vi.spyOn(client, 'invalidateQueries')
  const view = render(
    <QueryClientProvider client={client}>
      <ToastProvider>
        <ImagesSection profile={profileFixture({ name: 'dev', status })} />
        <Toaster />
      </ToastProvider>
    </QueryClientProvider>,
  )
  return { getImages, invalidate, user: userEvent.setup(), ...view }
}

const table = () => screen.findByRole('table', { name: 'Images in dev' })
const refreshed = (invalidate: MockInstance) => {
  const keys = invalidate.mock.calls.map((c: unknown[]) => JSON.stringify((c[0] as { queryKey: unknown }).queryKey))
  expect(keys).toContain(JSON.stringify(queryKeys.images('dev')))
  expect(keys).toContain(JSON.stringify(queryKeys.engine('dev')))
}

describe('tag image', () => {
  async function open(s: ReturnType<typeof setup>, name = 'Tag synthetic/web:1') {
    await table()
    await s.user.click(screen.getByRole('button', { name }))
    return within(await screen.findByRole('dialog'))
  }

  it('prefills the repository of the first tag, shows the reference live and sends exactly the documented body', async () => {
    const s = setup()
    const tag = vi.spyOn(api, 'tagImage').mockResolvedValue(undefined)
    const d = await open(s)
    expect(d.getByLabelText('Repository')).toHaveValue('synthetic/web')
    expect(d.getByLabelText('Tag')).toHaveValue('')
    await waitFor(() => expect(d.getByLabelText('Tag')).toHaveFocus())
    expect(d.getByText('enter a repository and a tag')).toBeInTheDocument()
    await s.user.type(d.getByLabelText('Tag'), 'v2')
    expect(d.getByText('synthetic/web:v2')).toBeInTheDocument()
    await s.user.click(d.getByRole('button', { name: 'Add tag' }))
    await waitFor(() => expect(tag).toHaveBeenCalledTimes(1))
    expect(tag.mock.calls[0]).toEqual(['dev', 'aaaaaaaaaaaa', { repository: 'synthetic/web', tag: 'v2' }])
    expect(JSON.stringify(tag.mock.calls[0][2])).toBe('{"repository":"synthetic/web","tag":"v2"}')
    expect(await screen.findByText('Tagged image aaaaaaaaaaaa as synthetic/web:v2')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    refreshed(s.invalidate)
  })

  it('keeps a registry host and port out of the tag split and leaves the repository empty for an untagged image', async () => {
    const s = setup()
    const d = await open(s, 'Tag registry.example.test:5000/team/app:v1')
    expect(d.getByLabelText('Repository')).toHaveValue('registry.example.test:5000/team/app')
    await s.user.click(d.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    await s.user.click(screen.getByRole('button', { name: 'Tag image dddddddddddd' }))
    const e = within(await screen.findByRole('dialog'))
    expect(e.getByLabelText('Repository')).toHaveValue('')
    await waitFor(() => expect(e.getByLabelText('Repository')).toHaveFocus())
  })

  it.each([
    ['an empty repository', '', 'v2', 'Enter a repository.'],
    ['a repository with a tag', 'app:v1', 'v2', 'Leave out the tag. Enter it in the Tag field.'],
    ['a repository with a digest', 'app@sha256:' + 'a'.repeat(64), 'v2', /Leave out the digest/],
    ['a repository named sha256', 'sha256', 'v2', /read like an image ID/],
    ['a 64 hex repository', 'a'.repeat(64), 'v2', /read like an image ID/],
    ['an uppercase path', 'Team/App', 'v2', /Use lowercase letters and digits/],
    ['a double slash', 'team//app', 'v2', /Use lowercase letters and digits/],
    ['a bad registry host', 'bad_host.example/app', 'v2', /Use lowercase|registry host/],
    ['a repository over 255 characters', 'a'.repeat(256), 'v2', 'Use at most 255 characters.'],
    ['an empty tag', 'app', '', 'Enter a tag.'],
    ['a tag starting with a dot', 'app', '.v2', /Use letters, digits/],
    ['a tag starting with a hyphen', 'app', '-v2', /Use letters, digits/],
    ['a tag with a space', 'app', 'v 2', /Use letters, digits/],
    ['a tag over 128 characters', 'app', 'v'.repeat(129), 'Use at most 128 characters.'],
    ['a whole reference over 255 characters', 'a'.repeat(250), 'v12345', 'The whole reference is limited to 255 characters.'],
    ['a tag the image already has', 'synthetic/web', 'latest', 'This image already has this tag. Choose another one.'],
  ])('blocks %s and sends nothing', async (_n, repository, tagText, message) => {
    const s = setup()
    const tag = vi.spyOn(api, 'tagImage')
    const d = await open(s)
    await s.user.clear(d.getByLabelText('Repository'))
    if (repository) {
      await s.user.click(d.getByLabelText('Repository'))
      await s.user.paste(repository)
    }
    if (tagText) {
      await s.user.click(d.getByLabelText('Tag'))
      await s.user.paste(tagText)
    }
    await s.user.click(d.getByRole('button', { name: 'Add tag' }))
    expect(await d.findByText(message)).toBeInTheDocument()
    expect(tag).not.toHaveBeenCalled()
  })

  it('accepts a registry with a port and uppercase host letters', async () => {
    const s = setup()
    const tag = vi.spyOn(api, 'tagImage').mockResolvedValue(undefined)
    const d = await open(s)
    await s.user.clear(d.getByLabelText('Repository'))
    await s.user.click(d.getByLabelText('Repository'))
    await s.user.paste('Registry.Example.test:5000/team/app')
    await s.user.type(d.getByLabelText('Tag'), 'V2_x.y-z')
    await s.user.click(d.getByRole('button', { name: 'Add tag' }))
    await waitFor(() => expect(tag).toHaveBeenCalledTimes(1))
    expect(tag.mock.calls[0][2]).toEqual({ repository: 'Registry.Example.test:5000/team/app', tag: 'V2_x.y-z' })
  })

  it('shows a 409 on the tag field, never offers to move the tag, keeps the dialog open and shows no toast', async () => {
    const s = setup()
    vi.spyOn(api, 'tagImage').mockRejectedValue(new ApiError(409, 'conflict', 'raw'))
    const d = await open(s)
    await s.user.type(d.getByLabelText('Tag'), 'taken')
    await s.user.click(d.getByRole('button', { name: 'Add tag' }))
    expect(await d.findByText(/already points at another image\. Choose another tag/)).toBeInTheDocument()
    expect(d.getByLabelText('Tag')).toHaveAttribute('aria-invalid', 'true')
    expect(d.getByLabelText('Tag')).toHaveAccessibleDescription(/Choose another tag/)
    expect(d.queryByRole('button', { name: /move|force|replace/i })).not.toBeInTheDocument()
    expect(screen.queryByText('Could not tag image aaaaaaaaaaaa')).not.toBeInTheDocument()
    await s.user.type(d.getByLabelText('Tag'), '2')
    expect(d.queryByText(/already points at another image/)).not.toBeInTheDocument()
    await waitFor(() => refreshed(s.invalidate))
  })

  it('shows 422 field messages on their fields and an image problem on the form', async () => {
    const s = setup()
    const tag = vi
      .spyOn(api, 'tagImage')
      .mockRejectedValueOnce(new ApiError(422, 'invalid_request', 'x', { fields: { repository: 'Repository is not valid.' } }))
      .mockRejectedValueOnce(new ApiError(422, 'invalid_request', 'x', { fields: { tag: 'Tag is not valid.' } }))
      .mockRejectedValueOnce(new ApiError(422, 'invalid_request', 'x', { fields: { image: 'bad' } }))
    const d = await open(s)
    await s.user.type(d.getByLabelText('Tag'), 'v2')
    await s.user.click(d.getByRole('button', { name: 'Add tag' }))
    expect(await d.findByText('Repository is not valid.')).toBeInTheDocument()
    await s.user.click(d.getByRole('button', { name: 'Add tag' }))
    expect(await d.findByText('Tag is not valid.')).toBeInTheDocument()
    expect(d.queryByText('Repository is not valid.')).not.toBeInTheDocument()
    await s.user.click(d.getByRole('button', { name: 'Add tag' }))
    expect(await d.findByRole('alert')).toHaveTextContent(/does not know this image/)
    expect(tag).toHaveBeenCalledTimes(3)
  })

  it('explains an image that no longer exists', async () => {
    const s = setup()
    vi.spyOn(api, 'tagImage').mockRejectedValue(new ApiError(404, 'not_found', 'x', { name: 'aaaaaaaaaaaa' }))
    const d = await open(s)
    await s.user.type(d.getByLabelText('Tag'), 'v2')
    await s.user.click(d.getByRole('button', { name: 'Add tag' }))
    expect(await d.findByRole('alert')).toHaveTextContent('This image no longer exists')
  })

  it('toasts any other failure and passes axe with the dialog open', async () => {
    const s = setup()
    vi.spyOn(api, 'tagImage').mockRejectedValue(new ApiError(502, 'docker_unavailable', 'x'))
    const d = await open(s)
    await expectNoA11yViolations(document.body)
    await s.user.type(d.getByLabelText('Tag'), 'v2')
    await s.user.click(d.getByRole('button', { name: 'Add tag' }))
    expect(await screen.findByText('Could not tag image aaaaaaaaaaaa')).toBeInTheDocument()
  })

  it('closes with Cancel and sends nothing', async () => {
    const s = setup()
    const tag = vi.spyOn(api, 'tagImage')
    const d = await open(s)
    await s.user.click(d.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(tag).not.toHaveBeenCalled()
  })
})

describe('inspect image', () => {
  it('loads once when opened, shows the documented fields as text, no commands, env or labels, and never polls', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const get = vi.spyOn(api, 'getImage').mockResolvedValue(detail)
      const s = setup()
      await table()
      expect(get).not.toHaveBeenCalled()
      await s.user.click(screen.getByRole('button', { name: 'Inspect synthetic/web:1' }))
      const d = within(await screen.findByRole('dialog'))
      expect(await d.findByText('arm64')).toBeInTheDocument()
      expect(get).toHaveBeenCalledWith('dev', 'aaaaaaaaaaaa', expect.anything())
      expect(d.getByText('linux')).toBeInTheDocument()
      expect(d.getByText('80, 443')).toBeInTheDocument()
      expect(d.getByText('5 MiB')).toBeInTheDocument()
      expect(d.getByText('synthetic/web:latest')).toBeInTheDocument()
      expect(d.getByText('synthetic/web@sha256:' + '9'.repeat(64))).toBeInTheDocument()
      expect(d.getByText('<b>synthetic-web</b>')).toBeInTheDocument()
      expect(screen.getByRole('dialog').querySelector('b')).toBeNull()
      expect(d.getByText('1 container, running or stopped.')).toBeInTheDocument()
      const layers = within(d.getByRole('list', { name: 'Layers' })).getAllByRole('listitem')
      expect(layers.map((l) => l.textContent)).toEqual([expect.stringMatching(/, 4 MiB$/), 'unknown time, 0 B'])
      expect(d.queryByText(/and \d+ more layers?/)).not.toBeInTheDocument()
      expect(screen.getByRole('dialog').textContent).not.toMatch(/environment|command|label|entrypoint/i)
      await expectNoA11yViolations(document.body)
      await vi.advanceTimersByTimeAsync(120_000)
      expect(get).toHaveBeenCalledTimes(1)
    } finally {
      vi.useRealTimers()
    }
  })

  it('says how many layers are not listed when the list is capped', async () => {
    vi.spyOn(api, 'getImage').mockResolvedValue({ ...detail, layerCount: 203, layers: Array.from({ length: 200 }, () => detail.layers[0]) })
    const s = setup()
    await table()
    await s.user.click(screen.getByRole('button', { name: 'Inspect synthetic/web:1' }))
    const d = within(await screen.findByRole('dialog'))
    expect(await d.findByText('and 3 more layers not listed')).toBeInTheDocument()
    expect(d.getByText(/203 layers, newest first/)).toBeInTheDocument()
    expect(within(d.getByRole('list', { name: 'Layers' })).getAllByRole('listitem')).toHaveLength(200)
  })

  it('shows empty lists and unknown values, then an error with Retry', async () => {
    const get = vi
      .spyOn(api, 'getImage')
      .mockRejectedValueOnce(new ApiError(500, 'internal_error', 'x'))
      .mockResolvedValue({
        ...detail,
        tags: [],
        digests: [],
        createdAt: null,
        architecture: '',
        os: '',
        exposedPorts: [],
        containers: 0,
        containerNames: [],
        layers: [],
        layerCount: 0,
      })
    const s = setup()
    await table()
    await s.user.click(screen.getByRole('button', { name: 'Inspect synthetic/web:1' }))
    const d = within(await screen.findByRole('dialog'))
    expect(await d.findByRole('alert')).toHaveTextContent('The image could not be loaded.')
    await s.user.click(d.getByRole('button', { name: 'Retry' }))
    expect(await d.findByText('No tags.')).toBeInTheDocument()
    expect(d.getByText('No digests.')).toBeInTheDocument()
    expect(d.getByText('No container uses this image.')).toBeInTheDocument()
    expect(d.getByText('No layer information.')).toBeInTheDocument()
    expect(d.getByText('None')).toBeInTheDocument()
    expect(d.getAllByText('Unknown')).toHaveLength(3)
    expect(get).toHaveBeenCalledTimes(2)
    await s.user.click(d.getByRole('button', { name: 'Close' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('explains an image that no longer exists', async () => {
    vi.spyOn(api, 'getImage').mockRejectedValue(new ApiError(404, 'not_found', 'x'))
    const s = setup()
    await table()
    await s.user.click(screen.getByRole('button', { name: 'Inspect image dddddddddddd' }))
    expect(await screen.findByText(/no longer exists/)).toBeInTheDocument()
  })
})

describe('remove image', () => {
  it('is disabled with the visible reason when containers use the image', async () => {
    const s = setup()
    const remove = vi.spyOn(api, 'removeImage')
    await table()
    const inUse = screen.getByRole('button', { name: 'Remove synthetic/api:1' })
    expect(inUse).toHaveAttribute('aria-disabled', 'true')
    expect(inUse).toHaveAccessibleDescription('In use by 2 containers')
    expect(screen.getByText('In use by 2 containers')).toBeVisible()
    expect(screen.getByText('In use by 1 container')).toBeVisible()
    await s.user.click(inUse)
    await s.user.click(screen.getByRole('button', { name: 'Remove image eeeeeeeeeeee' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(remove).not.toHaveBeenCalled()
  })

  it('says the usage is unknown when the count is not a usable number', async () => {
    setup([{ ...images[0], containers: -1 }])
    await table()
    expect(screen.getByRole('button', { name: 'Remove synthetic/web:1' })).toHaveAccessibleDescription('Usage unknown')
  })

  it('uses a plain confirmation naming the image for a single tag and removes it by id', async () => {
    const s = setup()
    const remove = vi.spyOn(api, 'removeImage').mockResolvedValue({ ...removeResult, deleted: ['cccccccccccc'], untagged: ['registry.example.test:5000/team/app:v1'], removedCount: 2 })
    await table()
    await s.user.click(screen.getByRole('button', { name: 'Remove registry.example.test:5000/team/app:v1' }))
    const d = within(await screen.findByRole('alertdialog'))
    expect(d.getByRole('heading', { name: 'Remove image registry.example.test:5000/team/app:v1?' })).toBeInTheDocument()
    expect(d.queryByRole('radio')).not.toBeInTheDocument()
    expect(d.queryByLabelText(/to confirm/)).not.toBeInTheDocument()
    await waitFor(() => expect(d.getByRole('button', { name: 'Cancel' })).toHaveFocus())
    await expectNoA11yViolations(document.body)
    expect(remove).not.toHaveBeenCalled()
    await s.user.click(d.getByRole('button', { name: 'Remove image' }))
    await waitFor(() => expect(remove).toHaveBeenCalledTimes(1))
    expect(remove).toHaveBeenCalledWith('dev', 'cccccccccccc')
    expect(await screen.findByText('Removed 1 image, untagged 1 reference')).toBeInTheDocument()
    refreshed(s.invalidate)
  })

  it('removes an untagged image by its id', async () => {
    const s = setup()
    const remove = vi.spyOn(api, 'removeImage').mockResolvedValue({ ...removeResult, untagged: [], deleted: ['dddddddddddd'] })
    await table()
    await s.user.click(screen.getByRole('button', { name: 'Remove image dddddddddddd' }))
    const d = within(await screen.findByRole('alertdialog'))
    expect(d.getByRole('heading', { name: 'Remove image dddddddddddd?' })).toBeInTheDocument()
    await s.user.click(d.getByRole('button', { name: 'Remove image' }))
    await waitFor(() => expect(remove).toHaveBeenCalledWith('dev', 'dddddddddddd'))
  })

  it('offers the tag choice for several tags, defaults to the least destructive one and untags by exact reference', async () => {
    const s = setup()
    const remove = vi.spyOn(api, 'removeImage').mockResolvedValue(removeResult)
    await table()
    await s.user.click(screen.getByRole('button', { name: 'Remove synthetic/web:1' }))
    const d = within(await screen.findByRole('alertdialog'))
    expect(d.getByText(/keeps the image while other tags point at it/)).toBeInTheDocument()
    expect(d.getByText(/removes all its tags and deletes it/)).toBeInTheDocument()
    const group = d.getByRole('group', { name: 'What to remove' })
    const radios = within(group).getAllByRole('radio')
    expect(radios.map((r) => r.closest('label')?.textContent)).toEqual([
      'Remove only the tag synthetic/web:1',
      'Remove only the tag synthetic/web:latest',
      'Remove the image and all 2 tags',
    ])
    expect(radios[0]).toBeChecked()
    expect(d.getByRole('button', { name: 'Remove tag' })).toBeInTheDocument()
    await expectNoA11yViolations(document.body)
    await s.user.click(radios[1])
    await s.user.click(d.getByRole('button', { name: 'Remove tag' }))
    await waitFor(() => expect(remove).toHaveBeenCalledTimes(1))
    expect(remove).toHaveBeenCalledWith('dev', 'synthetic/web:latest')
    expect(await screen.findByText('Untagged 1 reference')).toBeInTheDocument()
    refreshed(s.invalidate)
  })

  it('removes the whole image by its id when that choice is picked', async () => {
    const s = setup()
    const remove = vi.spyOn(api, 'removeImage').mockResolvedValue({ ...removeResult, untagged: ['synthetic/web:1', 'synthetic/web:latest'], deleted: ['aaaaaaaaaaaa'], removedCount: 3 })
    await table()
    await s.user.click(screen.getByRole('button', { name: 'Remove synthetic/web:1' }))
    const d = within(await screen.findByRole('alertdialog'))
    await s.user.click(d.getByRole('radio', { name: 'Remove the image and all 2 tags' }))
    expect(d.getByRole('button', { name: 'Remove image' })).toBeInTheDocument()
    await s.user.click(d.getByRole('button', { name: 'Remove image' }))
    await waitFor(() => expect(remove).toHaveBeenCalledTimes(1))
    expect(remove).toHaveBeenCalledWith('dev', 'aaaaaaaaaaaa')
    expect(await screen.findByText('Removed 1 image, untagged 2 references')).toBeInTheDocument()
  })

  it('sends nothing when cancelled', async () => {
    const s = setup()
    const remove = vi.spyOn(api, 'removeImage')
    await table()
    await s.user.click(screen.getByRole('button', { name: 'Remove synthetic/web:1' }))
    await s.user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(remove).not.toHaveBeenCalled()
  })

  it('shows a readable message and refreshes when the server answers 409 with a container count', async () => {
    const s = setup()
    vi.spyOn(api, 'removeImage').mockRejectedValue(new ApiError(409, 'conflict', 'raw', { name: 'cccccccccccc', containers: 1 }))
    await table()
    await s.user.click(screen.getByRole('button', { name: 'Remove registry.example.test:5000/team/app:v1' }))
    await s.user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Remove image' }))
    expect(await screen.findByText('Could not remove registry.example.test:5000/team/app:v1')).toBeInTheDocument()
    expect(screen.getByText(/1 container uses this image, running or stopped/)).toBeInTheDocument()
    expect(screen.queryByText('raw')).not.toBeInTheDocument()
    await waitFor(() => refreshed(s.invalidate))
  })

  it('uses the generic 409 text when Docker refused by itself', async () => {
    const s = setup()
    vi.spyOn(api, 'removeImage').mockRejectedValue(new ApiError(409, 'conflict', 'raw', { name: 'cccccccccccc' }))
    await table()
    await s.user.click(screen.getByRole('button', { name: 'Remove registry.example.test:5000/team/app:v1' }))
    await s.user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Remove image' }))
    expect(await screen.findByText(/in use or changed since the list loaded/)).toBeInTheDocument()
  })

  it('says what was removed when a removal fails part-way, and refreshes', async () => {
    const s = setup()
    vi.spyOn(api, 'removeImage').mockRejectedValue(
      new ApiError(502, 'docker_unavailable', 'raw', { untagged: ['synthetic/web:1'], deleted: [] }),
    )
    await table()
    await s.user.click(screen.getByRole('button', { name: 'Remove synthetic/web:1' }))
    const d = within(await screen.findByRole('alertdialog'))
    await s.user.click(d.getByRole('radio', { name: 'Remove the image and all 2 tags' }))
    await s.user.click(d.getByRole('button', { name: 'Remove image' }))
    expect(await screen.findByText('Could not remove image aaaaaaaaaaaa')).toBeInTheDocument()
    expect(screen.getByText(/Part of the removal happened before it failed: synthetic\/web:1\. The list is refreshed\./)).toBeInTheDocument()
    await waitFor(() => refreshed(s.invalidate))
  })

  it('says part of the removal happened for a 409 that carries what was removed', async () => {
    const s = setup()
    vi.spyOn(api, 'removeImage').mockRejectedValue(
      new ApiError(409, 'conflict', 'raw', { untagged: ['synthetic/web:1'], deleted: ['aaaaaaaaaaaa'] }),
    )
    await table()
    await s.user.click(screen.getByRole('button', { name: 'Remove synthetic/web:1' }))
    await s.user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Remove tag' }))
    expect(await screen.findByText(/Part of the removal happened before it failed: synthetic\/web:1, image aaaaaaaaaaaa\./)).toBeInTheDocument()
  })

  it('describes every kind of result honestly', () => {
    expect(removeTitle(removeResult)).toBe('Untagged 1 reference')
    expect(removeTitle({ ...removeResult, untagged: [], deleted: ['a', 'b'], removedCount: 2 })).toBe('Removed 2 images')
    expect(removeTitle({ ...removeResult, removedCount: 0, untagged: [] })).toBe('Nothing was removed.')
    expect(removeTitle({ ...removeResult, removedCount: 1500, truncated: true })).toBe(
      'Removed 1500 items (images and tags) (not every entry is listed)',
    )
    expect(removeTitle({ untagged: [], deleted: [], removedCount: null, truncated: false, resultComplete: false })).toMatch(
      /removal ran.*details are unknown/,
    )
  })
})

describe('prune images', () => {
  async function open(s: ReturnType<typeof setup>) {
    await table()
    await s.user.click(screen.getByRole('button', { name: 'Prune images' }))
    return within(await screen.findByRole('alertdialog'))
  }
  const items = (d: ReturnType<typeof within>) =>
    within(d.getByRole('list', { name: 'Images that would be removed' }))
      .getAllByRole('listitem')
      .map((i) => i.textContent)

  it('defaults to dangling images, lists them from the loaded list with the total, and sends nothing until confirmed', async () => {
    const s = setup()
    const prune = vi.spyOn(api, 'pruneImages').mockResolvedValue(pruneResult)
    const d = await open(s)
    expect(d.getByRole('radio', { name: /Dangling images only/ })).toBeChecked()
    expect(items(d)).toEqual(['<none> dddddddddddd (1 MiB)'])
    expect(d.getByText(/1 image, 1 MiB in total, would be removed/)).toBeInTheDocument()
    expect(d.getByText(/lower bound/)).toBeInTheDocument()
    expect(d.getByText(/Docker decides what is unused/)).toBeInTheDocument()
    expect(d.queryByLabelText(/to confirm/)).not.toBeInTheDocument()
    await waitFor(() => expect(d.getByRole('button', { name: 'Cancel' })).toHaveFocus())
    await expectNoA11yViolations(document.body)
    expect(prune).not.toHaveBeenCalled()
    await s.user.click(d.getByRole('button', { name: 'Prune dangling images' }))
    await waitFor(() => expect(prune).toHaveBeenCalledTimes(1))
    // The function sends the fixed confirmation text itself; the UI passes the profile and the `all` flag.
    expect(prune).toHaveBeenCalledWith('dev', false)
    expect(PRUNE_IMAGES_CONFIRMATION).toBe('delete-unused-images')
    expect(await screen.findByText('Removed 1 image, freed 1 MiB')).toBeInTheDocument()
    refreshed(s.invalidate)
  })

  it('lists every unused image for "All unused images" and requires typing prune', async () => {
    const s = setup()
    const prune = vi.spyOn(api, 'pruneImages').mockResolvedValue({
      ...pruneResult,
      all: true,
      untagged: ['synthetic/web:1', 'synthetic/web:latest', 'registry.example.test:5000/team/app:v1'],
      deleted: ['aaaaaaaaaaaa', 'cccccccccccc', 'dddddddddddd'],
      removedCount: 6,
      reclaimedBytes: 9 * MiB,
    })
    const d = await open(s)
    await s.user.click(d.getByRole('radio', { name: /All unused images/ }))
    expect(items(d)).toEqual([
      'synthetic/web:1, synthetic/web:latest (5 MiB)',
      'registry.example.test:5000/team/app:v1 (3 MiB)',
      '<none> dddddddddddd (1 MiB)',
    ])
    expect(d.getByText(/3 images, 9 MiB in total, would be removed/)).toBeInTheDocument()
    expect(d.getByText(/tagged images included/)).toBeInTheDocument()
    await expectNoA11yViolations(document.body)
    const confirm = d.getByRole('button', { name: 'Prune all unused images' })
    expect(confirm).toBeDisabled()
    await s.user.type(d.getByLabelText(/Type prune to confirm/), 'prun')
    expect(confirm).toBeDisabled()
    expect(prune).not.toHaveBeenCalled()
    await s.user.type(d.getByLabelText(/Type prune to confirm/), 'e')
    await s.user.click(confirm)
    await waitFor(() => expect(prune).toHaveBeenCalledTimes(1))
    expect(prune).toHaveBeenCalledWith('dev', true)
    expect(await screen.findByText('Removed 3 images, untagged 3 references, freed 9 MiB')).toBeInTheDocument()
  })

  it('drops the typed text when switching back to dangling', async () => {
    const s = setup()
    const d = await open(s)
    await s.user.click(d.getByRole('radio', { name: /All unused images/ }))
    await s.user.type(d.getByLabelText(/Type prune to confirm/), 'prune')
    await s.user.click(d.getByRole('radio', { name: /Dangling images only/ }))
    expect(d.queryByLabelText(/Type prune to confirm/)).not.toBeInTheDocument()
    await s.user.click(d.getByRole('radio', { name: /All unused images/ }))
    expect(d.getByRole('button', { name: 'Prune all unused images' })).toBeDisabled()
  })

  it('sends nothing when cancelled', async () => {
    const s = setup()
    const prune = vi.spyOn(api, 'pruneImages')
    const d = await open(s)
    await s.user.click(d.getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(prune).not.toHaveBeenCalled()
  })

  it('disables the choice that matches nothing in the list, with the reason', async () => {
    const s = setup([images[0], images[1]])
    const prune = vi.spyOn(api, 'pruneImages')
    const d = await open(s)
    expect(d.getByText('No dangling images in the list on screen.')).toBeInTheDocument()
    const confirm = d.getByRole('button', { name: 'Prune dangling images' })
    expect(confirm).toBeDisabled()
    expect(confirm).toHaveAccessibleDescription('No dangling images in the list on screen.')
    await s.user.click(d.getByRole('radio', { name: /All unused images/ }))
    expect(items(d)).toEqual(['synthetic/web:1, synthetic/web:latest (5 MiB)'])
    expect(prune).not.toHaveBeenCalled()
  })

  it('is disabled with the reason when no image is unused', async () => {
    const s = setup([images[1], images[4]])
    await table()
    const button = screen.getByRole('button', { name: 'Prune images' })
    expect(button).toHaveAttribute('aria-disabled', 'true')
    expect(button).toHaveAccessibleDescription('No unused images')
    await s.user.click(button)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  it('shows an error toast when the prune fails', async () => {
    const s = setup()
    vi.spyOn(api, 'pruneImages').mockRejectedValue(new ApiError(502, 'docker_unavailable', 'x'))
    const d = await open(s)
    await s.user.click(d.getByRole('button', { name: 'Prune dangling images' }))
    expect(await screen.findByText('Could not prune images')).toBeInTheDocument()
    await waitFor(() => refreshed(s.invalidate))
  })

  it('describes every kind of result honestly', () => {
    expect(imagePruneTitle(pruneResult)).toBe('Removed 1 image, freed 1 MiB')
    expect(imagePruneTitle({ ...pruneResult, reclaimedBytes: null })).toBe('Removed 1 image')
    expect(imagePruneTitle({ ...pruneResult, removedCount: 0, deleted: [], reclaimedBytes: 0 })).toBe('No dangling images were removed.')
    expect(imagePruneTitle({ ...pruneResult, all: true, removedCount: 0, deleted: [], reclaimedBytes: 0 })).toBe('No unused images were removed.')
    expect(imagePruneTitle({ ...pruneResult, removedCount: 2400, truncated: true, reclaimedBytes: 3 * MiB })).toBe(
      'Removed 2400 items (images and tags), freed 3 MiB (not every entry is listed)',
    )
    expect(
      imagePruneTitle({ untagged: [], deleted: [], removedCount: null, truncated: false, reclaimedBytes: null, all: true, resultComplete: false }),
    ).toBe('Prune ran and some images were removed. The exact result is unknown, refresh the list to see what is left.')
    expect(
      imagePruneTitle({ ...pruneResult, resultComplete: false, removedCount: 1 }),
    ).toMatch(/exact result is unknown/)
  })
})

describe('stopped profile', () => {
  it('disables the toolbar with one visible reason and makes no request', async () => {
    const s = setup(images, 'stopped')
    expect(await screen.findByText('Start the profile to see its images.')).toBeInTheDocument()
    expect(screen.getByText('Start the profile to use these actions.')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Prune images' })).toHaveAttribute('aria-disabled', 'true')
    expect(s.getImages).not.toHaveBeenCalled()
  })

  it('has no pull button (that is a separate issue)', async () => {
    setup()
    await table()
    expect(screen.queryByRole('button', { name: /pull/i })).not.toBeInTheDocument()
  })
})
