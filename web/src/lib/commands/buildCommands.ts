import type { AppRoute } from '@/app/types'
import { actionsDisabled } from '@/components/profileActionRules'
import type { ProfileAction, Profile } from '@/lib/api/types'
import type { ThemeDef } from '@/lib/theme'

/** What choosing a command does. Data, so the list is testable without cmdk. */
export type CommandIntent =
  | { type: 'navigate'; to: string }
  | { type: 'action'; profile: string; action: ProfileAction }
  | { type: 'theme'; id: string }
  | { type: 'help' }
  | { type: 'create-profile' }

export interface PaletteCommand {
  /** Unique; also the cmdk value. */
  label: string
  group: string
  keywords: string[]
  intent: CommandIntent
}

interface BuildInput {
  profiles: readonly Profile[]
  /** The profile of the current /profiles/:name route; its entries come first. */
  currentProfile?: string
  /** Names of profiles with a job running or starting. */
  busy: ReadonlySet<string>
  themes: readonly ThemeDef[]
  /** The profile tabs, with `:name` in their paths. */
  tabs: readonly AppRoute[]
}

const ACTIONS: { action: ProfileAction; label: string }[] = [
  { action: 'start', label: 'Start' },
  { action: 'stop', label: 'Stop' },
  { action: 'restart', label: 'Restart' },
]

export const isUnavailable = (p: Profile) =>
  (p.warnings ?? []).some((w) => w.code === 'colima_unavailable')

/**
 * Every palette entry. Actions follow the dashboard buttons' rules and are
 * hidden, not shown disabled, when they cannot run.
 */
export function buildCommands({ profiles, currentProfile, busy, themes, tabs }: BuildInput): PaletteCommand[] {
  const ordered = [...profiles].sort(
    (a, b) => Number(b.name === currentProfile) - Number(a.name === currentProfile),
  )
  const commands: PaletteCommand[] = [
    { label: 'Go to Dashboard', group: 'Navigate', keywords: ['home', 'profiles'], intent: { type: 'navigate', to: '/' } },
  ]
  commands.push({
    label: 'Create profile',
    group: 'Profiles',
    keywords: ['new', 'add', 'profile', 'vm'],
    intent: { type: 'create-profile' },
  })
  for (const p of ordered) {
    for (const tab of tabs) {
      commands.push({
        label: `Go to ${p.name}: ${tab.label}`,
        group: 'Navigate',
        keywords: [p.name, tab.label, 'open', 'view'],
        intent: { type: 'navigate', to: tab.path.replace(':name', encodeURIComponent(p.name)) },
      })
    }
  }
  for (const p of ordered) {
    const off = actionsDisabled(p.status, busy.has(p.name) || p.activeJobId != null || isUnavailable(p))
    for (const { action, label } of ACTIONS) {
      if (off[action]) continue
      commands.push({
        label: `${label} ${p.name}`,
        group: 'Actions',
        keywords: [p.name, action, 'profile', 'vm'],
        intent: { type: 'action', profile: p.name, action },
      })
    }
  }
  commands.push({
    label: 'Theme: System',
    group: 'Theme',
    keywords: ['appearance', 'color', 'os'],
    intent: { type: 'theme', id: 'system' },
  })
  for (const t of themes) {
    commands.push({
      label: `Theme: ${t.label}`,
      group: 'Theme',
      keywords: ['appearance', 'color', t.id],
      intent: { type: 'theme', id: t.id },
    })
  }
  commands.push({
    label: 'Keyboard shortcuts',
    group: 'Help',
    keywords: ['help', 'keys', 'hotkeys'],
    intent: { type: 'help' },
  })
  return commands
}
