import axe from 'axe-core'

/**
 * Fails when axe finds a violation in `container`. The color-contrast rule is
 * off because jsdom has no layout; contrast of the status tokens is covered by
 * styles/themes/contrast.test.ts.
 */
export async function expectNoA11yViolations(container: Element): Promise<void> {
  const results = await axe.run(container, {
    rules: { 'color-contrast': { enabled: false } },
  })
  if (results.violations.length > 0) {
    const report = results.violations
      .map((v) => `${v.id}: ${v.help}\n${v.nodes.map((n) => `  ${n.html}`).join('\n')}`)
      .join('\n')
    throw new Error(`Accessibility violations:\n${report}`)
  }
}
