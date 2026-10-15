import { defineConfig } from '@playwright/test'

// E2E runs the real binary (embedded UI, fake runtime driver). Set CHROME_PATH
// to use a preinstalled Chromium instead of Playwright's own download.
export default defineConfig({
  testDir: './e2e',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  retries: process.env.CI ? 2 : 0, // drag-and-drop timing is occasionally flaky on shared runners
  workers: 1,
  reporter: [['list']],
  use: {
    baseURL: 'http://127.0.0.1:18080',
    viewport: { width: 1440, height: 900 },
    trace: 'retain-on-failure',
    launchOptions: process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {},
  },
  webServer: [
    {
      command: 'bash e2e/start.sh',
      url: 'http://127.0.0.1:18080/healthz',
      reuseExistingServer: !process.env.CI,
      timeout: 240_000,
    },
    {
      // A second instance with sign-in enabled, for auth.spec.ts.
      command: 'E2E_PORT=18081 E2E_AUTH=enabled bash e2e/start.sh',
      url: 'http://127.0.0.1:18081/healthz',
      reuseExistingServer: !process.env.CI,
      timeout: 240_000,
    },
  ],
})
