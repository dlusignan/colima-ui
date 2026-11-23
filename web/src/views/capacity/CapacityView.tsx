import { useParams } from 'react-router-dom'
import { ViewPlaceholder } from '@/components/ViewPlaceholder'

export function CapacityView() {
  const { name } = useParams()
  return (
    <ViewPlaceholder title="Capacity">
      <p className="font-mono text-sm">Profile: {name}</p>
    </ViewPlaceholder>
  )
}
