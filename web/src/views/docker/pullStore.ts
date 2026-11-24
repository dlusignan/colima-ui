/**
 * Pulls this browser started and that may still be running. The backend has no
 * job list and a pull is not the profile's `activeJobId` (it holds no start/stop
 * slot, docs/api.md 3.30), so a reload can only find a running pull by the job
 * id remembered here. Pulls started elsewhere (another browser, curl) are not
 * rediscovered.
 */
export interface TrackedPull {
  profile: string
  jobId: string
  reference: string
}

const KEY = 'colima-ui:image-pulls'
const MAX_KEPT = 20

function readAll(): TrackedPull[] {
  try {
    const parsed: unknown = JSON.parse(window.localStorage.getItem(KEY) ?? '[]')
    if (!Array.isArray(parsed)) return []
    return parsed
      .filter(
        (p): p is TrackedPull =>
          typeof p === 'object' &&
          p !== null &&
          typeof (p as TrackedPull).profile === 'string' &&
          typeof (p as TrackedPull).jobId === 'string' &&
          typeof (p as TrackedPull).reference === 'string',
      )
      .slice(-MAX_KEPT)
  } catch {
    return []
  }
}

function writeAll(pulls: TrackedPull[]): void {
  try {
    window.localStorage.setItem(KEY, JSON.stringify(pulls.slice(-MAX_KEPT)))
  } catch {
    // Storage can be blocked: the pull still runs, only a reload forgets it.
  }
}

export const loadPulls = (): TrackedPull[] => readAll()

export function rememberPull(pull: TrackedPull): void {
  writeAll([...readAll().filter((p) => p.jobId !== pull.jobId), pull])
}

export function forgetPull(jobId: string): void {
  writeAll(readAll().filter((p) => p.jobId !== jobId))
}
