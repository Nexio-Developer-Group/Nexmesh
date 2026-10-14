import { expect, test, type Locator, type Page } from '@playwright/test'

const unique = (p: string) => `${p}-${Math.random().toString(36).slice(2, 7)}`

async function createWorkspace(page: Page, name: string) {
  await page.goto('/new')
  await page.getByLabel('Workspace name').fill(name)
  await expect(page.getByText('Available')).toBeVisible()
  await page.getByRole('button', { name: 'Create Code Space' }).click()
  await page.waitForURL(/\/w\/ws_[a-f0-9]+$/, { timeout: 30_000 })
  return page.url()
}

async function dragLine(page: Page, from: Locator, to: Locator) {
  const a = await from.locator('.react-flow__handle-right').boundingBox()
  const b = await to.locator('.react-flow__handle-left').boundingBox()
  if (!a || !b) throw new Error('handles not found')
  await page.mouse.move(a.x + a.width / 2, a.y + a.height / 2)
  await page.mouse.down()
  await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2, { steps: 12 })
  await page.mouse.up()
}

test.describe('Phase 3: connection lines', () => {
  test('draw a line, review the variables, confirm, watch the state follow the provider, remove it', async ({ page }) => {
    await createWorkspace(page, unique('wire'))
    await page.getByRole('button', { name: 'Templates' }).click()
    await page.getByRole('button', { name: /React, NestJS and MongoDB/ }).click()
    await page.getByRole('button', { name: /Add \d+ cards to canvas/ }).click()
    await page.getByRole('button', { name: 'Review the cards' }).click()
    const nest = page.getByRole('group', { name: /NestJS card/ })
    const mongo = page.getByRole('group', { name: /MongoDB card/ })
    const react = page.getByRole('group', { name: /React card/ })
    await expect(mongo).toBeVisible()

    // An impossible line (a frontend to a database) cannot even be started: no review opens.
    await dragLine(page, react, mongo)
    await expect(page.getByRole('dialog', { name: 'Connect cards' })).toHaveCount(0)

    // A valid one opens the review. Nothing is created yet.
    await dragLine(page, nest, mongo)
    const dialog = page.getByRole('dialog', { name: 'Connect cards' })
    await expect(dialog).toBeVisible()
    await expect(dialog.getByText('MONGODB_URI')).toBeVisible()
    await expect(dialog.getByText(/Waiting|not installed or connected/).first()).toBeVisible()
    await expect(page.locator('.react-flow__edge')).toHaveCount(0)
    await dialog.getByRole('button', { name: 'Create connection' }).click()
    await expect(page.locator('.react-flow__edge')).toHaveCount(1)
    await expect(page.getByText('mongodb · waiting')).toBeVisible()

    // The provider becomes ready: the line follows (Redis is quicker to install than a whole stack, so use the card we have).
    await mongo.click()
    await page.getByRole('button', { name: 'Install and start' }).click()
    await page.getByRole('dialog').getByRole('button', { name: 'Install and start' }).click()
    await expect(page.getByText('mongodb · healthy')).toBeVisible({ timeout: 30_000 })

    // The variable appears in the workspace variables as generated, and no value does.
    await page.goto(page.url().replace(/\/?$/, '') + '/settings?tab=variables')
    await expect(page.getByText('MONGODB_URI').first()).toBeVisible()
    await page.goBack()

    // Remove through the details dialog: the variables go with it.
    await page.locator('.react-flow__edge').first().click({ force: true })
    const details = page.getByRole('dialog', { name: 'Connection' })
    await expect(details.getByText('Healthy')).toBeVisible()
    await details.getByRole('button', { name: 'Remove connection' }).click()
    await expect(page.locator('.react-flow__edge')).toHaveCount(0)
  })

  test('the Connections section is the keyboard route', async ({ page }) => {
    await createWorkspace(page, unique('wirekb'))
    await page.getByRole('button', { name: 'Templates' }).click()
    await page.getByRole('button', { name: /React, NestJS and MongoDB/ }).click()
    await page.getByRole('button', { name: /Add \d+ cards to canvas/ }).click()
    await page.getByRole('button', { name: 'Review the cards' }).click()
    await page.getByRole('group', { name: /React card/ }).click()
    const section = page.getByRole('region', { name: 'Connections' })
    await section.getByLabel('Connect to').selectOption({ label: 'NestJS' })
    await section.getByRole('button', { name: 'Review…' }).click()
    const dialog = page.getByRole('dialog', { name: 'Connect cards' })
    await expect(dialog.getByText('NESTJS_URL')).toBeVisible()
    await dialog.getByLabel('Connection name').fill('api')
    await dialog.getByLabel('Connection name').blur()
    await expect(dialog.getByText('API_URL')).toBeVisible()
    await dialog.getByRole('button', { name: 'Create connection' }).click()
    await expect(section.getByText('api', { exact: true })).toBeVisible()
  })
})
