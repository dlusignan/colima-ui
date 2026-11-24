import { NavLink, Outlet, useParams } from 'react-router-dom'
import type { AppRoute } from './types'

/** Wraps the /profiles/:name routes with the profile tabs. */
export function ProfileLayout({ tabs }: { tabs: AppRoute[] }) {
  const { name = '' } = useParams()
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-baseline gap-3">
        <h2 className="font-mono text-lg break-words">{name}</h2>
      </div>
      <nav aria-label="Profile" className="flex flex-wrap gap-x-4 border-b border-border">
        {tabs.map((t) => (
          <NavLink
            key={t.id}
            to={t.path.replace(':name', encodeURIComponent(name))}
            end
            className="-mb-px border-b-2 border-transparent pb-2 text-sm text-muted-foreground hover:text-foreground aria-[current=page]:border-primary aria-[current=page]:text-foreground"
          >
            {t.label}
          </NavLink>
        ))}
      </nav>
      <Outlet />
    </div>
  )
}
