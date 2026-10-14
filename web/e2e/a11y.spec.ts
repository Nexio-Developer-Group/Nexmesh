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

test('Phase 2 screens have no serious accessibility violations', async ({ page }) => {
  await page.goto('/new')
  await page.getByLabel('Workspace name').fill('a11y-p2')
  await page.getByRole('button', { name: 'Create Code Space' }).click()
  await page.waitForURL(/\/w\/ws_[a-f0-9]+$/, { timeout: 30_000 })
  const url = page.url()

  // Code view: file tree, editor tabs, terminal, git and ports panels.
  await page.getByRole('tab', { name: 'Code' }).click()
  await expect(page.getByRole('tree', { name: 'Workspace files' }).getByText('README.md')).toBeVisible({ timeout: 20_000 })
  await page.getByRole('tree', { name: 'Workspace files' }).getByText('README.md').click()
  await expect(page.locator('.monaco-editor').first()).toBeVisible({ timeout: 20_000 })
  await expect(page.getByRole('region', { name: /Terminal 1/ }).locator('.xterm')).toBeVisible()
  await check(page, 'code view')
  await page.getByRole('tab', { name: 'Source control' }).click()
  await check(page, 'source control')
  await page.getByRole('tab', { name: 'Ports' }).click()
  await check(page, 'ports')

  // Canvas dialogs.
  await page.goto(url)
  await page.getByRole('button', { name: 'Templates' }).click()
  await page.getByRole('button', { name: /React, NestJS and MongoDB/ }).click()
  await check(page, 'templates dialog')
  await page.keyboard.press('Escape')
  await page.getByRole('button', { name: 'Import connection' }).click()
  await check(page, 'import connection dialog')
  await page.keyboard.press('Escape')

  // Every settings tab.
  await page.goto(url + '/settings')
  for (const tab of ['Variables', 'Git credentials', 'Snapshots', 'Definition', 'Activity']) {
    await page.getByRole('button', { name: tab, exact: true }).click()
    await expect(page.getByRole('heading', { name: tab === 'Git credentials' ? 'Git credentials' : tab, level: 2 })).toBeVisible()
    await check(page, `settings ${tab}`)
  }
  await page.getByRole('button', { name: 'Variables', exact: true }).click()
  await page.getByRole('button', { name: 'Add variable' }).click()
  await check(page, 'add variable dialog')
})
