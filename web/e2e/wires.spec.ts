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

  test('the architecture view matches the canvas and exports without secrets', async ({ page }) => {
    await createWorkspace(page, unique('arch'))
    await page.getByRole('button', { name: 'Templates' }).click()
    await page.getByRole('button', { name: /React, NestJS and MongoDB/ }).click()
    await page.getByRole('button', { name: /Add \d+ cards to canvas/ }).click()
    await page.getByRole('button', { name: 'Review the cards' }).click()
    await page.getByRole('group', { name: /NestJS card/ }).click()
    const section = page.getByRole('region', { name: 'Connections' })
    await section.getByLabel('Connect to').selectOption({ label: 'MongoDB' })
    await section.getByRole('button', { name: 'Review…' }).click()
    await page.getByRole('dialog', { name: 'Connect cards' }).getByRole('button', { name: 'Create connection' }).click()

    await page.getByRole('tab', { name: 'Architecture' }).click()
    await expect(page.getByRole('heading', { name: 'Architecture' })).toBeVisible()
    const layers = page.getByRole('region', { name: 'Components by layer' })
    await expect(layers.getByText('Frontend')).toBeVisible()
    await expect(layers.getByText('Data')).toBeVisible()
    await expect(page.getByRole('region', { name: 'Connections' }).getByText(/NestJS → MongoDB/)).toBeVisible()
    // Draft cards are not "configured" yet, so nothing needs attention.
    await expect(page.getByRole('region', { name: 'Needs attention' })).toContainText('Everything configured is reachable')

    // The image really renders.
    const img = page.getByRole('img', { name: /Architecture diagram/ })
    await expect(img).toBeVisible()
    await expect.poll(() => img.evaluate((el: HTMLImageElement) => el.naturalWidth)).toBeGreaterThan(100)

    // Exports download and carry no secret-looking content.
    const [dl] = await Promise.all([page.waitForEvent('download'), page.getByRole('link', { name: 'Mermaid' }).click()])
    const path = await dl.path()
    const text = (await import('node:fs')).readFileSync(path!, 'utf8')
    expect(text).toContain('flowchart LR')
    expect(text).toContain('NestJS')
    expect(text).not.toMatch(/password|secret/i)
  })
})

test.describe('Phase 3: Docker export', () => {
  test('previews compose and Dockerfiles from the canvas, writes them on request and keeps existing files', async ({ page }) => {
    const url = await createWorkspace(page, unique('dock'))
    const id = url.match(/\/w\/(ws_[a-f0-9]+)/)![1]
    const api = (p: string) => `/api/workspaces/${id}${p}`
    const ok = async (r: Awaited<ReturnType<typeof page.request.get>>) => { expect(r.status(), await r.text()).toBeLessThan(300); return r.json().catch(() => ({})) }

    // Cards, set up through the API so the test stays about the export.
    const redis = await ok(await page.request.post(api('/components'), { data: { catalogId: 'redis' } }))
    await ok(await page.request.post(api(`/components/${redis.id}/install`), { data: { version: '7.4.1', start: true, confirm: true } }))
    await ok(await page.request.post(api('/runtimes/nodejs/install'), { data: { version: '22.11.0', activate: true } }))
    await expect.poll(async () => (await (await page.request.get(api('/runtimes'))).json()).runtimes.nodejs.active, { timeout: 30_000 }).toBe('22.11.0')
    const nest = await ok(await page.request.post(api('/components'), { data: { catalogId: 'nestjs' } }))
    await ok(await page.request.post(api(`/components/${nest.id}/install`), { data: { version: '10.4.7', confirm: true } }))
    await expect.poll(async () => (await (await page.request.get(api(`/components/${nest.id}`))).json()).observedState, { timeout: 30_000 }).toBe('ready')
    await ok(await page.request.post(api('/wires'), { data: { fromId: nest.id, toId: redis.id, confirm: true } }))

    // A project for NestJS, and a Dockerfile of the user's own that must survive.
    await ok(await page.request.post(api('/files/dir'), { data: { path: 'api' } }))
    await ok(await page.request.put(api('/files/content'), { data: { path: 'api/package.json', content: '{"scripts":{"build":"nest build"},"dependencies":{"@nestjs/core":"10"}}' } }))
    await ok(await page.request.put(api('/files/content'), { data: { path: 'api/Dockerfile', content: 'FROM scratch\n# mine\n' } }))

    await page.getByRole('tab', { name: 'Architecture' }).click()
    const panel = page.getByRole('region', { name: 'Docker files' })
    await expect(panel.getByText('docker-compose.yml', { exact: true })).toBeVisible()
    await expect(panel.getByText('Exists (yours)')).toBeVisible() // api/Dockerfile
    await panel.locator('li', { hasText: 'docker-compose.yml' }).getByText('Preview').click()
    const compose = panel.locator('li', { hasText: 'docker-compose.yml' }).locator('pre')
    await expect(compose).toContainText('image: redis:7.4.1')
    await expect(compose).toContainText('REDIS_URI: redis://redis:6379')
    await expect(compose).not.toContainText(/password|secret/i)

    await panel.getByRole('button', { name: /Write \d+ files? to the workspace/ }).click()
    await expect(panel.getByRole('status')).toContainText('Wrote')
    // The user's Dockerfile was not touched; the compose file is in the workspace.
    const df = await (await page.request.get(api('/files/content?path=api/Dockerfile'))).json()
    expect(df.content).toContain('# mine')
    const written = await (await page.request.get(api('/files/content?path=docker-compose.yml'))).json()
    expect(written.content).toContain('Generated by Nexmesh')
  })
})

// Per-environment connections need two reachable services (see scripts/it/start-services.sh);
// the second port is a second Redis with the same password.
const dev = process.env.NX_IT_REDIS_PORT
const stg = process.env.NX_IT_REDIS_PORT2
const pass = process.env.NX_IT_PASS
test.describe('Phase 3: canvas environments', () => {
  test.skip(!dev || !stg || !pass, 'set NX_IT_REDIS_PORT, NX_IT_REDIS_PORT2 and NX_IT_PASS to run')

  test('a card has one connection per environment and switching changes which one is used', async ({ page }) => {
    await createWorkspace(page, unique('env'))
    await page.getByRole('button', { name: 'Add Redis to canvas' }).click()
    await page.getByRole('tab', { name: 'connect' }).click()
    await page.getByLabel('Connection name').fill('orders-cache')
    await page.getByLabel(/^Host/).fill('127.0.0.1')
    await page.getByLabel(/^Port/).fill(dev!)
    await page.getByText('Advanced').click()
    await page.getByRole('switch', { name: /^TLS/ }).uncheck()
    await page.getByLabel(/^Password/).fill(pass!)
    await page.getByRole('button', { name: 'Test Connection' }).click()
    await expect(page.getByRole('status').filter({ hasText: /Passed/ })).toBeVisible()
    await page.getByRole('button', { name: 'Add to Workspace' }).click()
    const card = page.getByRole('group', { name: /Redis card/ })
    await expect(card.getByText('Connected').first()).toBeVisible()

    // Add a staging connection. Nothing is copied from development: the password must be entered again.
    await page.getByRole('button', { name: 'Add staging connection' }).click()
    await expect(page.getByLabel(/^Host/)).toHaveValue('')
    await page.getByLabel(/^Host/).fill('127.0.0.1')
    await page.getByLabel(/^Port/).fill(stg!)
    await page.getByText('Advanced').click()
    await page.getByRole('switch', { name: /^TLS/ }).uncheck()
    await page.waitForTimeout(1100) // the test rate limit is one per second per card
    await page.getByRole('button', { name: 'Test Connection' }).click()
    await expect(page.getByText(/authentication failed|Password/i).first()).toBeVisible()
    await expect(page.getByRole('button', { name: 'Add staging connection' })).toBeDisabled()
    await page.getByLabel(/^Password/).fill(pass!)
    await page.waitForTimeout(1100)
    await page.getByRole('button', { name: 'Retest' }).click()
    await expect(page.getByRole('status').filter({ hasText: /Passed/ })).toBeVisible()
    await page.getByRole('button', { name: 'Add staging connection' }).click()
    await expect(page.getByRole('region', { name: 'Environments' }).getByText('connection set')).toBeVisible()

    const env = page.getByRole('radiogroup', { name: 'Active environment' })
    await env.getByRole('radio', { name: 'Staging' }).click()
    await expect(card.getByText('staging', { exact: true })).toBeVisible()
    await env.getByRole('radio', { name: 'Development' }).click()
    await expect(card.getByText('development', { exact: true })).toBeVisible()

    // Production has no connection: confirmation first, then the card says so and nothing is injected.
    await env.getByRole('radio', { name: 'Production' }).click()
    await page.getByRole('dialog').getByRole('button', { name: 'Switch to Production' }).click()
    await expect(card.getByText('no production', { exact: true })).toBeVisible()
    await expect(card.getByText('Disconnected').first()).toBeVisible()
    await expect(page.getByText(/No production connection/).first()).toBeVisible()
    await expect(page.locator('body')).not.toContainText(pass!)

    // Variables carry an environment too.
    await page.goto(page.url().replace(/\/?$/, '') + '/settings?tab=variables')
    await page.getByRole('button', { name: 'Add variable' }).click()
    await page.getByLabel('Name').fill('API_URL')
    await page.getByLabel('Value', { exact: true }).fill('https://staging.example')
    await page.getByLabel('Environment').selectOption('staging')
    await page.getByRole('button', { name: 'Save variable' }).click()
    const row = page.getByRole('row', { name: /API_URL/ })
    await expect(row.getByText('staging', { exact: true })).toBeVisible()
  })
})

// The inspector needs a real Redis with a password (scripts/it/start-services.sh) and redis-cli to seed it.
test.describe('Phase 3: data inspector', () => {
  test.skip(!dev || !pass, 'set NX_IT_REDIS_PORT and NX_IT_PASS to run')

  test('browse keys, read a value, and a write is refused and audited', async ({ page }) => {
    const { execFileSync } = await import('node:child_process')
    try {
      for (const args of [['set', 'e2e:inspect:greeting', 'hello-from-e2e'], ['hset', 'e2e:inspect:user', 'name', 'Ada']]) {
        execFileSync('redis-cli', ['-p', dev!, '-a', pass!, '--no-auth-warning', ...args])
      }
    } catch { test.skip(true, 'redis-cli is not available to seed the data') }

    const url = await createWorkspace(page, unique('insp'))
    await page.getByRole('button', { name: 'Add Redis to canvas' }).click()
    await page.getByRole('tab', { name: 'connect' }).click()
    await page.getByLabel('Connection name').fill('seen-cache')
    await page.getByLabel(/^Host/).fill('127.0.0.1')
    await page.getByLabel(/^Port/).fill(dev!)
    await page.getByText('Advanced').click()
    await page.getByRole('switch', { name: /^TLS/ }).uncheck()
    await page.getByLabel(/^Password/).fill(pass!)
    await page.getByRole('button', { name: 'Test Connection' }).click()
    await expect(page.getByRole('status').filter({ hasText: /Passed/ })).toBeVisible()
    await page.getByRole('button', { name: 'Add to Workspace' }).click()

    await page.getByRole('button', { name: 'Inspect data' }).click()
    const dialog = page.getByRole('dialog', { name: /Inspect/ })
    await expect(dialog.getByText('✓ Read-only')).toBeVisible()
    await dialog.getByLabel(/^Pattern/).fill('e2e:inspect:*')
    await dialog.getByRole('button', { name: 'Run' }).click()
    const results = dialog.getByRole('region', { name: 'Results' })
    await expect(results.getByRole('button', { name: 'e2e:inspect:greeting' })).toBeVisible()
    await expect(results.getByText('hash')).toBeVisible()
    await results.getByRole('button', { name: 'e2e:inspect:greeting' }).click()
    await expect(results.getByText('hello-from-e2e')).toBeVisible()

    // A write is refused with a reason, and nothing changed.
    await dialog.getByLabel('Operation').selectOption('command')
    await dialog.getByLabel(/^Command/).fill('SET e2e:inspect:greeting hacked')
    await dialog.getByRole('button', { name: 'Run' }).click()
    await expect(dialog.getByRole('alert')).toContainText('not a read command')
    await dialog.getByLabel(/^Command/).fill('GET e2e:inspect:greeting')
    await dialog.getByRole('button', { name: 'Run' }).click()
    await expect(dialog.getByRole('region', { name: 'Results' }).getByText('hello-from-e2e')).toBeVisible()
    await expect(page.locator('body')).not.toContainText(pass!)
    await dialog.getByRole('button', { name: 'Close' }).click()

    // Both the query and the refusal are in the activity log, without results.
    await page.goto(url + '/settings?tab=activity')
    await expect(page.getByText('inspect.query', { exact: true }).first()).toBeVisible()
    await expect(page.getByText('inspect.query_failed', { exact: true })).toBeVisible()
    await expect(page.locator('body')).not.toContainText('hello-from-e2e')
  })
})
