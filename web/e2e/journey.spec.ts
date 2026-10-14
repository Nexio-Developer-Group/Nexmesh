import { expect, test, type Page } from '@playwright/test'
import { KEY, startFakeModel } from './fake-model.mjs'

const unique = (p: string) => `${p}-${Math.random().toString(36).slice(2, 7)}`

async function createWorkspace(page: Page, name: string, opts: { cpu?: number } = {}) {
  await page.goto('/workspaces')
  await page.getByRole('link', { name: 'Create Code Space' }).first().click()
  await page.getByLabel('Workspace name').fill(name)
  await expect(page.getByText('Available')).toBeVisible()
  if (opts.cpu) await page.getByRole('spinbutton').nth(1).fill(String(opts.cpu))
  await page.getByRole('button', { name: 'Create Code Space' }).click()
  // The canvas opens only once provisioning reports Ready (PRD 6.3).
  await expect(page.getByRole('heading', { name: /Setting up your code space|Workspace ready/ })).toBeVisible()
  await page.waitForURL(new RegExp(`/w/ws_[a-f0-9]+$`), { timeout: 30_000 })
  await expect(page.getByRole('navigation', { name: 'Breadcrumb' }).getByText(name)).toBeVisible()
}

async function dropOnCanvas(page: Page, tech: string) {
  await expect(page.getByRole('button', { name: 'Zoom in' })).toBeVisible() // the canvas is initialised
  const src = page.getByRole('button', { name: `Add ${tech} to canvas` })
  const canvas = page.getByRole('main', { name: 'Canvas' })
  const box = (await canvas.boundingBox())!
  await src.dragTo(canvas, { targetPosition: { x: box.width / 2, y: box.height / 2 } })
}

test.describe('Phase 1 journey', () => {
  test('empty dashboard, create a code space, drag Redis, install and start it', async ({ page }) => {
    const name = unique('orders')
    await page.goto('/workspaces')
    await createWorkspace(page, name)

    await expect(page.getByText('Drag a technology here')).toBeVisible()
    await dropOnCanvas(page, 'Redis')
    const card = page.getByRole('group', { name: /Redis card/ })
    await expect(card).toBeVisible()
    await expect(card.getByText('Draft')).toBeVisible()
    // The properties panel opens with Install and Connect tabs for service components.
    await expect(page.getByRole('tab', { name: 'install' })).toBeVisible()
    await expect(page.getByRole('tab', { name: 'connect' })).toBeVisible()

    await page.getByRole('button', { name: 'Install and start' }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toContainText('Install and start Redis')
    await dialog.getByRole('button', { name: 'Install and start' }).click()
    await expect(card.getByText('Running')).toBeVisible({ timeout: 20_000 })
    await expect(card.getByText('Installed')).toBeVisible()
    // After a reload the card still reflects real state.
    await page.reload()
    await expect(page.getByRole('group', { name: /Redis card/ }).getByText('Running')).toBeVisible()
  })

  test('install-only components show no Install/Connect tabs', async ({ page }) => {
    await createWorkspace(page, unique('tabs'))
    await page.getByRole('button', { name: 'Add NestJS to canvas' }).click()
    await expect(page.getByRole('complementary', { name: 'NestJS properties' })).toBeVisible()
    await expect(page.getByRole('complementary', { name: 'NestJS properties' }).getByRole('tab')).toHaveCount(0)
    await page.getByRole('button', { name: 'Install', exact: true }).first().click()
    // Requires Node.js first: the error is specific.
    await page.getByRole('dialog').getByRole('button', { name: 'Install', exact: true }).click()
    await expect(page.getByText('NestJS requires Node.js. Install Node.js first.').first()).toBeVisible()
  })

  test('runtimes can be installed from the Runtimes panel', async ({ page }) => {
    await createWorkspace(page, unique('rt'))
    await page.getByRole('button', { name: 'Runtimes' }).click()
    const py = page.getByRole('region', { name: 'Python runtime' })
    await py.getByRole('button', { name: 'Install version' }).click()
    await expect(py.getByText(/\d+\.\d+\.\d+ active/)).toBeVisible({ timeout: 20_000 })
    await page.getByRole('button', { name: 'Close' }).click()
    await expect(page.getByRole('complementary', { name: 'Technology catalog' }).getByText(/3\.\d+\.\d+ active/)).toBeVisible()
  })

  test('Workspace Settings: OS is locked, CPU applies live, storage needs a confirmed restart', async ({ page }) => {
    const name = unique('settings')
    await createWorkspace(page, name)
    await page.getByRole('link', { name: 'Workspace settings' }).click()
    await expect(page.getByText('Locked')).toBeVisible()
    await expect(page.getByText('Changing the operating system requires creating a new workspace.')).toBeVisible()

    await page.getByRole('button', { name: 'Resources' }).click()
    await page.getByRole('spinbutton').nth(1).fill('6')
    const review = page.getByRole('region', { name: 'Review changes' })
    await expect(review.getByText('applies immediately')).toBeVisible()
    await review.getByRole('button', { name: 'Apply changes' }).click()
    await expect(page.getByRole('region', { name: 'Review changes' })).toHaveCount(0)

    await page.getByRole('spinbutton').nth(2).fill('80')
    await expect(review.getByText('needs restart')).toBeVisible()
    await review.getByRole('button', { name: 'Apply and restart' }).click()
    await page.getByRole('dialog').getByRole('button', { name: 'Restart and apply' }).click()
    await expect(page.getByRole('region', { name: 'Review changes' })).toHaveCount(0)
  })

  test('Code Agent: add a local model, test it, and Save unlocks only after a pass', async ({ page }) => {
    const srv = await startFakeModel()
    try {
      await createWorkspace(page, unique('agent'))
      await page.getByRole('link', { name: 'Workspace settings' }).click()
      await page.getByRole('button', { name: 'Code Agent' }).click()
      await page.getByRole('button', { name: 'Add configuration' }).click()
      await page.getByRole('radio', { name: /Custom or local/ }).click()
      await page.getByLabel('Configuration name').fill('Local Qwen coder')
      await page.getByLabel('Endpoint base URL').fill('http://127.0.0.1:19998')
      await expect(page.getByText(/Private address\./)).toBeVisible()
      await page.getByLabel('Model ID').fill('qwen2.5-coder:32b')
      await page.getByLabel(/API key or token/).fill(KEY)
      const save = page.getByRole('button', { name: 'Save' })
      await expect(save).toBeDisabled()
      await page.getByRole('button', { name: 'Test Configuration' }).click()
      await expect(page.getByText('✓ Passed')).toBeVisible()
      await expect(save).toBeEnabled()
      await page.getByLabel('Model ID').fill('qwen2.5-coder:7b')
      await expect(save).toBeDisabled()
      await page.getByLabel('Model ID').fill('qwen2.5-coder:32b')
      await page.waitForTimeout(1100) // tests are rate limited to one per second
      await page.getByRole('button', { name: 'Test Configuration' }).click()
      await expect(save).toBeEnabled()
      await save.click()
      await expect(page.getByRole('cell', { name: /Local Qwen coder/ })).toBeVisible()
      await expect(page.getByText('Default')).toBeVisible()
      // The key is never shown again.
      await expect(page.locator('body')).not.toContainText(KEY)
    } finally {
      srv.close()
    }
  })

  test('Advanced Setup blocks creation only when started and untested', async ({ page }) => {
    await page.goto('/new')
    await page.getByLabel('Workspace name').fill(unique('adv'))
    const create = page.getByRole('button', { name: 'Create Code Space' })
    await expect(create).toBeEnabled()
    await page.getByRole('button', { name: 'Expand' }).click()
    await page.getByRole('radio', { name: /Anthropic/ }).click()
    await page.getByLabel('API key').fill('sk-ant-not-tested-0000')
    await expect(create).toBeDisabled()
    await expect(page.getByText(/started configuring a code agent/)).toBeVisible()
    await page.getByRole('button', { name: 'Clear section' }).click()
    await expect(create).toBeEnabled()
  })

  test('resource limits are explained on the create screen', async ({ page }) => {
    await page.goto('/new')
    await page.getByLabel('Workspace name').fill(unique('limits'))
    await page.getByRole('spinbutton').first().fill('99')
    await expect(page.getByRole('alert').first()).toContainText('RAM must be between 1 and')
    await expect(page.getByRole('button', { name: 'Create Code Space' })).toBeDisabled()
  })

  test('dashboard lists workspaces, filters, and deleting requires typing the name', async ({ page }) => {
    const name = unique('del')
    await createWorkspace(page, name)
    await page.goto('/workspaces')
    await expect(page.getByRole('article', { name })).toBeVisible()
    await page.getByRole('tab', { name: /Running/ }).click()
    await expect(page.getByRole('article', { name })).toBeVisible()
    await page.getByRole('button', { name: `Actions for ${name}` }).click()
    await page.getByRole('menuitem', { name: 'Delete…' }).click()
    const del = page.getByRole('button', { name: 'Delete workspace' })
    await expect(del).toBeDisabled()
    await page.getByLabel(`Type ${name} to confirm`).fill(name)
    await del.click()
    await expect(page.getByRole('article', { name })).toHaveCount(0)
  })
})

// Connect to a real Redis when one is provided (see scripts/it/start-services.sh).
const redisPort = process.env.NX_IT_REDIS_PORT
const redisPass = process.env.NX_IT_PASS
test.describe('Connect mode against real Redis', () => {
  test.skip(!redisPort || !redisPass, 'set NX_IT_REDIS_PORT and NX_IT_PASS (a Redis with a password; see scripts/it/env.sh) to run')

  test('test, fail, fix, add, re-test and disconnect', async ({ page }) => {
    await createWorkspace(page, unique('conn'))
    await page.getByRole('button', { name: 'Add Redis to canvas' }).click()
    await page.getByRole('tab', { name: 'connect' }).click()
    await page.getByLabel('Connection name').fill('shared-cache')
    await page.getByRole('radiogroup', { name: 'Environment', exact: true }).getByRole('radio', { name: 'staging' }).click()
    await page.getByLabel(/^Host/).fill('127.0.0.1')
    await page.getByLabel(/^Port/).fill(redisPort!)
    await page.getByText('Advanced').click() // Redis credentials and TLS live under Advanced
    await page.getByRole('switch', { name: /^TLS/ }).uncheck() // the local test server is plaintext
    await page.getByLabel(/^Password/).fill('definitely-wrong')
    const add = page.getByRole('button', { name: 'Add to Workspace' })
    await expect(add).toBeDisabled()
    await page.getByRole('button', { name: 'Test Connection' }).click()
    await expect(page.getByText(/Host reachable but authentication failed/).first()).toBeVisible()
    await expect(add).toBeDisabled()
    // The entered values are kept; fix the password and retest.
    await expect(page.getByLabel(/^Host/)).toHaveValue('127.0.0.1')
    await page.getByLabel(/^Password/).fill(redisPass!)
    await expect(add).toBeDisabled() // editing invalidated the earlier result
    await page.waitForTimeout(1100) // test rate limit
    await page.getByRole('button', { name: 'Retest' }).click()
    await expect(page.getByRole('status').filter({ hasText: /Passed · 7 of 7 checks/ })).toBeVisible()
    await expect(add).toBeEnabled()
    await add.click()
    await expect(page.getByText(/Last check passed/).first()).toBeVisible()
    const card = page.getByRole('group', { name: /shared-cache|Redis card/ })
    await expect(card.getByText('Connected').first()).toBeVisible()
    await expect(page.getByText('SHARED_CACHE_URI')).toBeVisible()
    // The password is never displayed.
    await expect(page.locator('body')).not.toContainText(redisPass!)
    await page.getByRole('button', { name: 'Disconnect' }).click()
    await page.getByRole('dialog').getByRole('button', { name: 'Disconnect' }).click()
    await expect(page.getByText('Disconnected').first()).toBeVisible()
  })
})
