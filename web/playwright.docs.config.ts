import { defineConfig, devices } from '@playwright/test'

// The Desk screenshots of the docs (docs/ui.md, "Docs screenshots"):
// e2e/docs.shots.ts opens a few pages of the built Desk on the browser
// checks' fake data and saves PNGs to e2e-results/docs; `make
// docs-screenshots` turns them into docs/assets/screenshots/*.webp. Not part
// of `npm run e2e` (its testMatch is *.spec.ts).
const port = 4174

export default defineConfig({
  testDir: 'e2e',
  testMatch: '**/*.shots.ts',
  // Playwright empties it at the start of a run, so no stale shots survive.
  outputDir: 'e2e-results/docs',
  workers: 2,
  fullyParallel: true,
  retries: 0,
  reporter: [['list']],
  use: {
    ...devices['Desktop Chrome'],
    baseURL: `http://127.0.0.1:${port}`,
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
    colorScheme: 'light',
  },
  projects: [{ name: 'chromium' }],
  webServer: {
    command: `node e2e/serve.mjs ${port}`,
    url: `http://127.0.0.1:${port}/`,
    reuseExistingServer: false,
    stdout: 'ignore',
  },
})
