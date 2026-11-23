import type { AppRoute } from '@/app/types'
import { LazyLogView } from './LazyLogView'

export const logRoute: AppRoute = {
  id: 'log',
  path: '/profiles/:name/log',
  label: 'Operation log',
  element: <LazyLogView />,
}
