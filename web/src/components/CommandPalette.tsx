import { useEffect, useRef, useState } from 'react'
import { useMatch, useNavigate } from 'react-router-dom'
import { profileRoutes } from '@/app/profileRoutes'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { actionsDisabled, CONFIRM } from '@/components/profileActionRules'
import { Button } from '@/components/ui/button'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { useProfiles } from '@/lib/api/useProfiles'
import type { ProfileAction } from '@/lib/api/types'
import { buildCommands, isUnavailable, type CommandIntent, type PaletteCommand } from '@/lib/commands/buildCommands'
import type { ShortcutId } from '@/lib/commands/shortcuts'
import { useGlobalShortcuts } from '@/lib/commands/useGlobalShortcuts'
import { useJobTracker, type JobTracker } from '@/lib/jobs/useJobTracker'
import { useProfileActionRunner } from '@/lib/jobs/useProfileActionRunner'
import { themes, useTheme } from '@/lib/theme'
import { useCreateProfile } from '@/views/create/createProfileContext'

interface ActionRequest {
  id: number
  action: ProfileAction
}

const VERB: Record<ProfileAction, string> = { start: 'Starting', stop: 'Stopping', restart: 'Restarting', update: 'Updating' }
const LABEL: Record<ProfileAction, string> = { start: 'Start', stop: 'Stop', restart: 'Restart', update: 'Update runtime' }

/**
 * Runs the latest request for one profile. It stays mounted after the request:
 * TanStack Query drops the per-call success and error callbacks of a mutation
 * whose component unmounted, and the runner needs them to adopt the job.
 */
function ActionRunner({
  profile,
  request,
  tracker,
  onError,
}: {
  profile: string
  request: ActionRequest
  tracker: JobTracker
  onError: (message: string) => void
}) {
  const runner = useProfileActionRunner(profile, tracker, onError)
  const run = useRef(runner.run)
  useEffect(() => {
    run.current = runner.run
  })
  useEffect(() => {
    run.current(request.action)
  }, [request])
  return null
}

interface Failure {
  id: number
  message: string
}

/**
 * The Cmd+K palette and the profile shortcuts. Mounted inside AuthGate so
 * nothing renders or fetches without a token.
 */
export function CommandPalette({ onOpenHelp }: { onOpenHelp: () => void }) {
  const navigate = useNavigate()
  const currentProfile = useMatch('/profiles/:name/*')?.params.name
  const profiles = useProfiles().data?.profiles
  const tracker = useJobTracker(profiles)
  const { select: selectTheme } = useTheme()
  const createProfile = useCreateProfile()

  const [open, setOpen] = useState(false)
  const [pick, setPick] = useState<ProfileAction | null>(null)
  const [confirm, setConfirm] = useState<{ profile: string; action: 'stop' | 'restart' | 'update' } | null>(null)
  const [requests, setRequests] = useState<Record<string, ActionRequest>>({})
  const [failures, setFailures] = useState<Failure[]>([])
  const [announcement, setAnnouncement] = useState('')
  const counter = useRef(0)

  const busy = new Set<string>()
  for (const [name, job] of tracker.jobs) if (job.state === 'running') busy.add(name)

  const commands = buildCommands({ profiles: profiles ?? [], currentProfile, busy, themes, tabs: profileRoutes })
  const visible = pick
    ? commands.filter((c) => c.intent.type === 'action' && c.intent.action === pick)
    : commands

  const closePalette = () => {
    setOpen(false)
    setPick(null)
  }

  const dispatch = (profile: string, action: ProfileAction) => {
    setRequests((r) => ({ ...r, [profile]: { id: counter.current++, action } }))
    setAnnouncement(`${VERB[action]} ${profile}.`)
  }

  /** Start runs at once; Stop and Restart ask first, like the dashboard buttons. */
  const requestAction = (name: string, action: ProfileAction) => {
    const profile = profiles?.find((p) => p.name === name)
    if (!profile) return
    const off = actionsDisabled(profile.status, busy.has(name) || profile.activeJobId != null || isUnavailable(profile))
    if (off[action]) {
      setAnnouncement(`${LABEL[action]} is not available for ${name} right now.`)
      return
    }
    if (action === 'start') dispatch(name, action)
    else setConfirm({ profile: name, action })
  }

  const selectedProfile = (): string | null => {
    const names = (profiles ?? []).map((p) => p.name)
    if (currentProfile && names.includes(currentProfile)) return currentProfile
    const row = document.activeElement?.closest('tr[data-profile]')?.getAttribute('data-profile')
    if (row && names.includes(row)) return row
    return names.length === 1 ? names[0] : null
  }

  const run = (intent: CommandIntent) => {
    closePalette()
    switch (intent.type) {
      case 'navigate':
        void navigate(intent.to)
        break
      case 'action':
        requestAction(intent.profile, intent.action)
        break
      case 'theme':
        selectTheme(intent.id)
        break
      case 'help':
        onOpenHelp()
        break
      case 'create-profile':
        createProfile.open()
        break
    }
  }

  useGlobalShortcuts(open, (id: ShortcutId) => {
    if (id === 'palette') {
      if (open) closePalette()
      else setOpen(true)
    } else if (id === 'help') {
      onOpenHelp()
    } else {
      const target = selectedProfile()
      if (target) requestAction(target, id)
      else {
        setPick(id)
        setOpen(true)
      }
    }
  })

  const groups = [...new Set(visible.map((c) => c.group))]
  const byGroup = (g: string): PaletteCommand[] => visible.filter((c) => c.group === g)

  return (
    <>
      <Dialog open={open} onOpenChange={(next) => (next ? setOpen(true) : closePalette())}>
        <DialogContent className="p-0">
          <DialogTitle className="sr-only">Command palette</DialogTitle>
          <DialogDescription className="sr-only">
            Type to filter, use the arrow keys and Enter to run a command, Escape to close.
          </DialogDescription>
          <Command label="Search commands">
            <CommandInput
              autoFocus
              placeholder={pick ? `Pick a profile to ${pick}` : 'Type a command or search'}
            />
            <CommandList>
              <CommandEmpty>{pick ? `No profile can ${pick} right now.` : 'No matching commands.'}</CommandEmpty>
              {groups.map((g) => (
                <CommandGroup key={g} heading={pick ? `Pick a profile to ${pick}` : g}>
                  {byGroup(g).map((c) => (
                    <CommandItem key={c.label} value={c.label} keywords={c.keywords} onSelect={() => run(c.intent)}>
                      {c.label}
                    </CommandItem>
                  ))}
                </CommandGroup>
              ))}
            </CommandList>
          </Command>
        </DialogContent>
      </Dialog>

      {confirm && (
        <ConfirmDialog
          open
          onOpenChange={(next) => {
            if (!next) setConfirm(null)
          }}
          title={`${CONFIRM[confirm.action].label} ${confirm.profile}?`}
          description={CONFIRM[confirm.action].description(confirm.profile)}
          confirmLabel={CONFIRM[confirm.action].label}
          destructive
          onConfirm={() => dispatch(confirm.profile, confirm.action)}
        />
      )}

      {Object.entries(requests).map(([profile, request]) => (
        <ActionRunner
          key={profile}
          profile={profile}
          request={request}
          tracker={tracker}
          onError={(message) => setFailures((f) => [...f, { id: counter.current++, message }])}
        />
      ))}

      {failures.length > 0 && (
        <div role="alert" className="fixed right-4 bottom-4 z-40 flex max-w-sm flex-col gap-1">
          {failures.map((f) => (
            <div
              key={f.id}
              className="flex items-center gap-2 rounded-lg border border-destructive bg-popover px-3 py-2 text-sm"
            >
              <span>{f.message}</span>
              <Button
                size="xs"
                variant="ghost"
                aria-label={`Dismiss error: ${f.message}`}
                onClick={() => setFailures((all) => all.filter((x) => x.id !== f.id))}
              >
                Dismiss
              </Button>
            </div>
          ))}
        </div>
      )}
      <div role="status" aria-live="polite" className="sr-only">
        {announcement}
      </div>
    </>
  )
}
