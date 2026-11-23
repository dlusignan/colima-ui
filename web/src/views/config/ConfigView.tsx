import { useParams } from 'react-router-dom'
import { ViewPlaceholder } from '@/components/ViewPlaceholder'

export function ConfigView() {
  const { name } = useParams()
  return (
    <ViewPlaceholder title="Configuration">
      <p className="font-mono text-sm">Profile: {name}</p>
    </ViewPlaceholder>
  )
}
