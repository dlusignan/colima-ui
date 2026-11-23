import { useTheme } from '@/lib/theme'

export function ThemeSwitcher() {
  const { choice, select, themes } = useTheme()
  return (
    <label className="flex items-center gap-2 text-sm text-muted-foreground">
      Theme
      <select
        value={choice}
        onChange={(e) => select(e.target.value)}
        className="h-8 rounded-lg border border-input bg-card px-2 font-mono text-foreground outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        <option value="system">System</option>
        {themes.map((t) => (
          <option key={t.id} value={t.id}>
            {t.label}
          </option>
        ))}
      </select>
    </label>
  )
}
