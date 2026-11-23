import type { ITheme } from '@xterm/xterm'

// xterm needs concrete colors, so the palette is read from the active theme's
// CSS variables at runtime. No color literals live here.
const MAP: Partial<Record<keyof ITheme, string>> = {
  background: '--background',
  foreground: '--foreground',
  cursor: '--background',
  cursorAccent: '--background',
  selectionBackground: '--accent',
  black: '--card',
  red: '--destructive',
  green: '--success',
  yellow: '--warning',
  blue: '--info',
  magenta: '--primary',
  cyan: '--chart-3',
  white: '--foreground',
  brightBlack: '--muted-foreground',
  brightRed: '--destructive',
  brightGreen: '--success',
  brightYellow: '--warning',
  brightBlue: '--info',
  brightMagenta: '--primary',
  brightCyan: '--chart-3',
  brightWhite: '--foreground',
}

export function buildTerminalTheme(el: Element = document.documentElement): ITheme {
  const style = getComputedStyle(el)
  const theme: Record<string, string> = {}
  for (const [key, variable] of Object.entries(MAP)) {
    const value = style.getPropertyValue(variable).trim()
    if (value) theme[key] = value
  }
  return theme as ITheme
}
