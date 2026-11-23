import type { AppRoute } from '@/app/types'
import { CapacityView } from './CapacityView'

export const capacityRoute: AppRoute = {
  id: 'capacity',
  path: '/profiles/:name/capacity',
  label: 'Capacity',
  element: <CapacityView />,
}
