import type { ReactNode } from 'react'

export function ViewPlaceholder({
  title,
  children,
}: {
  title: string
  children?: ReactNode
}) {
  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-xl font-semibold">{title}</h2>
      <p className="text-sm text-muted-foreground">
        Placeholder. The content for this view arrives in a later issue.
      </p>
      {children}
    </section>
  )
}
