import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright configuration for AI SRE Agent UI tests
 *
 * These tests run against a running controller instance.
 * Use `make run-controller` or `make run-both` before running tests.
 */
export default defineConfig({
  testDir: './e2e',
  timeout: 30000,
  outputDir: 'test-results/artifacts',
  fullyParallel: false, // Run tests sequentially for now
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
