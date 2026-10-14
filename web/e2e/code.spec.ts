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
})
