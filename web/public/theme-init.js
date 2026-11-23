// Classic blocking script: runs before first paint to avoid a theme flash.
// Keep in sync with src/lib/theme.ts (storage key, theme ids, dark ids).
;(function () {
  var root = document.documentElement
  var id = 'mocha'
  var dark = true
  try {
    var stored = null
    try {
      stored = localStorage.getItem('colima-ui-theme')
    } catch (e) {}
    if (stored === 'light' || stored === 'mocha') {
      id = stored
    } else if (window.matchMedia) {
      id = window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'mocha'
    }
    dark = id !== 'light'
  } catch (e) {}
  root.setAttribute('data-theme', id)
  root.classList.toggle('dark', dark)
  root.style.colorScheme = dark ? 'dark' : 'light'
})()
