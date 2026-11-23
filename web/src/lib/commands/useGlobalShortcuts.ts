import { useEffect, useRef } from 'react'
import { shortcuts, type ShortcutId } from './shortcuts'

const EDITABLE = 'input, textarea, select, [contenteditable]:not([contenteditable="false"])'
const DIALOG = '[role="dialog"], [role="alertdialog"]'

/**
 * One keydown listener on the document. Key repeat is ignored. While a dialog
 * is open only the palette toggle works, and only when the palette is the
 * dialog (so it closes itself). Shortcuts that are not `inEditable` are also
 * ignored while an input, textarea, select or contenteditable has focus; that
 * covers xterm, whose helper is a textarea.
 */
export function useGlobalShortcuts(paletteOpen: boolean, onShortcut: (id: ShortcutId) => void) {
  const latest = useRef({ paletteOpen, onShortcut })
  useEffect(() => {
    latest.current = { paletteOpen, onShortcut }
  })

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.repeat) return
      const shortcut = shortcuts.find((s) => s.matches(e))
      if (!shortcut || shortcut.id === 'toasts') return
      const { paletteOpen: open, onShortcut: fire } = latest.current
      const dialogOpen = document.querySelector(DIALOG) !== null
      if (shortcut.id === 'palette') {
        if (dialogOpen && !open) return
      } else if (dialogOpen) {
        return
      }
      const target = e.target instanceof Element ? e.target : null
      if (!shortcut.inEditable && target?.closest(EDITABLE)) return
      e.preventDefault()
      fire(shortcut.id)
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [])
}
