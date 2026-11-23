import type { AppRoute } from '@/app/types'
import { LogView } from './LogView'

export const logRoute: AppRoute = {
  id: 'log',
  path: '/profiles/:name/log',
  label: 'Operation log',
  element: <LogView />,
}
