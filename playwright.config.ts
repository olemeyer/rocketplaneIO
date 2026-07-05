import { defineConfig, devices } from '@playwright/test';

// E2E-Tests gegen den Production-Build der Web-SPA (vite preview auf :4173).
// Playwright baut & startet den Server automatisch (webServer).
export default defineConfig({
  testDir: './e2e',
  outputDir: './e2e/.output',
  timeout: 30_000,
  expect: { timeout: 8_000 },
  fullyParallel: true,
  // Der Next-Dev-Server kompiliert Routen beim ersten Treffer -> unter paralleler
  // Last kann ein Erststart flaky sein. Ein Retry fängt das ab.
  retries: 1,
  reporter: [['list'], ['html', { outputFolder: 'playwright-report', open: 'never' }]],
  use: {
    baseURL: 'http://localhost:4173',
    viewport: { width: 1280, height: 900 },
    trace: 'on-first-retry',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    command: 'pnpm --filter @rocketplane/web build && pnpm --filter @rocketplane/web start',
    url: 'http://localhost:4173',
    reuseExistingServer: !process.env.CI,
    timeout: 180_000,
  },
});
