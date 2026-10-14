import { expect, test } from '@playwright/test'

// Runs against the instance with sign-in enabled (port 18081, see playwright.config.ts).
test.use({ baseURL: 'http://127.0.0.1:18081' })

test.describe('accounts', () => {
  test('first visit creates the owner, sign-out and sign-in work, wrong passwords are refused', async ({ page, context }) => {
    // Unauthenticated: the app shows the sign-in screen and protects the API.
    await page.goto('/workspaces')
    await expect(page.getByRole('heading', { name: 'Sign in to Nexmesh' })).toBeVisible()
    const api = await page.request.get('/api/workspaces')
    expect(api.status()).toBe(401)

    // First run: create the owner account.
    await page.getByRole('button', { name: 'Create an account' }).click()
    await expect(page.getByText('The first account on this instance becomes its owner.')).toBeVisible()
    await page.getByLabel('Email').fill('owner@example.com')
    await page.getByLabel('Password').fill('short')
    await page.getByRole('button', { name: 'Create owner account' }).click()
    await expect(page.getByText('Use at least 10 characters.')).toBeVisible()
    await page.getByLabel('Password').fill('correct horse battery staple')
    await page.getByRole('button', { name: 'Create owner account' }).click()
    await expect(page.getByRole('heading', { name: 'Workspaces', level: 1 })).toBeVisible()

    // The session cookie is HttpOnly: scripts cannot read it.
    expect(await page.evaluate(() => document.cookie)).not.toContain('nexmesh_session')
    expect((await context.cookies()).find((c) => c.name === 'nexmesh_session')?.httpOnly).toBe(true)

    // Sign out returns to the sign-in screen and revokes access.
    await page.getByRole('button', { name: 'Account' }).click()
    await page.getByRole('menuitem', { name: 'Sign out' }).click()
    await expect(page.getByRole('heading', { name: 'Sign in to Nexmesh' })).toBeVisible()
    expect((await page.request.get('/api/workspaces')).status()).toBe(401)

    // Sign-up is closed now; a wrong password is refused with a neutral message.
    await expect(page.getByRole('button', { name: 'Create an account' })).toHaveCount(0)
    await page.getByLabel('Email').fill('owner@example.com')
    await page.getByLabel('Password').fill('not the password')
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByRole('alert')).toContainText('Email or password is incorrect.')

    await page.getByLabel('Password').fill('correct horse battery staple')
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByRole('heading', { name: 'Workspaces', level: 1 })).toBeVisible()
  })

  test('a second browser has its own session and cannot reach the first one\'s workspaces', async ({ browser }) => {
    const a = await browser.newContext({ baseURL: 'http://127.0.0.1:18081' })
    const pa = await a.newPage()
    await pa.goto('/workspaces')
    // Sign-up is closed (the owner exists), so a stranger sees only the sign-in form.
    await expect(pa.getByRole('heading', { name: 'Sign in to Nexmesh' })).toBeVisible()
    expect((await pa.request.get('/api/workspaces')).status()).toBe(401)
    expect((await pa.request.post('/api/auth/register', { data: { email: 'x@example.com', password: 'another long password' } })).status()).toBe(403)
    await a.close()
  })
})
