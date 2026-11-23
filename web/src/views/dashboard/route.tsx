import type { AppRoute } from '@/app/types'
import { DashboardView } from './DashboardView'

export const dashboardRoute: AppRoute = {
  id: 'dashboard',
  path: '/',
  label: 'Dashboard',
  element: <DashboardView />,
}
