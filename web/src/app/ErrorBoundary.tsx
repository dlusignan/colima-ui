import { Component, type ErrorInfo, type ReactNode } from 'react'
import { Button } from '@/components/ui/button'

interface Props {
  children: ReactNode
}

interface State {
  error: Error | null
}

/** Catches render errors. Layout keys it on the pathname so navigating clears it. */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error(error, info.componentStack)
  }

  render() {
    const { error } = this.state
    if (!error) return this.props.children
    return (
      <div
        role="alert"
        className="flex flex-col items-start gap-3 rounded-lg border border-destructive/40 bg-card p-4"
      >
        <h2 className="text-lg font-semibold text-destructive">Something went wrong</h2>
        <p className="font-mono text-sm text-muted-foreground">{error.message}</p>
        <Button variant="outline" onClick={() => this.setState({ error: null })}>
          Try again
        </Button>
      </div>
    )
  }
}
