/**
 * Every keyboard shortcut, in one table: the global handler matches events
 * against it and the help dialog renders it, so the two cannot drift. All
 * bindings carry a modifier, so none collides with typing.
 */
export type ShortcutId = 'palette' | 'help' | 'start' | 'stop' | 'restart'

export interface Shortcut {
  id: ShortcutId
  /** Shown in the help dialog. */
  keys: string
  description: string
  /** Fires while an input, textarea, select or contenteditable has focus. */
  inEditable: boolean
  matches: (e: KeyboardEvent) => boolean
}

const mod = (e: KeyboardEvent) => (e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey
// event.code, not event.key: Option on macOS rewrites key into a symbol.
const altShift = (e: KeyboardEvent, code: string) =>
  e.altKey && e.shiftKey && !e.metaKey && !e.ctrlKey && e.code === code

export const shortcuts: readonly Shortcut[] = [
  {
    id: 'palette',
    keys: 'Cmd+K or Ctrl+K',
    description: 'Open or close the command palette',
    inEditable: true,
    matches: (e) => mod(e) && e.code === 'KeyK',
  },
  {
    id: 'help',
    keys: 'Cmd+/ or Ctrl+/',
    description: 'Show the keyboard shortcuts',
    inEditable: false,
    matches: (e) => mod(e) && e.code === 'Slash',
  },
  {
    id: 'start',
    keys: 'Alt+Shift+S',
    description: 'Start the selected profile',
    inEditable: false,
    matches: (e) => altShift(e, 'KeyS'),
  },
  {
    id: 'stop',
    keys: 'Alt+Shift+X',
    description: 'Stop the selected profile (asks first)',
    inEditable: false,
    matches: (e) => altShift(e, 'KeyX'),
  },
  {
    id: 'restart',
    keys: 'Alt+Shift+R',
    description: 'Restart the selected profile (asks first)',
    inEditable: false,
    matches: (e) => altShift(e, 'KeyR'),
  },
]
