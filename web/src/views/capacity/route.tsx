import type { AppRoute } from '@/app/types'
import { LazyCapacityView } from './LazyCapacityView'

export const capacityRoute: AppRoute = {
  id: 'capacity',
  path: '/profiles/:name/capacity',
  label: 'Capacity',
  element: <LazyCapacityView />,
}
