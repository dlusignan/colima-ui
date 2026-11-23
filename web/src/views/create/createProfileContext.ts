import { createContext, useContext } from 'react'

interface CreateProfileApi {
  /** Opens the create profile dialog. */
  open: () => void
}

export const CreateProfileContext = createContext<CreateProfileApi | null>(null)

/** Lets the dashboard button and the command palette open the same dialog. */
export function useCreateProfile(): CreateProfileApi {
  const ctx = useContext(CreateProfileContext)
  if (!ctx) throw new Error('useCreateProfile needs a CreateProfileProvider.')
  return ctx
}
