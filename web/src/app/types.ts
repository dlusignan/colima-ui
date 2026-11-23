import type { ReactElement } from 'react'

/** What each view's route.tsx exports. routes.tsx is the only file that lists them. */
export interface AppRoute {
  id: string
  /** Absolute path, with `:name` for the profile name. */
  path: string
  label: string
  element: ReactElement
}
