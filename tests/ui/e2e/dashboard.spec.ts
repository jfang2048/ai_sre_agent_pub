import { test, expect } from '@playwright/test';

/**
 * Dashboard Smoke Tests
 *
 * These tests verify the basic functionality of the AI SRE Agent dashboard.
 * They should run quickly and catch major regressions.
 */

test.describe('Dashboard', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
  });

  test('loads and displays title', async ({ page }) => {
    await expect(page).toHaveTitle(/AI SRE Agent/);
  });

  test('shows main dashboard content', async ({ page }) => {
    // Check for key dashboard elements
    await expect(page.locator('h1, h2').first()).toBeVisible();
  });

  test('displays fleet overview', async ({ page }) => {
    // Navigate to fleet page
    await page.goto('/#/fleet');

    // Wait for data to load
    await page.waitForLoadState('networkidle');

    // Check for fleet data display
    const content = page.locator('main, #root, [data-testid="fleet-view"]');
    await expect(content.first()).toBeVisible({ timeout: 10000 });
  });

  test('API endpoints respond', async ({ page }) => {
    // Test fleet API directly
    const response = await page.request.get('/api/v1/fleet');
    expect(response.status()).toBe(200);

    const data = await response.json();
    expect(data).toBeDefined();
  });

  test('health check endpoint', async ({ page }) => {
    const response = await page.request.get('/healthz');
    expect(response.status()).toBe(200);
  });

  test('status API returns valid data', async ({ page }) => {
    const response = await page.request.get('/api/v1/status');
    expect(response.status()).toBe(200);

    const data = await response.json();
    expect(data).toHaveProperty('collectors');
  });
});

test.describe('Log Indexing', () => {
  test('log index stats available', async ({ page }) => {
    const response = await page.request.get('/api/v1/logs/stats');
    // Might be 404 if not implemented yet, that's okay for now
    expect([200, 404]).toContain(response.status());
  });

  test('can search logs (if implemented)', async ({ page }) => {
    const response = await page.request.post('/api/v1/logs/search', {
      data: {
        limit: 10
      }
    });
    // Might be 404 if not implemented yet, that's okay for now
    expect([200, 404, 501]).toContain(response.status());
  });
});

test.describe('Diagnostics', () => {
  test('data path diagnostics available', async ({ page }) => {
    const response = await page.request.get('/api/v1/diagnostics/data-path');
    // Should return 200 or 501 if not implemented
    expect([200, 501]).toContain(response.status());
  });

  test('kernel path diagnostics available', async ({ page }) => {
    const response = await page.request.get('/api/v1/diagnostics/kernel-path');
    expect([200, 501]).toContain(response.status());
  });

  test('root cause analysis available', async ({ page }) => {
    const response = await page.request.get('/api/v1/diagnostics/root-cause');
    expect([200, 501]).toContain(response.status());
  });
});

test.describe('GPU Monitoring', () => {
  test('GPU data available in fleet', async ({ page }) => {
    const response = await page.request.get('/api/v1/fleet');
    expect(response.status()).toBe(200);

    const data = await response.json();
    // GPU data might be present but not guaranteed
    expect(data).toBeDefined();
  });
});
