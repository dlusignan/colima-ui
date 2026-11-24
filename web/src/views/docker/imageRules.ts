import type { DockerImage, TagImageRequest } from '@/lib/api/types'

/** Limits of docs/api.md 3.26, mirrored from ValidImageRepository and ValidImageTag in internal/dockerc/names.go. */
export const MAX_IMAGE_TAG = 128
export const MAX_IMAGE_REFERENCE = 255

const COMPONENT = /^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*$/
const DOMAIN = /^[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?)*(?::[0-9]{1,5})?$/
const TAG = /^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$/
const HEX64 = /^[0-9a-f]{64}$/

const REPOSITORY_HINT =
  'Use lowercase letters and digits, with . _ or - between them, and / between path parts. A registry host and port may come first.'

/** The 12-character id the list and the routes use. */
export const shortImageId = (id: string) => id.replace(/^sha256:/, '').slice(0, 12)

/** What names an image in a sentence or an accessible name: its first tag, or its id when it has none. */
export const imageLabel = (img: DockerImage) => img.tags[0] ?? `image ${shortImageId(img.id)}`

/** Splits a listed tag at its tag colon (a colon after the last slash is the tag, an earlier one is a registry port). */
export function splitReference(ref: string): { repository: string; tag: string } {
  const i = ref.lastIndexOf(':')
  if (i > ref.lastIndexOf('/')) return { repository: ref.slice(0, i), tag: ref.slice(i + 1) }
  return { repository: ref, tag: '' }
}

export function validateRepository(repository: string): string | undefined {
  if (repository === '') return 'Enter a repository.'
  if (repository.length > MAX_IMAGE_REFERENCE) return `Use at most ${MAX_IMAGE_REFERENCE} characters.`
  if (repository.includes('@')) return 'Leave out the digest (@…). Enter the repository only.'
  if (repository.lastIndexOf(':') > repository.lastIndexOf('/')) {
    return 'Leave out the tag. Enter it in the Tag field.'
  }
  if (repository === 'sha256' || HEX64.test(repository)) {
    return 'This name would read like an image ID. Choose another repository.'
  }
  const parts = repository.split('/')
  if (parts.length > 1) {
    const d = parts[0]
    if (/[.:]/.test(d) || d === 'localhost' || d !== d.toLowerCase()) {
      if (!DOMAIN.test(d)) return `The registry host is not valid. ${REPOSITORY_HINT}`
      parts.shift()
    }
  }
  if (!parts.every((c) => COMPONENT.test(c))) return REPOSITORY_HINT
  return undefined
}

export function validateTag(tag: string): string | undefined {
  if (tag === '') return 'Enter a tag.'
  if (tag.length > MAX_IMAGE_TAG) return `Use at most ${MAX_IMAGE_TAG} characters.`
  if (!TAG.test(tag)) {
    return 'Use letters, digits, underscore, dot and hyphen, not starting with a dot or hyphen.'
  }
  return undefined
}

export interface TagFormErrors {
  repository?: string
  tag?: string
}

/** The tag field also carries the limit on the whole reference, since it is what the user adds last. */
export function validateTagForm(repository: string, tag: string, existing: readonly string[]): TagFormErrors {
  const errors: TagFormErrors = {}
  const r = validateRepository(repository)
  const t = validateTag(tag)
  if (r) errors.repository = r
  if (t) errors.tag = t
  if (!r && !t) {
    if (repository.length + 1 + tag.length > MAX_IMAGE_REFERENCE) {
      errors.tag = `The whole reference is limited to ${MAX_IMAGE_REFERENCE} characters.`
    } else if (existing.includes(`${repository}:${tag}`)) {
      errors.tag = 'This image already has this tag. Choose another one.'
    }
  }
  return errors
}

/** The reference the tag will have, or null while either field is empty. */
export const resultingReference = (repository: string, tag: string) =>
  repository !== '' && tag !== '' ? `${repository}:${tag}` : null

export const buildTagRequest = (repository: string, tag: string): TagImageRequest => ({ repository, tag })

/** Why an image cannot be removed from here, or undefined when it can. Any container in any state counts. */
export function imageRemoveReason(img: DockerImage): string | undefined {
  const n = img.containers
  if (!Number.isFinite(n) || n < 0) return 'Usage unknown'
  if (n > 0) return `In use by ${n} ${n === 1 ? 'container' : 'containers'}`
  return undefined
}

export type PruneMode = 'dangling' | 'all'

/**
 * What a prune would remove, judged from the loaded list only. Dangling means
 * no tag and no container; all unused means no container. Docker decides what
 * is unused when the call runs, so this is a lower bound.
 */
export function pruneCandidates(images: readonly DockerImage[], mode: PruneMode): DockerImage[] {
  return images.filter((i) => i.containers === 0 && (mode === 'all' || i.tags.length === 0))
}
