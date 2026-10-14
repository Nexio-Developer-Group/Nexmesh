import { expect, test, type Page } from '@playwright/test'

const unique = (p: string) => `${p}-${Math.random().toString(36).slice(2, 7)}`

async function createWorkspace(page: Page, name: string) {
  await page.goto('/new')
  await page.getByLabel('Workspace name').fill(name)
  await expect(page.getByText('Available')).toBeVisible()
  await page.getByRole('button', { name: 'Create Code Space' }).click()
  await page.waitForURL(/\/w\/ws_[a-f0-9]+$/, { timeout: 30_000 })
  return page.url()
}

test.describe('Phase 2 workspace tools', () => {
  test('a stack template adds draft cards and installs nothing', async ({ page }) => {
    await createWorkspace(page, unique('tpl'))
    await page.getByRole('button', { name: 'Templates' }).click()
    await page.getByRole('button', { name: /FastAPI with Redis/ }).click()
    await expect(page.getByText(/Nothing installs automatically/)).toBeVisible()
    await page.getByRole('button', { name: /Add 3 cards to canvas/ }).click()
    await expect(page.getByRole('status')).toContainText('Added 3 cards as drafts')
    await page.getByRole('button', { name: 'Review the cards' }).click()
    for (const tech of ['Python', 'FastAPI', 'Redis']) {
      const card = page.getByRole('group', { name: new RegExp(`${tech} card`) })
      await expect(card).toBeVisible()
      await expect(card.getByText('Draft')).toBeVisible()
    }
  })

  test('variables keep secrets write-only and the activity log records the change', async ({ page }) => {
    const url = await createWorkspace(page, unique('vars'))
    await page.goto(url + '/settings?tab=variables')
    await page.getByRole('button', { name: 'Add variable' }).click()
    await page.getByLabel('Name').fill('STRIPE_KEY')
    await page.getByLabel(/Secret/).check()
    await page.getByLabel('Value', { exact: true }).fill('sk_live_E2E_SECRET_VALUE')
    await page.getByRole('button', { name: 'Save variable' }).click()
    const row = page.getByRole('row', { name: /STRIPE_KEY/ })
    await expect(row.getByText('Secret')).toBeVisible()
    await expect(page.locator('body')).not.toContainText('sk_live_E2E_SECRET_VALUE')

    await page.getByRole('button', { name: 'Activity', exact: true }).click()
    await expect(page.getByText('variable.created', { exact: true })).toBeVisible()
    await expect(page.locator('body')).not.toContainText('sk_live_E2E_SECRET_VALUE')
  })

  test('snapshot, change, restore: the files come back', async ({ page }) => {
    const name = unique('snap')
    const url = await createWorkspace(page, name)
    const card = page.getByRole('article', { name })
    await page.getByRole('tab', { name: 'Code' }).click()
    await expect(page.getByRole('tree', { name: 'Workspace files' }).getByText('README.md')).toBeVisible({ timeout: 20_000 })
    const term = page.getByRole('region', { name: /Terminal 1/ })
    await expect(term.locator('.xterm')).toBeVisible()
    await term.locator('.xterm-helper-textarea').focus()
    await page.keyboard.type('echo state-one > state.txt\n')

    await page.goto(url + '/settings?tab=snapshots')
    await page.getByRole('button', { name: 'Create snapshot' }).click()
    await page.getByLabel('Name').fill('first')
    await page.getByRole('dialog').getByRole('button', { name: 'Create snapshot' }).click()
    await expect(page.getByText(/KB|MB/).first()).toBeVisible({ timeout: 20_000 })

    // Change the file, then stop the workspace and restore.
    await page.goto(url)
    await page.getByRole('tab', { name: 'Code' }).click()
    const t2 = page.getByRole('region', { name: /Terminal 1/ })
    await expect(t2.locator('.xterm')).toBeVisible()
    await t2.locator('.xterm-helper-textarea').focus()
    await page.keyboard.type('echo state-two > state.txt\n')
    await page.goto('/workspaces')
    await card.getByRole('button', { name: `Actions for ${name}` }).click()
    await page.getByRole('menuitem', { name: 'Stop' }).click()
    await expect(card.getByText('Stopped')).toBeVisible({ timeout: 20_000 })

    await page.goto(url + '/settings?tab=snapshots')
    await page.getByRole('button', { name: 'Restore first' }).click()
    await page.getByRole('button', { name: 'Replace files with this snapshot' }).click()
    await page.goto('/workspaces')
    await card.getByRole('button', { name: 'Start' }).click()
    await expect(card.getByRole('link', { name: 'Open' })).toBeVisible({ timeout: 30_000 })
    await page.goto(url)
    await page.getByRole('tab', { name: 'Code' }).click()
    await page.getByRole('tree', { name: 'Workspace files' }).getByText('state.txt').click()
    await expect(page.locator('.monaco-editor').first()).toContainText('state-one', { timeout: 20_000 })
  })

  test('the definition exports without secrets and the drift check reports no drift', async ({ page }) => {
    const url = await createWorkspace(page, unique('def'))
    await page.goto(url + '/settings?tab=definition')
    const [download] = await Promise.all([page.waitForEvent('download'), page.getByRole('link', { name: 'Download YAML' }).click()])
    const stream = await download.createReadStream()
    let text = ''
    for await (const chunk of stream) text += chunk
    expect(text).toContain('kind: NexmeshWorkspace')
    expect(text).toContain('Contains no secret values')

    await page.getByRole('textbox', { name: 'Definition' }).fill(text)
    await page.getByRole('button', { name: 'Check for drift' }).click()
    await expect(page.getByRole('status')).toContainText('In sync')
  })
})
