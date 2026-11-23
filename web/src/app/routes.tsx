// The only file that lists the views. A new view adds its route.tsx import and
// one entry here (the profile tabs live in profileRoutes.ts).
import { Route, Routes } from 'react-router-dom'
import { dashboardRoute } from '@/views/dashboard/route'
import { Layout } from './Layout'
import { ProfileLayout } from './ProfileLayout'
import { profileRoutes } from './profileRoutes'

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
