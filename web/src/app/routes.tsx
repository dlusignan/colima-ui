// The only file that lists the views. A new view adds its route.tsx import and
// one entry here (the five views of this milestone are already registered).
import { Route, Routes } from 'react-router-dom'
import { capacityRoute } from '@/views/capacity/route'
import { configRoute } from '@/views/config/route'
import { dashboardRoute } from '@/views/dashboard/route'
import { logRoute } from '@/views/log/route'
import { profileRoute } from '@/views/profile/route'
import { Layout } from './Layout'
import { ProfileLayout } from './ProfileLayout'
import type { AppRoute } from './types'

const profileRoutes: AppRoute[] = [
  profileRoute,
  capacityRoute,
  configRoute,
  logRoute,
]

export function AppRoutes() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route path={dashboardRoute.path} element={dashboardRoute.element} />
        <Route element={<ProfileLayout tabs={profileRoutes} />}>
          {profileRoutes.map((r) => (
            <Route key={r.id} path={r.path} element={r.element} />
          ))}
        </Route>
      </Route>
    </Routes>
  )
}
