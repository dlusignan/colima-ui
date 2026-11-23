import { useParams } from 'react-router-dom'
import { useProfiles } from '@/lib/api/useProfiles'
import { ImagesSection } from './ImagesSection'

/** The Docker resources of one profile. Images today; volumes and networks follow. */
export function DockerView() {
  const { name = '' } = useParams()
  const profiles = useProfiles()
  const profile = profiles.data?.profiles.find((p) => p.name === name)

  let body
  if (profiles.isPending) {
    body = <p className="text-sm text-muted-foreground">Loading...</p>
  } else if (!profile) {
    body = (
      <p role="alert" className="text-sm text-destructive">
        Profile {name} was not found.
      </p>
    )
  } else {
    body = <ImagesSection profile={profile} />
  }

  return (
    <section className="flex flex-col gap-4">
      <h2 className="text-xl font-semibold">Docker: {name}</h2>
      {body}
    </section>
  )
}
