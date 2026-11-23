import { useEffect, useRef, useState } from 'react'
import uPlot from 'uplot'
import 'uplot/dist/uPlot.min.css'
import { buildChartTheme } from './chartTheme'
import type { SeriesBuffer, SeriesKey } from './SeriesBuffer'

export interface UPlotChartProps {
  buffer: SeriesBuffer
  seriesKey: SeriesKey
  /** Index into the theme's series colors. */
  colorIndex: 0 | 1 | 2
  /** The allocated value drawn as a flat dashed line, or null for none. */
  limit: number | null
  formatValue: (value: number) => string
  height?: number
}

/** Canvas chart: thin lines, no fill, a dashed allocated line, theme colors. */
export function UPlotChart({
  buffer,
  seriesKey,
  colorIndex,
  limit,
  formatValue,
  height = 180,
}: UPlotChartProps) {
  const hostRef = useRef<HTMLDivElement>(null)
  const [themeEpoch, setThemeEpoch] = useState(0)
  // The formatter is only read when the chart is built; keep its identity out
  // of the effect dependencies.
  const formatRef = useRef(formatValue)
  useEffect(() => {
    formatRef.current = formatValue
  })

  useEffect(() => {
    const watch = new MutationObserver(() => setThemeEpoch((n) => n + 1))
    watch.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['data-theme', 'class'],
    })
    return () => watch.disconnect()
  }, [])

  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    const theme = buildChartTheme()
    const stroke = theme.series[colorIndex]
    const grid = { stroke: theme.grid, width: 1 }
    const chart = new uPlot(
      {
        width: host.clientWidth || 600,
        height,
        legend: { show: false },
        scales: {
          x: { time: true },
          y: {
            range: (_u, _min, max) => [0, Math.max(max ?? 0, limit ?? 0, 1) * 1.05],
          },
        },
        series: [
          {},
          { stroke, width: 1, spanGaps: false, points: { show: false } },
          { stroke: theme.allocated, width: 1, dash: [6, 4], points: { show: false } },
        ],
        axes: [
          { stroke: theme.axis, grid, ticks: grid },
          {
            stroke: theme.axis,
            grid,
            ticks: grid,
            size: 72,
            values: (_u, vals) => vals.map((v) => formatRef.current(v)),
          },
        ],
      },
      emptyData(),
      host,
    )

    const draw = () => {
      const { t, values } = buffer.column(seriesKey)
      chart.setData([t, values, t.map(() => limit)], true)
    }
    draw()

    let frame: number | null = null
    const unsubscribe = buffer.subscribe(() => {
      if (frame !== null) return
      frame = requestAnimationFrame(() => {
        frame = null
        draw()
      })
    })

    let resizeFrame: number | null = null
    const resize = new ResizeObserver(() => {
      if (resizeFrame !== null) cancelAnimationFrame(resizeFrame)
      resizeFrame = requestAnimationFrame(() => {
        resizeFrame = null
        const width = host.clientWidth
        if (width > 0) chart.setSize({ width, height })
      })
    })
    resize.observe(host)

    return () => {
      unsubscribe()
      resize.disconnect()
      if (frame !== null) cancelAnimationFrame(frame)
      if (resizeFrame !== null) cancelAnimationFrame(resizeFrame)
      chart.destroy()
    }
  }, [buffer, seriesKey, colorIndex, limit, height, themeEpoch])

  return <div ref={hostRef} data-testid="uplot-host" className="w-full" />
}

function emptyData(): uPlot.AlignedData {
  return [[], [], []]
}
