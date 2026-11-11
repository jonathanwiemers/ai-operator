import { defineConfig, devices } from '@playwright/test'

// Browser checks of the built Desk (docs/ui.md, "Checks"): e2e/serve.mjs
// serves dist/app with the server's security headers, the specs answer the
// API from e2e/fixtures. Run `npm run build` first, or `make ui-e2e`.
const port = 4173

export default defineConfig({
  testDir: 'e2e',
  testMatch: '**/*.spec.ts',
  outputDir: 'e2e-results/output',
  // Two workers keep the browsers within a small CI runner's memory.
  workers: 2,
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: [['list'], ['html', { outputFolder: 'e2e-results/report', open: 'never' }]],
  use: {
    ...devices['Desktop Chrome'],
    baseURL: `http://127.0.0.1:${port}`,
    // Desk follows the system's colour scheme; the screenshots show light,
    // but for the cases with their own colorScheme (overview-dark).
    colorScheme: 'light',
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium' }],
  webServer: {
    command: `node e2e/serve.mjs ${port}`,
    url: `http://127.0.0.1:${port}/`,
    reuseExistingServer: !process.env.CI,
    stdout: 'ignore',
  },
})
