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

test('Phase 3 screens have no serious accessibility violations', async ({ page }) => {
  await page.goto('/new')
  await page.getByLabel('Workspace name').fill('a11y-p3')
  await page.getByRole('button', { name: 'Create Code Space' }).click()
  await page.waitForURL(/\/w\/ws_[a-f0-9]+$/, { timeout: 30_000 })
  const url = page.url()
  const ws = url.split('/w/')[1]
  await expect.poll(async () => (await page.request.get(`/api/workspaces/${ws}/files`)).status(), { timeout: 20_000 }).toBe(200)

  // Architecture view and the agent panel with its three sections.
  await page.getByRole('tab', { name: 'Architecture' }).click()
  await check(page, 'architecture view')
  await page.getByRole('tab', { name: 'Canvas' }).click()
  await page.getByRole('button', { name: 'Code Agent' }).click()
  await check(page, 'agent chat')
  const panel = page.getByRole('complementary', { name: 'Code Agent' })
  await panel.getByRole('tab', { name: 'Settings' }).click()
  await check(page, 'agent settings')
  await panel.getByRole('button', { name: 'Show what the agent can see' }).click()
  await expect(page.getByLabel('Agent context')).toBeVisible()
  await check(page, 'agent context dialog')
  await page.keyboard.press('Escape')

  // A change set open for review, with a file and diff.
  await page.request.put(`/api/workspaces/${ws}/files/content`, { data: { path: 'a.txt', content: 'one\ntwo\nthree\n' } })
  const made = await page.request.post(`/api/workspaces/${ws}/changes`, { data: { title: 'Edit a', changes: [{ path: 'a.txt', content: 'one\n2\nthree\n' }, { path: 'b/new.txt', content: 'x\n' }] } })
  expect(made.status()).toBe(201)
  await panel.getByRole('tab', { name: 'Changes' }).click()
  await check(page, 'agent changes')
  await panel.getByRole('button', { name: 'Review Edit a' }).click()
  await expect(page.getByRole('dialog', { name: 'Edit a' }).getByText('b/new.txt')).toBeVisible()
  await check(page, 'change review dialog')
  await page.keyboard.press('Escape')

  // Explain dialog for a card.
  await page.getByRole('button', { name: 'Add Redis to canvas' }).click()
  await page.getByRole('button', { name: 'Explain' }).click()
  await expect(page.getByLabel('Text that will be sent')).toBeVisible()
  await check(page, 'explain dialog')
  await page.keyboard.press('Escape')

  // Rules and gates settings, with their dialogs.
  await page.goto(url + '/settings?tab=rules')
  await expect(page.getByRole('heading', { name: 'Agent rules' })).toBeVisible()
  await check(page, 'rules')
  await page.getByRole('button', { name: 'New rule' }).click()
  await check(page, 'new rule dialog')
  await page.keyboard.press('Escape')
  await page.goto(url + '/settings?tab=gates')
  await expect(page.getByRole('heading', { name: 'Quality gates' })).toBeVisible()
  await page.getByRole('button', { name: 'Add a gate' }).click()
  await check(page, 'quality gates')
})
