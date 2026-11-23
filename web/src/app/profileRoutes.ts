// The tabs of a profile. routes.tsx registers them and the command palette
// builds its "go to" entries from the same list, so a new tab appears in both.
import { capacityRoute } from '@/views/capacity/route'
import { configRoute } from '@/views/config/route'
import { logRoute } from '@/views/log/route'
import { profileRoute } from '@/views/profile/route'
import type { AppRoute } from './types'

export const profileRoutes: AppRoute[] = [profileRoute, capacityRoute, configRoute, logRoute]
