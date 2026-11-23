import { useParams } from 'react-router-dom'
import { ViewPlaceholder } from '@/components/ViewPlaceholder'

export function ProfileView() {
  const { name } = useParams()
  return (
    <ViewPlaceholder title="Detail">
      <p className="font-mono text-sm">Profile: {name}</p>
    </ViewPlaceholder>
  )
}
