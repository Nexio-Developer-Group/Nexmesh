import { expect, test, type Page } from '@playwright/test'
import { KEY, startScriptedModel } from './agent-model.mjs'

const unique = (p: string) => `${p}-${Math.random().toString(36).slice(2, 7)}`
const PORT = 19997

async function createWorkspace(page: Page, name: string) {
  await page.goto('/new')
  await page.getByLabel('Workspace name').fill(name)
  await expect(page.getByText('Available')).toBeVisible()
  await page.getByRole('button', { name: 'Create Code Space' }).click()
  await page.waitForURL(/\/w\/ws_[a-f0-9]+$/, { timeout: 30_000 })
  const id = page.url().split('/w/')[1]
  // Wait for the workspace agent so files can be written.
  await expect.poll(async () => (await page.request.get(`/api/workspaces/${id}/files`)).status(), { timeout: 20_000 }).toBe(200)
  return id
}

async function configureModel(page: Page, ws: string) {
  const cfg = { name: 'Scripted', provider: 'custom', protocol: 'openai', endpoint: `http://127.0.0.1:${PORT}`, modelId: 'scripted-1', apiKey: KEY }
  expect((await page.request.post('/api/agent/test', { data: cfg })).ok()).toBeTruthy()
  expect((await page.request.post(`/api/workspaces/${ws}/agent-configs`, { data: cfg })).status()).toBe(201)
}

const put = (page: Page, ws: string, path: string, content: string) =>
  page.request.put(`/api/workspaces/${ws}/files/content`, { data: { path, content } })
const read = async (page: Page, ws: string, path: string) => {
  const r = await page.request.get(`/api/workspaces/${ws}/files/content?path=${encodeURIComponent(path)}`)
  return r.ok() ? ((await r.json()).content as string) : null
}

const lines = (n: number) => Array.from({ length: n }, (_, i) => `line ${i + 1}`)

async function ask(page: Page, text: string) {
  await page.getByLabel('Message to the agent').fill(text)
  await page.getByRole('button', { name: 'Send' }).click()
}

test.describe('Phase 3: Code Agent', () => {
  test('agent proposes, the user reviews part of it, restores a checkpoint, and secrets stay out of reach', async ({ page }) => {
    const original = lines(40).join('\n') + '\n'
    const edited = lines(40).map((l, i) => (i === 2 ? 'EARLY CHANGE' : i === 36 ? 'LATE CHANGE' : l)).join('\n') + '\n'
    const model = await startScriptedModel([
      { tool: 'read_file', args: { path: 'notes.txt' } },
      { tool: 'read_file', args: { path: '.env' } },
      { tool: 'propose_changes', args: { title: 'Tidy notes', summary: 'Two small edits', changes: [{ path: 'notes.txt', content: edited }, { path: 'docs/new.md', content: '# New\n' }] } },
      { text: 'I proposed two edits for you to review.' },
    ])
    try {
      const ws = await createWorkspace(page, unique('agent'))
      await configureModel(page, ws)
      await put(page, ws, 'notes.txt', original)
      await put(page, ws, '.env', 'API_SECRET=sk_live_VERY_SECRET_VALUE_1234567890\n')
      await page.request.put(`/api/workspaces/${ws}/rules`, { data: { title: 'House style', scope: 'global', components: [], paths: [], protect: [], body: 'Keep edits small.' } })

      await page.goto(`/w/${ws}`)
      await page.getByRole('button', { name: 'Code Agent' }).click()
      const panel = page.getByRole('complementary', { name: 'Code Agent' })
      await ask(page, 'tidy the notes')
      await expect(panel.getByText('I proposed two edits for you to review.')).toBeVisible({ timeout: 20_000 })
      await expect(panel.getByText('Read notes.txt')).toBeVisible()
      await expect(panel.getByText('Not allowed', { exact: true })).toBeVisible() // the .env read
      await expect(page.locator('body')).not.toContainText('VERY_SECRET_VALUE')
      // Nothing is written before review.
      expect(await read(page, ws, 'notes.txt')).toBe(original)

      await panel.getByRole('button', { name: 'Review changes' }).click()
      const dialog = page.getByRole('dialog', { name: 'Tidy notes' })
      await expect(dialog.getByText('Proposed by the Code Agent')).toBeVisible()
      await expect(dialog.getByText('House style')).toBeVisible() // applied rules
      await expect(dialog.getByText('EARLY CHANGE')).toBeVisible()
      await expect(dialog.getByText('LATE CHANGE')).toBeVisible()
      // Accept only the late change and the new file.
      await dialog.getByRole('checkbox', { name: 'Accept change 1 in notes.txt' }).uncheck()
      await dialog.getByRole('button', { name: 'Accept 2 of 3 changes' }).click()
      await expect(dialog).toHaveCount(0)
      const after = (await read(page, ws, 'notes.txt'))!
      expect(after).toContain('LATE CHANGE')
      expect(after).not.toContain('EARLY CHANGE')
      expect(await read(page, ws, 'docs/new.md')).toBe('# New\n')

      // Restore the checkpoint: everything goes back.
      await panel.getByRole('tab', { name: 'Changes' }).click()
      await panel.getByRole('button', { name: 'Restore checkpoint Tidy notes' }).click()
      await page.getByRole('dialog').getByRole('button', { name: 'Restore files' }).click()
      await expect.poll(() => read(page, ws, 'notes.txt')).toBe(original)
      expect(await read(page, ws, 'docs/new.md')).toBeNull()

      // The model never saw the secret.
      expect(JSON.stringify(model.seen)).not.toContain('VERY_SECRET_VALUE')
      expect(JSON.stringify(model.seen)).toContain('Keep edits small.')
    } finally {
      model.close()
    }
  })

  test('a required failing gate blocks acceptance until it is overridden with a recorded reason', async ({ page }) => {
    const model = await startScriptedModel([
      { tool: 'propose_changes', args: { title: 'Add marker', changes: [{ path: 'app.txt', content: 'ok\nBROKEN\n' }] } },
      { text: 'Proposed.' },
    ])
    try {
      const ws = await createWorkspace(page, unique('gates'))
      await configureModel(page, ws)
      await put(page, ws, 'app.txt', 'ok\n')
      // Configure a gate in Settings, the way a user would.
      await page.goto(`/w/${ws}/settings?tab=gates`)
      await page.getByRole('button', { name: 'Add a gate' }).click()
      await page.getByLabel('Name').fill('No BROKEN marker')
      await page.getByLabel('Command').fill('! grep -q BROKEN app.txt')
      await page.getByLabel('Policy').selectOption('required')
      await page.getByRole('button', { name: 'Confirm and save gates' }).click()
      await expect(page.getByRole('button', { name: 'Save gates' })).toBeDisabled()

      await page.goto(`/w/${ws}`)
      await page.getByRole('button', { name: 'Code Agent' }).click()
      const panel = page.getByRole('complementary', { name: 'Code Agent' })
      await ask(page, 'add the marker')
      await expect(panel.getByText('Proposed.')).toBeVisible({ timeout: 20_000 })
      await panel.getByRole('button', { name: 'Review changes' }).click()
      const dialog = page.getByRole('dialog', { name: 'Add marker' })
      await expect(dialog.getByText('Required gates have not run on this change')).toBeVisible()
      await expect(dialog.getByRole('button', { name: 'Accept all' })).toBeDisabled()
      await dialog.getByRole('button', { name: 'Run quality gates' }).click()
      await expect(dialog.getByText(/Required gates failed: No BROKEN marker/)).toBeVisible({ timeout: 20_000 })
      expect(await read(page, ws, 'app.txt')).toBe('ok\n') // the trial left nothing behind
      await expect(dialog.getByRole('button', { name: 'Accept all' })).toBeDisabled()
      await dialog.getByRole('button', { name: 'Override the gates…' }).click()
      await dialog.getByLabel(/Reason for accepting despite the gates/).fill('prototype, will fix tomorrow')
      await dialog.getByRole('button', { name: 'Accept all' }).click()
      await expect(dialog).toHaveCount(0)
      expect(await read(page, ws, 'app.txt')).toContain('BROKEN')
      const audit = await (await page.request.get(`/api/workspaces/${ws}/audit`)).text()
      expect(audit).toContain('gates.overridden')
    } finally {
      model.close()
    }
  })

  test('risky commands wait for approval, and Explain on a card shows exactly what is sent', async ({ page }) => {
    const model = await startScriptedModel([
      { tool: 'run_command', args: { command: 'rm -rf build' } },
      { text: 'Understood, I will not delete it.' },
      { text: 'Redis is an in-memory data store.' },
    ])
    try {
      const ws = await createWorkspace(page, unique('approve'))
      await configureModel(page, ws)
      await page.request.put(`/api/workspaces/${ws}/agent/settings`, { data: { level: 'execute', autoAccept: false } })
      await page.goto(`/w/${ws}`)
      await page.getByRole('button', { name: 'Code Agent' }).click()
      const panel = page.getByRole('complementary', { name: 'Code Agent' })
      await ask(page, 'clean the build folder')
      const approval = panel.getByRole('region', { name: 'Approval needed' })
      await expect(approval).toContainText('rm -rf build', { timeout: 20_000 })
      await approval.getByRole('button', { name: 'Decline' }).click()
      await expect(panel.getByText('Understood, I will not delete it.')).toBeVisible({ timeout: 20_000 })
      await expect(panel.getByText('Declined')).toBeVisible()

      // Explain a card: preview first, then send exactly that.
      await page.getByRole('button', { name: 'Add Redis to canvas' }).click()
      await page.getByRole('button', { name: 'Explain' }).click()
      const dlg = page.getByRole('dialog', { name: 'Explain Redis' })
      const text = dlg.getByLabel('Text that will be sent')
      await expect(text).toContainText('Redis')
      const previewed = await text.innerText()
      await dlg.getByRole('button', { name: 'Send to the agent' }).click()
      await expect(panel.getByText('Redis is an in-memory data store.')).toBeVisible({ timeout: 20_000 })
      const sent = model.seen[model.seen.length - 1].messages.find((m: { role: string }) => m.role === 'user').content as string
      expect(sent.trim()).toBe(previewed.trim())
    } finally {
      model.close()
    }
  })
})
