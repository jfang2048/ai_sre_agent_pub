import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright configuration for AI SRE Agent UI tests.
 *
 * Preferred entrypoint: `make test-ui` (auto-bootstraps a local stack).
 * Direct `npm test` usage assumes BASE_URL (or default localhost:8080) is already reachable.
 */
export default defineConfig({
  testDir: './e2e',
  timeout: 30000,
  outputDir: 'test-results/artifacts',
  fullyParallel: false, // Keep deterministic sequencing for shared local stack resources.
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: 1,
  reporter: [
    ['list'],
    ['html', { outputFolder: 'test-results/html' }]
  ],

  use: {
    baseURL: process.env.BASE_URL || 'http://127.0.0.1:8080',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],

  // Run local dev server before starting tests (optional)
  // webServer: {
  //   command: 'make run-controller',
  //   url: 'http://127.0.0.1:8080',
  //   reuseExistingServer: !process.env.CI,
  //   timeout: 120000,
  // },
});
