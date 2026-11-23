import { ThemeSwitcher } from '@/components/ThemeSwitcher'
import { Button } from '@/components/ui/button'

export function HomePage() {
  return (
    <main className="mx-auto flex min-h-screen max-w-xl flex-col items-start justify-center gap-4 p-8">
      <h1 className="text-3xl font-semibold">Colima UI</h1>
      <p className="text-muted-foreground">
        Placeholder page. Views arrive in later issues.
      </p>
      <Button>Get started</Button>
      <ThemeSwitcher />
    </main>
  )
}
