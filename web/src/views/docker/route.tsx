import type { AppRoute } from '@/app/types'
import { LazyDockerView } from './LazyDockerView'

export const dockerRoute: AppRoute = {
  id: 'docker',
  path: '/profiles/:name/docker',
  label: 'Docker',
  element: <LazyDockerView />,
}
