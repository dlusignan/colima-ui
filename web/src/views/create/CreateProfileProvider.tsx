import { useMemo, useState, type ReactNode } from 'react'
import { CreateProfileContext } from './createProfileContext'
import { CreateProfileDialog } from './CreateProfileDialog'

/** Owns the open state of the create profile dialog and renders it once for the whole app. */
export function CreateProfileProvider({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState(false)
  const api = useMemo(() => ({ open: () => setOpen(true) }), [])
  return (
    <CreateProfileContext.Provider value={api}>
      {children}
      <CreateProfileDialog open={open} onOpenChange={setOpen} />
    </CreateProfileContext.Provider>
  )
}
