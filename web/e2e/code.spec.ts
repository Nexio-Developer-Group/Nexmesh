import { expect, test, type Page } from '@playwright/test'

const unique = (p: string) => `${p}-${Math.random().toString(36).slice(2, 7)}`

async function createWorkspace(page: Page, name: string) {
  await page.goto('/workspaces')
  await page.getByRole('link', { name: 'Create Code Space' }).first().click()
  await page.getByLabel('Workspace name').fill(name)
  await expect(page.getByText('Available')).toBeVisible()
  await page.getByRole('button', { name: 'Create Code Space' }).click()
  await page.waitForURL(/\/w\/ws_[a-f0-9]+$/, { timeout: 30_000 })
}

test.describe('Phase 2 Code view', () => {
  test('edit a file in Monaco, save it, and see it from a real terminal', async ({ page }) => {
    await createWorkspace(page, unique('code'))
    await page.getByRole('tab', { name: 'Code' }).click()

    // The local agent seeds a README; the tree and agent link come up.
    const tree = page.getByRole('tree', { name: 'Workspace files' })
    await expect(tree.getByText('README.md')).toBeVisible({ timeout: 20_000 })

    // New file via the tree, opened in the editor.
    await page.getByRole('button', { name: 'New file' }).click()
    await page.getByLabel('Name').fill('hello.sh')
    await page.getByRole('button', { name: 'Create', exact: true }).click()
    const editor = page.locator('.monaco-editor').first()
    await expect(editor).toBeVisible({ timeout: 20_000 })

    await editor.click()
    await page.keyboard.type('echo nexmesh-says-$((6*7))')
    await expect(page.getByLabel('unsaved changes')).toBeVisible()
    await page.keyboard.press('Control+S')
    await expect(page.getByLabel('unsaved changes')).toBeHidden()

    // The terminal is a real shell in the workspace root: run the saved file.
    const term = page.getByRole('region', { name: /Terminal 1/ })
    await expect(term.locator('.xterm')).toBeVisible()
    await term.locator('.xterm-helper-textarea').focus()
    await page.keyboard.type('sh hello.sh\n')
    await expect(term.locator('.xterm-rows')).toContainText('nexmesh-says-42', { timeout: 15_000 })

    // A reload re-attaches to the same shell and replays its scrollback.
    await page.reload()
    await page.getByRole('tab', { name: 'Code' }).click()
    await expect(page.getByRole('region', { name: /Terminal 1/ }).locator('.xterm-rows')).toContainText('nexmesh-says-42', { timeout: 20_000 })
  })

  test('source control: stage and commit changes from the Git panel', async ({ page }) => {
    await createWorkspace(page, unique('git'))
    await page.getByRole('tab', { name: 'Code' }).click()
    await expect(page.getByRole('tree', { name: 'Workspace files' }).getByText('README.md')).toBeVisible({ timeout: 20_000 })

    // Make a repository with the real terminal, then see it in the panel.
    const term = page.getByRole('region', { name: /Terminal 1/ })
    await expect(term.locator('.xterm')).toBeVisible()
    await term.locator('.xterm-helper-textarea').focus()
    await page.keyboard.type('git init -q -b main . && echo ready-$((1+1))\n')
    await expect(term.locator('.xterm-rows')).toContainText('ready-2', { timeout: 15_000 })

    await page.getByRole('tab', { name: 'Source control' }).click()
    await expect(page.getByRole('region', { name: 'Untracked' }).getByText('README.md')).toBeVisible({ timeout: 15_000 })
    await page.getByRole('button', { name: 'Stage README.md' }).click({ force: true })
    await expect(page.getByRole('region', { name: 'Staged changes' }).getByText('README.md')).toBeVisible()
    await page.getByLabel('Commit message').fill('add readme')
    await page.getByRole('button', { name: /Commit 1 file/ }).click()
    await expect(page.getByText('No changes. Working tree clean.')).toBeVisible({ timeout: 15_000 })

    await page.getByRole('button', { name: 'History' }).click()
    await expect(page.getByRole('list', { name: 'Recent commits' })).toContainText('add readme')
  })

  test('ports: a server started in the terminal appears and opens in a sandboxed preview', async ({ page }) => {
    await createWorkspace(page, unique('ports'))
    await page.getByRole('tab', { name: 'Code' }).click()
    await expect(page.getByRole('tree', { name: 'Workspace files' }).getByText('README.md')).toBeVisible({ timeout: 20_000 })

    const term = page.getByRole('region', { name: /Terminal 1/ })
    await expect(term.locator('.xterm')).toBeVisible()
    await term.locator('.xterm-helper-textarea').focus()
    await page.keyboard.type('echo hello-preview-$((3*3)) > index.html && (python3 -m http.server 8123 --bind 127.0.0.1 >/dev/null 2>&1 & echo $! > .srv.pid)\n')

    await page.getByRole('tab', { name: 'Ports' }).click()
    const row = page.locator('li', { hasText: '8123' }).first()
    await expect(row.getByText('Listening')).toBeVisible({ timeout: 20_000 })
    await expect(row.getByText(/loopback only/)).toBeVisible()

    const popup = page.waitForEvent('popup')
    await row.getByRole('button', { name: 'Open preview of port 8123' }).click()
    const preview = await popup
    await expect(preview.locator('body')).toContainText('hello-preview-9', { timeout: 15_000 })
    // The previewed page is sandboxed: it cannot act as the signed-in Nexmesh user.
    const sandboxed = await preview.evaluate(() => { try { void localStorage.length; return false } catch { return true } })
    expect(sandboxed).toBe(true)

    await term.locator('.xterm-helper-textarea').focus()
    await page.keyboard.type('kill $(cat .srv.pid)\n')
  })
})
