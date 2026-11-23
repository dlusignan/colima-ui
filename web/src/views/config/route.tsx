import type { AppRoute } from '@/app/types'
import { ConfigView } from './ConfigView'

export const configRoute: AppRoute = {
  id: 'config',
  path: '/profiles/:name/config',
  label: 'Configuration',
  element: <ConfigView />,
}
