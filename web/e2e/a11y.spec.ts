import AxeBuilder from '@axe-core/playwright'
import { expect, test } from '@playwright/test'

// WCAG 2 A/AA automated checks on the main screens. Automated checks catch a
// subset of issues; keyboard and screen-reader review is still needed.
async function check(page: import('@playwright/test').Page, label: string) {
  const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze()
  const serious = results.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical')
  expect(serious.map((v) => `${label}: ${v.id} (${v.nodes.length}) ${v.help}`), `${label} accessibility`).toEqual([])
}

test('dashboard, create, canvas and settings have no serious accessibility violations', async ({ page }) => {
  await page.goto('/workspaces')
  await check(page, 'dashboard')
  await page.goto('/new')
  await page.getByLabel('Workspace name').fill('a11y-check')
  await check(page, 'create')
  await page.getByRole('button', { name: 'Create Code Space' }).click()
  await page.waitForURL(/\/w\/ws_[a-f0-9]+$/, { timeout: 30_000 })
  await page.getByRole('button', { name: 'Add MongoDB to canvas' }).click()
  await page.getByRole('tab', { name: 'connect' }).click()
  await check(page, 'canvas with properties panel')
  await page.getByRole('link', { name: 'Workspace settings' }).click()
  await check(page, 'settings general')
  await page.getByRole('button', { name: 'Code Agent' }).click()
  await check(page, 'settings code agent')
})
