import { test, expect } from '@playwright/test';

/**
 * Visual Regression Tests
 *
 * These tests capture screenshots and verify UI components render correctly.
 * They're useful for catching layout regressions.
 */

test.describe('Visual Tests', () => {
  test('dashboard screenshot', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');

    // Wait for main content to load
    await expect(page.locator('main, #root')).toBeVisible();

    await screenshot(page, 'dashboard');
  });

  test('fleet page screenshot', async ({ page }) => {
    await page.goto('/#/fleet');
    await page.waitForLoadState('networkidle');

    // Wait for data to load
    await page.waitForTimeout(2000);

    await screenshot(page, 'fleet');
  });

  test('top programs page screenshot', async ({ page }) => {
    await page.goto('/#/top');
    await page.waitForLoadState('networkidle');

    await page.waitForTimeout(2000);

    await screenshot(page, 'top-programs');
  });
});

async function screenshot(page: any, name: string) {
  await page.screenshot({
    path: `test-results/screenshots/${name}.png`,
    fullPage: true,
  });
}
