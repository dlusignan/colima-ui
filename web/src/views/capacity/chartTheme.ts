// uPlot draws on a canvas, which needs concrete colors, so they are read from
// the active theme's CSS variables at runtime. No color literals live here.

export interface ChartTheme {
  /** One stroke per chart: CPU, memory, disk. */
  series: [string, string, string]
  allocated: string
  axis: string
  grid: string
}

// Canvas strokes need some value when a variable is missing (a test, a broken
// theme). A named color is not a theme color, so it only shows up on failure.
const FALLBACK = 'gray'

export function buildChartTheme(el: Element = document.documentElement): ChartTheme {
  const style = getComputedStyle(el)
  const read = (variable: string) => style.getPropertyValue(variable).trim() || FALLBACK
  return {
    series: [read('--chart-1'), read('--chart-2'), read('--chart-3')],
    allocated: read('--warning'),
    axis: read('--muted-foreground'),
    grid: read('--border'),
  }
}
