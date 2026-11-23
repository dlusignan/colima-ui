import type { AppRoute } from '@/app/types'
import { ProfileView } from './ProfileView'

export const profileRoute: AppRoute = {
  id: 'profile',
  path: '/profiles/:name',
  label: 'Detail',
  element: <ProfileView />,
}
