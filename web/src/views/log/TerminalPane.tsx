import { useCallback, useEffect, useRef, useState } from 'react'
import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { Button } from '@/components/ui/button'
import type { JobLine } from '@/lib/events'
import { buildTerminalTheme } from './terminalTheme'

/** Lines written per animation frame, so a big first paint does not block. */
const CHUNK = 500

export interface TerminalPaneProps {
  /** Ordered, one entry per seq. New entries are appended to the terminal. */
  lines: readonly JobLine[]
  /** Dim notice written before the first line, for example dropped output. */
  notice?: string
}

function countUpTo(lines: readonly JobLine[], seq: number): number {
  let n = 0
  for (const l of lines) if (l.seq <= seq) n++
  return n
}

/** Read-only xterm instance. Lines keep their ANSI escapes. */
export function TerminalPane({ lines, notice }: TerminalPaneProps) {
  const hostRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const queue = useRef<string[]>([])
  const frame = useRef<number | null>(null)
  const written = useRef({ seq: 0, count: 0, notice: undefined as string | undefined })
  const [atBottom, setAtBottom] = useState(true)

  const schedule = useCallback(() => {
    const flush = () => {
      frame.current = null
      const term = termRef.current
      if (!term) return
      const batch = queue.current.splice(0, CHUNK)
      if (batch.length) term.write(batch.join(''))
      if (queue.current.length) frame.current = requestAnimationFrame(flush)
    }
    if (frame.current === null) frame.current = requestAnimationFrame(flush)
  }, [])

  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    const term = new Terminal({
      disableStdin: true,
      cursorBlink: false,
      cursorStyle: 'underline',
      cursorInactiveStyle: 'none',
      convertEol: false,
      scrollback: 10_000,
      fontFamily: getComputedStyle(host).getPropertyValue('--font-mono') || 'monospace',
      fontSize: 13,
      theme: buildTerminalTheme(),
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(host)
    termRef.current = term

    let fitFrame: number | null = null
    const resize = new ResizeObserver(() => {
      if (fitFrame !== null) cancelAnimationFrame(fitFrame)
      fitFrame = requestAnimationFrame(() => {
        fitFrame = null
        try {
          fit.fit()
        } catch {
          // zero-size container while hidden
        }
      })
    })
    resize.observe(host)

    const themeWatch = new MutationObserver(() => {
      term.options.theme = buildTerminalTheme()
    })
    themeWatch.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['data-theme', 'class'],
    })

    const scroll = term.onScroll(() => {
      const b = term.buffer.active
      setAtBottom(b.viewportY >= b.baseY)
    })

    // The effect can run again (StrictMode): start the bookkeeping over.
    written.current = { seq: 0, count: 0, notice: undefined }
    queue.current = []

    return () => {
      if (frame.current !== null) cancelAnimationFrame(frame.current)
      frame.current = null
      if (fitFrame !== null) cancelAnimationFrame(fitFrame)
      resize.disconnect()
      themeWatch.disconnect()
      scroll.dispose()
      fit.dispose()
      term.dispose()
      termRef.current = null
      queue.current = []
    }
  }, [])

  useEffect(() => {
    const term = termRef.current
    if (!term) return
    const w = written.current
    // Older lines appeared (a refetch filled a gap) or the notice changed: redraw.
    const rewrite = countUpTo(lines, w.seq) > w.count || notice !== w.notice
    if (rewrite) {
      queue.current = []
      if (w.count > 0) term.reset()
      w.seq = 0
      w.count = 0
      w.notice = notice
      if (notice) queue.current.push(`\x1b[2m${notice}\x1b[0m\r\n`)
    }
    const fresh = lines.filter((l) => l.seq > w.seq)
    w.count = countUpTo(lines, w.seq) + fresh.length
    if (fresh.length) {
      w.seq = fresh[fresh.length - 1].seq
      for (const l of fresh) queue.current.push(`${l.line}\r\n`)
    }
    if (queue.current.length) schedule()
  }, [lines, notice, schedule])

  return (
    <div className="relative">
      <div
        ref={hostRef}
        data-testid="terminal-host"
        className="h-[60vh] min-h-64 overflow-hidden rounded-lg border border-border bg-background p-2"
      />
      {!atBottom && (
        <Button
          size="sm"
          variant="secondary"
          className="absolute right-4 bottom-4"
          onClick={() => termRef.current?.scrollToBottom()}
        >
          Jump to bottom
        </Button>
      )}
    </div>
  )
}
