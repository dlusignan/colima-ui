import { useParams } from 'react-router-dom'
import { ViewPlaceholder } from '@/components/ViewPlaceholder'

export function LogView() {
  const { name } = useParams()
  return (
    <ViewPlaceholder title="Operation log">
      <p className="font-mono text-sm">Profile: {name}</p>
    </ViewPlaceholder>
  )
}
